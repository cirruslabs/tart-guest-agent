package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/samber/lo"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	standardStreamsBufferSize = 4096

	eofChar = 0x04
)

type execResponseSender struct {
	stream grpc.BidiStreamingServer[ExecRequest, ExecResponse]
	mu     sync.Mutex
}

func (sender *execResponseSender) send(response *ExecResponse) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()

	return sender.stream.Send(response)
}

func (rpc *RPC) Exec(stream grpc.BidiStreamingServer[ExecRequest, ExecResponse]) error {
	// Read the first exec request, it should describe a command to execute
	firstExecRequest, err := stream.Recv()
	if err != nil {
		return err
	}
	firstExecRequestCommand, ok := firstExecRequest.Type.(*ExecRequest_Command_)
	if !ok {
		return fmt.Errorf("first exec request should describe a command to execute")
	}

	zap.S().Infof("executing %s", formatCommandAndArgs(firstExecRequestCommand.Command.Name,
		firstExecRequestCommand.Command.Args))

	if firstExecRequestCommand.Command.Detach &&
		(firstExecRequestCommand.Command.Interactive || firstExecRequestCommand.Command.Tty) {
		return fmt.Errorf("detach cannot be used with interactive or tty")
	}

	// Execute the command
	execCtx, cancelExec := context.WithCancel(stream.Context())
	defer cancelExec()

	if firstExecRequestCommand.Command.Detach {
		execCtx = context.Background()
	}

	cmd := exec.CommandContext(execCtx, firstExecRequestCommand.Command.Name,
		firstExecRequestCommand.Command.Args...)
	applyExecOverrides(cmd, firstExecRequestCommand.Command)
	responseSender := &execResponseSender{stream: stream}

	if firstExecRequestCommand.Command.Detach {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

		if err := cmd.Start(); err != nil {
			return err
		}
		if cmd.Process != nil {
			if err := cmd.Process.Release(); err != nil {
				return err
			}
		}

		if err := responseSender.send(&ExecResponse{
			Type: &ExecResponse_Exit_{
				Exit: &ExecResponse_Exit{
					Code: 0,
				},
			},
		}); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}

		return nil
	}

	var stdin io.WriteCloser
	var stdout, stderr io.ReadCloser
	var ptmx *os.File

	if firstExecRequestCommand.Command.Tty {
		ptmx, err = pty.StartWithSize(cmd, &pty.Winsize{
			Rows: uint16(firstExecRequestCommand.Command.GetTerminalSize().GetRows()),
			Cols: uint16(firstExecRequestCommand.Command.GetTerminalSize().GetCols()),
		})

		if firstExecRequestCommand.Command.Interactive {
			stdin = ptmx
		}
		stdout = ptmx
		stderr = ptmx
	} else {
		if firstExecRequestCommand.Command.Interactive {
			stdin, err = cmd.StdinPipe()
			if err != nil {
				return err
			}
		}

		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return err
		}

		stderr, err = cmd.StderrPipe()
		if err != nil {
			return err
		}

		// Give each attached command its own process group. PTY commands already
		// receive a dedicated session and process group from pty.StartWithSize.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		err = cmd.Start()
	}
	if err != nil {
		return err
	}
	if ptmx != nil {
		defer ptmx.Close()
	}

	// Send the managed guest PID before starting any output readers, so even
	// commands that finish immediately cannot produce output before Started.
	if err := responseSender.send(&ExecResponse{
		Type: &ExecResponse_Started_{
			Started: &ExecResponse_Started{Pid: uint32(cmd.Process.Pid)},
		},
	}); err != nil {
		cancelExec()
		_ = cmd.Wait()
		return err
	}

	// Handle standard input, terminal resize, and signals from this stream only.
	fromClientErrCh := make(chan error, 1)
	reportClientError := func(err error) {
		select {
		case fromClientErrCh <- err:
		default:
		}
		cancelExec()
	}

	var signalMu sync.Mutex
	processExited := false
	seenSignalRequests := make(map[uint64]struct{})

	go func() {
		for {
			request, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) &&
					status.Code(err) != codes.Canceled {
					reportClientError(err)
				}

				return
			}

			switch typedAction := request.Type.(type) {
			case *ExecRequest_StandardInput:
				if !firstExecRequestCommand.Command.Interactive {
					// Ignore standard input from the client
					// as non-interactive command is running
					continue
				}

				dataToWrite := typedAction.StandardInput.Data

				// Check if the remote client has received EOF on their standard input
				if len(typedAction.StandardInput.Data) == 0 {
					if firstExecRequestCommand.Command.Tty {
						// When using pseudo-terminal, we can't simply close the
						// standard input, as the file descriptor is shared for
						// standard output and standard error too, so we send
						// an EOF character instead
						dataToWrite = []byte{eofChar}
					} else {
						// Close the standard input
						if err := stdin.Close(); err != nil {
							reportClientError(err)

							return
						}

						continue
					}
				}

				if _, err := stdin.Write(dataToWrite); err != nil {
					reportClientError(err)

					return
				}
			case *ExecRequest_TerminalResize:
				// Ignore terminal resize requests
				// when pseudo terminal is disabled
				if !firstExecRequestCommand.Command.Tty {
					continue
				}

				if err := pty.Setsize(ptmx, &pty.Winsize{
					Rows: uint16(typedAction.TerminalResize.GetRows()),
					Cols: uint16(typedAction.TerminalResize.GetCols()),
				}); err != nil {
					reportClientError(err)

					return
				}
			case *ExecRequest_Signal_:
				signalRequest := typedAction.Signal
				if signalRequest == nil || signalRequest.RequestId == 0 {
					reportClientError(status.Error(codes.InvalidArgument,
						"signal request_id must be nonzero"))
					return
				}
				if _, seen := seenSignalRequests[signalRequest.RequestId]; seen {
					reportClientError(status.Errorf(codes.InvalidArgument,
						"signal request_id %d has already been used", signalRequest.RequestId))
					return
				}
				seenSignalRequests[signalRequest.RequestId] = struct{}{}

				if err := func() error {
					signalMu.Lock()
					defer signalMu.Unlock()

					if processExited {
						return status.Error(codes.FailedPrecondition,
							"managed process has already exited")
					}
					if err := deliverExecSignal(cmd.Process, signalRequest); err != nil {
						return err
					}

					return responseSender.send(&ExecResponse{
						Type: &ExecResponse_SignalAck_{
							SignalAck: &ExecResponse_SignalAck{
								RequestId: signalRequest.RequestId,
							},
						},
					})
				}(); err != nil {
					reportClientError(err)
					return
				}
			}
		}
	}()

	group, _ := errgroup.WithContext(stream.Context())

	// Handle standard output from the command
	group.Go(func() error {
		buf := make([]byte, standardStreamsBufferSize)

		for {
			n, err := stdout.Read(buf)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}

				// PTY way of signalling io.EOF
				if ptmx != nil && strings.Contains(err.Error(), "input/output error") {
					return nil
				}

				return err
			}

			if err := responseSender.send(&ExecResponse{
				Type: &ExecResponse_StandardOutput{
					StandardOutput: &IOChunk{
						Data: slices.Clone(buf[:n]),
					},
				},
			}); err != nil {
				return err
			}
		}
	})

	// Handle standard error from the command
	//
	// Note that it makes no sense to handle standard error when TTY is requested
	// because in this case stdout and stderr will point to the same file descriptor
	if !firstExecRequestCommand.Command.Tty {
		group.Go(func() error {
			buf := make([]byte, standardStreamsBufferSize)

			for {
				n, err := stderr.Read(buf)
				if err != nil {
					if errors.Is(err, io.EOF) {
						return nil
					}

					return err
				}

				if err := responseSender.send(&ExecResponse{
					Type: &ExecResponse_StandardError{
						StandardError: &IOChunk{
							Data: slices.Clone(buf[:n]),
						},
					},
				}); err != nil {
					return err
				}
			}
		})
	}

	if err := group.Wait(); err != nil {
		zap.S().Warnf("%v", err)
	}

	// Wait for the command to finish before allowing the final exit response.
	waitErr := cmd.Wait()
	signalMu.Lock()
	defer signalMu.Unlock()
	processExited = true

	select {
	case err := <-fromClientErrCh:
		return err
	default:
	}

	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			return waitErr
		}
	}

	return responseSender.send(&ExecResponse{
		Type: &ExecResponse_Exit_{
			Exit: &ExecResponse_Exit{
				Code: int32(exitCode),
			},
		},
	})
}

func deliverExecSignal(process *os.Process, request *ExecRequest_Signal) error {
	var signal syscall.Signal
	switch request.Signal {
	case uint32(syscall.SIGHUP), uint32(syscall.SIGINT), uint32(syscall.SIGQUIT),
		uint32(syscall.SIGKILL), uint32(syscall.SIGTERM), uint32(syscall.SIGUSR1),
		uint32(syscall.SIGUSR2), uint32(syscall.SIGCONT), uint32(syscall.SIGSTOP),
		uint32(syscall.SIGTSTP), uint32(syscall.SIGWINCH):
		signal = syscall.Signal(request.Signal)
	default:
		return status.Errorf(codes.InvalidArgument,
			"unsupported signal %d", request.Signal)
	}

	var err error
	if request.All {
		// Every attached command leads its own process group, so a negative
		// managed PID cannot signal the agent or another execution.
		err = syscall.Kill(-process.Pid, signal)
	} else {
		err = process.Signal(signal)
	}
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return status.Errorf(codes.FailedPrecondition,
				"managed process is no longer running: %v", err)
		}
		if errors.Is(err, syscall.EPERM) {
			return status.Errorf(codes.PermissionDenied,
				"cannot signal managed process: %v", err)
		}
		return status.Errorf(codes.Internal,
			"cannot deliver signal %d to managed process: %v", request.Signal, err)
	}

	return nil
}

func applyExecOverrides(cmd *exec.Cmd, command *ExecRequest_Command) {
	if command.Workdir != "" {
		cmd.Dir = command.Workdir
	}

	if len(command.Env) > 0 {
		cmd.Env = mergeEnv(command.Env)
	}
}

func mergeEnv(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}

	envMap := make(map[string]string, len(overrides))
	for _, entry := range os.Environ() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		envMap[parts[0]] = parts[1]
	}

	for key, value := range overrides {
		envMap[key] = value
	}

	merged := make([]string, 0, len(envMap))
	for key, value := range envMap {
		merged = append(merged, key+"="+value)
	}

	return merged
}

func formatCommandAndArgs(name string, args []string) string {
	var all []string

	all = append(all, name)
	all = append(all, args...)

	all = lo.Map(all, func(item string, _ int) string {
		return fmt.Sprintf("%q", item)
	})

	return fmt.Sprintf("[%s]", strings.Join(all, ", "))
}

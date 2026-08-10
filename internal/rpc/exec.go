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
)

const (
	standardStreamsBufferSize = 4096

	eofChar = 0x04

	// execRuntimeFailureExitCode matches Docker's exit code for runtime failures before a process starts.
	execRuntimeFailureExitCode = 125
	// signalExitCodeOffset is the base for shell-style exit codes of processes terminated by signals.
	signalExitCodeOffset = 128
)

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
	execCtx := stream.Context()
	if firstExecRequestCommand.Command.Detach {
		execCtx = context.Background()
	}

	cmd := exec.CommandContext(execCtx, firstExecRequestCommand.Command.Name,
		firstExecRequestCommand.Command.Args...)
	applyExecOverrides(cmd, firstExecRequestCommand.Command)

	if firstExecRequestCommand.Command.Detach {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

		if err := cmd.Start(); err != nil {
			zap.S().Warnf("failed to start %s: %v", formatCommandAndArgs(firstExecRequestCommand.Command.GetName(),
				firstExecRequestCommand.Command.GetArgs()), err)

			return sendStartFailure(stream)
		}

		// Explicitly notify the client that the process was started
		err = sendStartSuccess(stream)
		if err != nil {
			return err
		}

		if cmd.Process != nil {
			if err := cmd.Process.Release(); err != nil {
				return err
			}
		}

		if err := stream.Send(&ExecResponse{
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

		err = cmd.Start()
	}

	if err != nil {
		zap.S().Warnf("failed to start %s: %v", formatCommandAndArgs(firstExecRequestCommand.Command.GetName(),
			firstExecRequestCommand.Command.GetArgs()), err)

		return sendStartFailure(stream)
	}

	// Explicitly notify the client that the process was started
	err = sendStartSuccess(stream)
	if err != nil {
		return err
	}

	if ptmx != nil {
		defer ptmx.Close()
	}

	// Handle standard input and terminal resize events from the client
	fromClientErrCh := make(chan error, 1)

	go func() {
		for {
			request, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					fromClientErrCh <- err
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
							fromClientErrCh <- err

							return
						}

						continue
					}
				}

				if _, err := stdin.Write(dataToWrite); err != nil {
					fromClientErrCh <- err

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
					fromClientErrCh <- err

					return
				}
			case *ExecRequest_Signal_:
				var signal syscall.Signal

				switch typedAction.Signal {
				case ExecRequest_SIGNAL_SIGTERM:
					signal = syscall.SIGTERM
				case ExecRequest_SIGNAL_SIGKILL:
					signal = syscall.SIGKILL
				default:
					fromClientErrCh <- fmt.Errorf("unsupported exec signal %q", typedAction.Signal.String())

					return
				}

				if err := cmd.Process.Signal(signal); err != nil && !errors.Is(err, os.ErrProcessDone) {
					fromClientErrCh <- err

					return
				}
			}
		}
	}()

	// Serialize responses from the stdout and stderr goroutines
	var sendMutex sync.Mutex

	sendResponse := func(response *ExecResponse) error {
		sendMutex.Lock()
		defer sendMutex.Unlock()

		return stream.Send(response)
	}

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

			if err := sendResponse(&ExecResponse{
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

				if err := sendResponse(&ExecResponse{
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

	// Wait for the command to finish
	exitCode := 0

	if err := cmd.Wait(); err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			return err
		}

		// ExitCode returns -1 for signals; report the containerd-compatible 128 + signal instead
		exitCode = exitError.ExitCode()

		if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
			exitCode = signalExitCodeOffset + int(waitStatus.Signal())
		}
	}

	return stream.Send(&ExecResponse{
		Type: &ExecResponse_Exit_{
			Exit: &ExecResponse_Exit{
				Code: int32(exitCode),
			},
		},
	})
}

func sendStartSuccess(stream grpc.BidiStreamingServer[ExecRequest, ExecResponse]) error {
	return stream.Send(&ExecResponse{
		Type: &ExecResponse_Started_{
			Started: &ExecResponse_Started{},
		},
	})
}

func sendStartFailure(stream grpc.BidiStreamingServer[ExecRequest, ExecResponse]) error {
	return stream.Send(&ExecResponse{
		Type: &ExecResponse_Exit_{
			Exit: &ExecResponse_Exit{
				Code: execRuntimeFailureExitCode,
			},
		},
	})
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

package rpc

import (
	"context"
	"errors"
	"fmt"
	"github.com/creack/pty"
	"github.com/samber/lo"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

const (
	standardStreamsBufferSize = 4096

	eofChar = 0x04
)

type standardStreamOutput struct {
	Data []byte
	Err  error
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

	// Execute the command
	cmd := exec.CommandContext(stream.Context(), firstExecRequestCommand.Command.Name,
		firstExecRequestCommand.Command.Args...)

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
			}
		}
	}()

	// Handle standard output from the command
	stdoutOutputCh := make(chan *standardStreamOutput, 1)

	go streamStandardStream(stdout, stdoutOutputCh)

	// Handle standard error from the command
	//
	// Note that it makes no sense to handle standard error when TTY is requested
	// because in this case stdout and stderr will point to the same file descriptor
	stderrOutputCh := make(chan *standardStreamOutput, 1)

	if !firstExecRequestCommand.Command.Tty {
		go streamStandardStream(stderr, stderrOutputCh)
	}

	// Wait for the command to finish
	commandErrCh := make(chan error, 1)
	go func() {
		commandErrCh <- cmd.Wait()
	}()

	for {
		select {
		case stdoutOutput := <-stdoutOutputCh:
			if err := stdoutOutput.Err; err != nil {
				return err
			}

			if err := stream.Send(&ExecResponse{
				Type: &ExecResponse_StandardOutput{
					StandardOutput: &IOChunk{
						Data: stdoutOutput.Data,
					},
				},
			}); err != nil {
				return err
			}
		case stderrOutput := <-stderrOutputCh:
			if err := stderrOutput.Err; err != nil {
				return err
			}

			if err := stream.Send(&ExecResponse{
				Type: &ExecResponse_StandardError{
					StandardError: &IOChunk{
						Data: stderrOutput.Data,
					},
				},
			}); err != nil {
				return err
			}
		case commandErr := <-commandErrCh:
			exitCode := 0

			var exitError *exec.ExitError
			if errors.As(commandErr, &exitError) {
				exitCode = exitError.ExitCode()
			}

			return stream.Send(&ExecResponse{
				Type: &ExecResponse_Exit_{
					Exit: &ExecResponse_Exit{
						Code: int32(exitCode),
					},
				},
			})
		}
	}
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

func streamStandardStream(standardStream io.Reader, outputCh chan *standardStreamOutput) {
	buf := make([]byte, standardStreamsBufferSize)

	for {
		n, err := standardStream.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				outputCh <- &standardStreamOutput{
					Err: err,
				}
			}

			return
		}

		outputCh <- &standardStreamOutput{
			Data: slices.Clone(buf[:n]),
		}
	}
}

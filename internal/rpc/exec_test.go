// In-process stream scaffolding intentionally favors direct test construction.
//
//nolint:containedctx,testpackage,wsl_v5
package rpc

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/cirruslabs/tart-guest-agent/pkg/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const (
	execTestShell   = "/bin/sh"
	execTestTimeout = 5 * time.Second
)

type execTestStream struct {
	grpc.ServerStream

	ctx       context.Context
	requests  chan *v1.ExecRequest
	responses chan *v1.ExecResponse
	sendHook  func(*v1.ExecResponse) error
}

var _ grpc.BidiStreamingServer[v1.ExecRequest, v1.ExecResponse] = (*execTestStream)(nil)

func newExecTestStream(ctx context.Context) *execTestStream {
	return &execTestStream{
		ctx:       ctx,
		requests:  make(chan *v1.ExecRequest, 8),
		responses: make(chan *v1.ExecResponse, 8),
	}
}

func (stream *execTestStream) Send(response *v1.ExecResponse) error {
	if stream.sendHook != nil {
		if err := stream.sendHook(response); err != nil {
			return err
		}
	}

	select {
	case stream.responses <- response:
		return nil
	case <-stream.ctx.Done():
		return stream.ctx.Err()
	}
}

func (stream *execTestStream) Recv() (*v1.ExecRequest, error) {
	select {
	case request, ok := <-stream.requests:
		if !ok {
			return nil, io.EOF
		}
		return request, nil
	case <-stream.ctx.Done():
		return nil, stream.ctx.Err()
	}
}

func (stream *execTestStream) Context() context.Context { return stream.ctx }

func TestExecSendsStartedBeforeOutputAndExit(t *testing.T) {
	_, stream, result := startExecTest(t, &v1.ExecRequest_Command{
		Name: execTestShell,
		Args: []string{"-c", "printf hello"},
	})

	first := receiveExecResponse(t, stream)
	require.NotNil(t, first.GetStarted())

	var output []byte
	for {
		response := receiveExecResponse(t, stream)
		switch response := response.GetType().(type) {
		case *v1.ExecResponse_StandardOutput:
			output = append(output, response.StandardOutput.GetData()...)
		case *v1.ExecResponse_Exit_:
			require.EqualValues(t, 0, response.Exit.GetCode())
			require.Equal(t, []byte("hello"), output)
			require.NoError(t, receiveExecResult(t, result))
			return
		default:
			t.Fatalf("unexpected exec response %T", response)
		}
	}
}

func TestExecClosesStandardInputOnRequestStreamEOF(t *testing.T) {
	_, stream, result := startExecTest(t, &v1.ExecRequest_Command{
		Name:        "/bin/cat",
		Interactive: true,
	})
	require.NotNil(t, receiveExecResponse(t, stream).GetStarted())

	stream.requests <- &v1.ExecRequest{
		Type: &v1.ExecRequest_StandardInput{
			StandardInput: &v1.IOChunk{Data: []byte("hello")},
		},
	}
	close(stream.requests)

	response := receiveExecResponse(t, stream)
	require.Equal(t, []byte("hello"), response.GetStandardOutput().GetData())
	response = receiveExecResponse(t, stream)
	require.EqualValues(t, 0, response.GetExit().GetCode())
	require.NoError(t, receiveExecResult(t, result))
}

func TestExecClosesStandardInputOnEmptyChunk(t *testing.T) {
	_, stream, result := startExecTest(t, &v1.ExecRequest_Command{
		Name:        "/bin/cat",
		Interactive: true,
	})
	require.NotNil(t, receiveExecResponse(t, stream).GetStarted())

	stream.requests <- &v1.ExecRequest{
		Type: &v1.ExecRequest_StandardInput{
			StandardInput: &v1.IOChunk{Data: []byte("hello")},
		},
	}
	stream.requests <- &v1.ExecRequest{
		Type: &v1.ExecRequest_StandardInput{
			StandardInput: &v1.IOChunk{},
		},
	}

	response := receiveExecResponse(t, stream)
	require.Equal(t, []byte("hello"), response.GetStandardOutput().GetData())
	response = receiveExecResponse(t, stream)
	require.EqualValues(t, 0, response.GetExit().GetCode())
	require.NoError(t, receiveExecResult(t, result))
}

func TestExecReportsStartFailureBeforeStarted(t *testing.T) {
	tests := []struct {
		name    string
		command *v1.ExecRequest_Command
	}{
		{
			name: "missing executable",
			command: &v1.ExecRequest_Command{
				Name: "/definitely/missing/tart-guest-agent-test-command",
			},
		},
		{
			name: "missing workdir",
			command: &v1.ExecRequest_Command{
				Name:    execTestShell,
				Workdir: "/definitely/missing/tart-guest-agent-test-workdir",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, stream, result := startExecTest(t, test.command)
			response := receiveExecResponse(t, stream)
			require.Nil(t, response.GetStarted())
			require.EqualValues(t, execRuntimeFailureExitCode, response.GetExit().GetCode())
			require.NoError(t, receiveExecResult(t, result))
		})
	}
}

func TestExecSignalsProcess(t *testing.T) {
	tests := []struct {
		name   string
		signal v1.SignalRequest_Signal
		code   int32
		err    string
	}{
		{
			name:   "SIGTERM",
			signal: v1.SignalRequest_SIGNAL_SIGTERM,
			code:   int32(signalExitCodeOffset + syscall.SIGTERM),
		},
		{
			name:   "SIGKILL",
			signal: v1.SignalRequest_SIGNAL_SIGKILL,
			code:   int32(signalExitCodeOffset + syscall.SIGKILL),
		},
		{
			name:   "unsupported",
			signal: v1.SignalRequest_SIGNAL_UNSPECIFIED,
			err:    `unsupported exec signal "SIGNAL_UNSPECIFIED"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rpc, stream, result := startExecTest(t, &v1.ExecRequest_Command{
				Name: "/bin/sleep",
				Args: []string{"30"},
			})
			started := receiveExecResponse(t, stream).GetStarted()
			require.NotNil(t, started)

			_, err := rpc.Signal(context.Background(), &v1.SignalRequest{
				ExecId: started.GetExecId(),
				Signal: test.signal,
			})

			if test.err != "" {
				require.EqualError(t, err, test.err)
				_, err = rpc.Signal(context.Background(), &v1.SignalRequest{
					ExecId: started.GetExecId(),
					Signal: v1.SignalRequest_SIGNAL_SIGKILL,
				})
				require.NoError(t, err)
				receiveExecResponse(t, stream)
				require.NoError(t, receiveExecResult(t, result))

				return
			}
			require.NoError(t, err)

			response := receiveExecResponse(t, stream)
			require.NotNil(t, response.GetExit())
			require.Equal(t, test.code, response.GetExit().GetCode())
			require.NoError(t, receiveExecResult(t, result))
		})
	}
}

func TestExecSignalsProcessGroup(t *testing.T) {
	rpc, stream, result := startExecTest(t, &v1.ExecRequest_Command{
		Name: execTestShell,
		Args: []string{"-c", "sleep 30 & printf ready; wait"},
	})
	started := receiveExecResponse(t, stream).GetStarted()
	require.NotNil(t, started)
	require.Equal(t, []byte("ready"), receiveExecResponse(t, stream).GetStandardOutput().GetData())

	_, err := rpc.Signal(context.Background(), &v1.SignalRequest{
		ExecId: started.GetExecId(),
		Signal: v1.SignalRequest_SIGNAL_SIGTERM,
	})
	require.NoError(t, err)

	response := receiveExecResponse(t, stream)
	require.EqualValues(t, signalExitCodeOffset+syscall.SIGTERM, response.GetExit().GetCode())
	require.NoError(t, receiveExecResult(t, result))
}

func TestExecReapsProcessWhenStartedCannotBeSent(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "pid")
	sendErr := errors.New("failed to send Started")
	var processPID int

	_, _, result := startExecTest(t, &v1.ExecRequest_Command{
		Name: execTestShell,
		Args: []string{"-c", `printf %d "$$" > "$PID_FILE"; exec sleep 30`},
		Env:  map[string]string{"PID_FILE": pidPath},
	}, func(stream *execTestStream) {
		stream.sendHook = func(response *v1.ExecResponse) error {
			if response.GetStarted() == nil {
				return nil
			}

			var err error
			processPID, err = waitForExecTestPID(pidPath)
			if err != nil {
				return err
			}

			return sendErr
		}
	})

	require.ErrorIs(t, receiveExecResult(t, result), sendErr)
	require.ErrorIs(t, syscall.Kill(processPID, 0), syscall.ESRCH)
}

func startExecTest(
	t *testing.T,
	command *v1.ExecRequest_Command,
	configure ...func(*execTestStream),
) (*RPC, *execTestStream, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream := newExecTestStream(ctx)
	for _, configureStream := range configure {
		configureStream(stream)
	}
	rpc, err := New(nil)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		result <- rpc.Exec(stream)
	}()
	stream.requests <- &v1.ExecRequest{
		Type: &v1.ExecRequest_Command_{Command: command},
	}
	return rpc, stream, result
}

func receiveExecResponse(t *testing.T, stream *execTestStream) *v1.ExecResponse {
	t.Helper()

	select {
	case response := <-stream.responses:
		return response
	case <-time.After(execTestTimeout):
		t.Fatal("timed out waiting for exec response")
		return nil
	}
}

func receiveExecResult(t *testing.T, result <-chan error) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-time.After(execTestTimeout):
		t.Fatal("timed out waiting for Exec to return")
		return nil
	}
}

func waitForExecTestPID(path string) (int, error) {
	deadline := time.Now().Add(execTestTimeout)
	for time.Now().Before(deadline) {
		//nolint:gosec // path is created under t.TempDir by the test
		data, err := os.ReadFile(path)
		if err == nil {
			return strconv.Atoi(string(data))
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}

		time.Sleep(10 * time.Millisecond)
	}

	return 0, context.DeadlineExceeded
}

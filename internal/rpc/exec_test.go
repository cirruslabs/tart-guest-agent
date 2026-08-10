// In-process stream scaffolding intentionally favors direct test construction.
//
//nolint:containedctx,testpackage,wsl_v5
package rpc

import (
	"context"
	"io"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const execTestTimeout = 5 * time.Second

type execTestStream struct {
	grpc.ServerStream

	ctx       context.Context
	requests  chan *ExecRequest
	responses chan *ExecResponse
}

var _ grpc.BidiStreamingServer[ExecRequest, ExecResponse] = (*execTestStream)(nil)

func newExecTestStream(ctx context.Context) *execTestStream {
	return &execTestStream{
		ctx:       ctx,
		requests:  make(chan *ExecRequest, 8),
		responses: make(chan *ExecResponse, 8),
	}
}

func (stream *execTestStream) Send(response *ExecResponse) error {
	select {
	case stream.responses <- response:
		return nil
	case <-stream.ctx.Done():
		return stream.ctx.Err()
	}
}

func (stream *execTestStream) Recv() (*ExecRequest, error) {
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
	stream, result := startExecTest(t, &ExecRequest_Command{
		Name: "/bin/sh",
		Args: []string{"-c", "printf hello"},
	})

	first := receiveExecResponse(t, stream)
	require.NotNil(t, first.GetStarted())

	var output []byte
	for {
		response := receiveExecResponse(t, stream)
		switch response := response.GetType().(type) {
		case *ExecResponse_StandardOutput:
			output = append(output, response.StandardOutput.GetData()...)
		case *ExecResponse_Exit_:
			require.EqualValues(t, 0, response.Exit.GetCode())
			require.Equal(t, []byte("hello"), output)
			require.NoError(t, receiveExecResult(t, result))
			return
		default:
			t.Fatalf("unexpected exec response %T", response)
		}
	}
}

func TestExecReportsStartFailureBeforeStarted(t *testing.T) {
	tests := []struct {
		name    string
		command *ExecRequest_Command
	}{
		{
			name: "missing executable",
			command: &ExecRequest_Command{
				Name: "/definitely/missing/tart-guest-agent-test-command",
			},
		},
		{
			name: "missing workdir",
			command: &ExecRequest_Command{
				Name:    "/bin/sh",
				Workdir: "/definitely/missing/tart-guest-agent-test-workdir",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream, result := startExecTest(t, test.command)
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
		signal ExecRequest_Signal
		code   int32
	}{
		{
			name:   "SIGTERM",
			signal: ExecRequest_SIGNAL_SIGTERM,
			code:   int32(signalExitCodeOffset + syscall.SIGTERM),
		},
		{
			name:   "SIGKILL",
			signal: ExecRequest_SIGNAL_SIGKILL,
			code:   int32(signalExitCodeOffset + syscall.SIGKILL),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream, result := startExecTest(t, &ExecRequest_Command{
				Name: "/bin/sleep",
				Args: []string{"30"},
			})
			require.NotNil(t, receiveExecResponse(t, stream).GetStarted())

			stream.requests <- &ExecRequest{
				Type: &ExecRequest_Signal_{Signal: test.signal},
			}

			response := receiveExecResponse(t, stream)
			require.NotNil(t, response.GetExit())
			require.Equal(t, test.code, response.GetExit().GetCode())
			require.NoError(t, receiveExecResult(t, result))
		})
	}
}

func startExecTest(
	t *testing.T,
	command *ExecRequest_Command,
) (*execTestStream, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream := newExecTestStream(ctx)
	result := make(chan error, 1)
	go func() {
		result <- (&RPC{}).Exec(stream)
	}()
	stream.requests <- &ExecRequest{
		Type: &ExecRequest_Command_{Command: command},
	}
	return stream, result
}

func receiveExecResponse(t *testing.T, stream *execTestStream) *ExecResponse {
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

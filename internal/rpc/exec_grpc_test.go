//nolint:testpackage
package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestExecGRPCBackgroundProcessLifetime(t *testing.T) {
	for _, mode := range []string{"normal exit", "cancel after exit", "cancel while running"} {
		t.Run(mode, func(t *testing.T) {
			client := NewAgentClient(newExecGRPCTestConn(t))
			ctx, cancel := context.WithTimeout(t.Context(), execTestTimeout)
			defer cancel()

			stream, err := client.Exec(ctx)
			require.NoError(t, err)

			releasePath := filepath.Join(t.TempDir(), "release")
			require.NoError(t, stream.Send(&ExecRequest{Type: &ExecRequest_Command_{Command: &ExecRequest_Command{
				Name: execTestShell,
				Args: []string{"-c", `nohup sleep 30 >/dev/null 2>&1 </dev/null &
echo "$!"
while [ ! -f "$RELEASE_FILE" ]; do sleep 0.01; done`},
				Env: map[string]string{"RELEASE_FILE": releasePath},
			}}}))

			var output strings.Builder
			for !strings.Contains(output.String(), "\n") {
				response, err := stream.Recv()
				require.NoError(t, err)
				require.Nil(t, response.GetExit())
				output.Write(response.GetStandardOutput().GetData())
			}
			childPID, err := strconv.Atoi(strings.TrimSpace(output.String()))
			require.NoError(t, err)
			require.Greater(t, childPID, 1)
			t.Cleanup(func() {
				if execTestProcessRunning(t, childPID) {
					require.NoError(t, syscall.Kill(childPID, syscall.SIGKILL))
				}
				require.Eventually(t, func() bool {
					return !execTestProcessRunning(t, childPID)
				}, execTestTimeout, 10*time.Millisecond)
			})
			require.True(t, execTestProcessRunning(t, childPID), "child must start before launcher completes")

			if mode == "cancel while running" {
				cancel()
				_, err := stream.Recv()
				require.Equal(t, codes.Canceled, status.Code(err))
				require.Eventually(t, func() bool {
					return !execTestProcessRunning(t, childPID)
				}, execTestTimeout, 10*time.Millisecond, "running command cancellation must kill its child")

				return
			}

			require.NoError(t, os.WriteFile(releasePath, nil, 0o600))
			response, err := stream.Recv()
			require.NoError(t, err)
			require.NotNil(t, response.GetExit())
			require.Zero(t, response.GetExit().GetCode())
			if mode == "cancel after exit" {
				cancel()
			} else {
				_, err = stream.Recv()
				require.ErrorIs(t, err, io.EOF)
			}

			require.Never(t, func() bool {
				return !execTestProcessRunning(t, childPID)
			}, 250*time.Millisecond, 10*time.Millisecond, "RPC shutdown must preserve background children")
		})
	}
}

func newExecGRPCTestConn(t *testing.T) *grpc.ClientConn {
	listener := bufconn.Listen(1024 * 1024)
	agent, err := New(listener)
	require.NoError(t, err)

	serveResult := make(chan error, 1)
	go func() { serveResult <- agent.grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		agent.grpcServer.Stop()
		require.NoError(t, <-serveResult)
	})

	conn, err := grpc.NewClient("passthrough:///exec-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return conn
}

func execTestProcessRunning(t *testing.T, pid int) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), execTestTimeout)
	defer cancel()

	//nolint:gosec
	output, err := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return false
		}
		require.NoError(t, err)
	}

	state := strings.TrimSpace(string(output))

	return state != "" && !strings.HasPrefix(state, "Z")
}

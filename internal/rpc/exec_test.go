package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const execTestTimeout = 15 * time.Second

type execTestResult struct {
	stdout strings.Builder
	stderr strings.Builder
	acks   []uint64
	exit   int32
}

func newExecTestClient(t *testing.T) (*grpc.ClientConn, AgentClient) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server, err := New(listener)
	if err != nil {
		t.Fatalf("create RPC server: %v", err)
	}

	serverContext, stopServer := context.WithCancel(context.Background())
	go func() {
		_ = server.Run(serverContext)
	}()

	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		stopServer()
		_ = listener.Close()
		t.Fatalf("create in-memory gRPC client: %v", err)
	}

	t.Cleanup(func() {
		stopServer()
		_ = connection.Close()
		_ = listener.Close()
	})

	return connection, NewAgentClient(connection)
}

func startExecTest(t *testing.T, client AgentClient, command *ExecRequest_Command) (
	grpc.BidiStreamingClient[ExecRequest, ExecResponse], *ExecResponse_Started,
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), execTestTimeout)
	t.Cleanup(cancel)

	stream, err := client.Exec(ctx)
	if err != nil {
		t.Fatalf("open exec stream: %v", err)
	}
	if err := stream.Send(&ExecRequest{
		Type: &ExecRequest_Command_{Command: command},
	}); err != nil {
		t.Fatalf("send exec command: %v", err)
	}

	response, err := stream.Recv()
	if err != nil {
		t.Fatalf("receive first exec response: %v", err)
	}
	started := response.GetStarted()
	if started == nil {
		t.Fatalf("first exec response = %T, want Started", response.GetType())
	}
	if started.Pid == 0 {
		t.Fatal("Started contains a zero process ID")
	}

	return stream, started
}

func addExecTestResponse(t *testing.T, result *execTestResult, response *ExecResponse) bool {
	t.Helper()

	switch event := response.GetType().(type) {
	case *ExecResponse_StandardOutput:
		_, _ = result.stdout.Write(event.StandardOutput.Data)
	case *ExecResponse_StandardError:
		_, _ = result.stderr.Write(event.StandardError.Data)
	case *ExecResponse_SignalAck_:
		result.acks = append(result.acks, event.SignalAck.RequestId)
	case *ExecResponse_Exit_:
		result.exit = event.Exit.Code
		return true
	default:
		t.Fatalf("unexpected exec response: %T", event)
	}

	return false
}

func finishExecTest(t *testing.T, stream grpc.BidiStreamingClient[ExecRequest, ExecResponse],
	result *execTestResult,
) {
	t.Helper()

	for {
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("receive exec response: %v", err)
		}
		if addExecTestResponse(t, result, response) {
			break
		}
	}

	if response, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("response after Exit = %v, %v; want EOF", response, err)
	}
}

func waitForExecOutput(t *testing.T, stream grpc.BidiStreamingClient[ExecRequest, ExecResponse],
	result *execTestResult, text string,
) {
	t.Helper()

	for !strings.Contains(result.stdout.String(), text) {
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("wait for output %q: %v", text, err)
		}
		if addExecTestResponse(t, result, response) {
			t.Fatalf("process exited before producing %q; stdout = %q", text, result.stdout.String())
		}
	}
}

func sendExecTestSignal(t *testing.T, stream grpc.BidiStreamingClient[ExecRequest, ExecResponse],
	requestID uint64, signal syscall.Signal, all bool,
) {
	t.Helper()

	if err := stream.Send(&ExecRequest{
		Type: &ExecRequest_Signal_{
			Signal: &ExecRequest_Signal{
				RequestId: requestID,
				Signal:    uint32(signal),
				All:       all,
			},
		},
	}); err != nil {
		t.Fatalf("send signal request %d: %v", requestID, err)
	}
}

func TestExecStartedContainsRealPIDAndPrecedesOutput(t *testing.T) {
	_, client := newExecTestClient(t)
	stream, started := startExecTest(t, client, &ExecRequest_Command{
		Name: "sh",
		Args: []string{"-c", `printf '%s\n' "$$"; printf 'standard-error\n' >&2`},
	})

	var result execTestResult
	finishExecTest(t, stream, &result)

	actualPID, err := strconv.ParseUint(strings.TrimSpace(result.stdout.String()), 10, 32)
	if err != nil {
		t.Fatalf("parse managed shell PID %q: %v", result.stdout.String(), err)
	}
	if started.Pid != uint32(actualPID) {
		t.Fatalf("Started PID = %d, actual guest process PID = %d", started.Pid, actualPID)
	}
	if got := result.stderr.String(); got != "standard-error\n" {
		t.Fatalf("standard error = %q, want %q", got, "standard-error\n")
	}
	if result.exit != 0 {
		t.Fatalf("exit code = %d, want 0", result.exit)
	}
}

func TestExecStartedPrecedesFastCommandOutput(t *testing.T) {
	_, client := newExecTestClient(t)

	for iteration := range 24 {
		t.Run(fmt.Sprintf("command-%02d", iteration), func(t *testing.T) {
			stream, _ := startExecTest(t, client, &ExecRequest_Command{
				Name: "sh",
				Args: []string{"-c", "printf fast"},
			})

			var result execTestResult
			finishExecTest(t, stream, &result)
			if got := result.stdout.String(); got != "fast" {
				t.Fatalf("standard output = %q, want %q", got, "fast")
			}
			if result.exit != 0 {
				t.Fatalf("exit code = %d, want 0", result.exit)
			}
		})
	}
}

func TestExecSerializesConcurrentStandardStreams(t *testing.T) {
	_, client := newExecTestClient(t)
	stream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name: "sh",
		Args: []string{"-c", `i=0; while [ "$i" -lt 128 ]; do printf 'out-%s\n' "$i"; printf 'err-%s\n' "$i" >&2; i=$((i + 1)); done`},
	})

	var result execTestResult
	finishExecTest(t, stream, &result)

	if got := strings.Count(result.stdout.String(), "\n"); got != 128 {
		t.Fatalf("standard output line count = %d, want 128", got)
	}
	if got := strings.Count(result.stderr.String(), "\n"); got != 128 {
		t.Fatalf("standard error line count = %d, want 128", got)
	}
	if result.exit != 0 {
		t.Fatalf("exit code = %d, want 0", result.exit)
	}
}

func TestExecPreservesEnvironmentAndWorkingDirectory(t *testing.T) {
	_, client := newExecTestClient(t)
	workdir := t.TempDir()
	resolvedWorkdir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		t.Fatalf("resolve test working directory: %v", err)
	}
	stream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name:    "sh",
		Args:    []string{"-c", `printf '%s:%s' "$TART_EXEC_LIFECYCLE_TEST" "$PWD"`},
		Env:     map[string]string{"TART_EXEC_LIFECYCLE_TEST": "preserved"},
		Workdir: workdir,
	})

	var result execTestResult
	finishExecTest(t, stream, &result)

	if got, want := result.stdout.String(), "preserved:"+resolvedWorkdir; got != want {
		t.Fatalf("environment and workdir = %q, want %q", got, want)
	}
	if result.exit != 0 {
		t.Fatalf("exit code = %d, want 0", result.exit)
	}
}

func TestExecInteractiveStandardInputAndEOF(t *testing.T) {
	_, client := newExecTestClient(t)
	stream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name:        "sh",
		Args:        []string{"-c", "cat"},
		Interactive: true,
	})

	for _, data := range [][]byte{[]byte("interactive input\n"), {}} {
		if err := stream.Send(&ExecRequest{
			Type: &ExecRequest_StandardInput{StandardInput: &IOChunk{Data: data}},
		}); err != nil {
			t.Fatalf("send standard input %q: %v", data, err)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("half-close client stream: %v", err)
	}

	var result execTestResult
	finishExecTest(t, stream, &result)

	if got := result.stdout.String(); got != "interactive input\n" {
		t.Fatalf("standard output = %q, want %q", got, "interactive input\n")
	}
	if result.exit != 0 {
		t.Fatalf("exit code = %d, want 0", result.exit)
	}
}

func TestExecInteractivePTYAndResize(t *testing.T) {
	_, client := newExecTestClient(t)
	stream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name:        "sh",
		Args:        []string{"-c", `stty size; IFS= read -r line; stty size; printf 'input:%s\n' "$line"`},
		Interactive: true,
		Tty:         true,
		TerminalSize: &TerminalSize{
			Rows: 24,
			Cols: 80,
		},
	})

	var result execTestResult
	waitForExecOutput(t, stream, &result, "24 80")

	if err := stream.Send(&ExecRequest{
		Type: &ExecRequest_TerminalResize{
			TerminalResize: &TerminalSize{Rows: 41, Cols: 101},
		},
	}); err != nil {
		t.Fatalf("resize pseudo-terminal: %v", err)
	}
	if err := stream.Send(&ExecRequest{
		Type: &ExecRequest_StandardInput{
			StandardInput: &IOChunk{Data: []byte("hello from a tty\n")},
		},
	}); err != nil {
		t.Fatalf("send pseudo-terminal input: %v", err)
	}

	finishExecTest(t, stream, &result)
	for _, want := range []string{"24 80", "41 101", "input:hello from a tty"} {
		if !strings.Contains(result.stdout.String(), want) {
			t.Fatalf("pseudo-terminal output %q does not contain %q", result.stdout.String(), want)
		}
	}
	if result.exit != 0 {
		t.Fatalf("exit code = %d, want 0", result.exit)
	}
}

func TestExecSignalAcknowledgesSuccessfulDelivery(t *testing.T) {
	tests := []struct {
		name   string
		signal syscall.Signal
		all    bool
		tty    bool
	}{
		{name: "process-SIGTERM", signal: syscall.SIGTERM},
		{name: "process-SIGKILL", signal: syscall.SIGKILL},
		{name: "group-SIGTERM", signal: syscall.SIGTERM, all: true},
		{name: "group-SIGKILL", signal: syscall.SIGKILL, all: true},
		{name: "pty-group-SIGTERM", signal: syscall.SIGTERM, all: true, tty: true},
		{name: "pty-group-SIGKILL", signal: syscall.SIGKILL, all: true, tty: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, client := newExecTestClient(t)
			stream, started := startExecTest(t, client, &ExecRequest_Command{
				Name: "sh",
				Args: []string{"-c", "exec sleep 30"},
				Tty:  test.tty,
			})

			groupID, err := syscall.Getpgid(int(started.Pid))
			if err != nil {
				t.Fatalf("read managed process group: %v", err)
			}
			if groupID != int(started.Pid) {
				t.Fatalf("managed process group = %d, want managed PID %d", groupID, started.Pid)
			}
			if groupID == syscall.Getpgrp() {
				t.Fatal("managed process shares the agent's process group")
			}

			const requestID = 73
			sendExecTestSignal(t, stream, requestID, test.signal, test.all)

			var result execTestResult
			finishExecTest(t, stream, &result)
			if len(result.acks) != 1 || result.acks[0] != requestID {
				t.Fatalf("signal acknowledgments = %v, want [%d]", result.acks, requestID)
			}
			if result.exit != -1 {
				t.Fatalf("signaled exit code = %d, want -1", result.exit)
			}
		})
	}
}

func TestExecSignalRequestsAreCorrelated(t *testing.T) {
	_, client := newExecTestClient(t)
	stream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name: "sh",
		Args: []string{"-c", "exec sleep 30"},
	})

	for _, request := range []struct {
		id     uint64
		signal syscall.Signal
	}{
		{id: 11, signal: syscall.SIGSTOP},
		{id: 29, signal: syscall.SIGCONT},
	} {
		sendExecTestSignal(t, stream, request.id, request.signal, false)
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("receive acknowledgment for request %d: %v", request.id, err)
		}
		if ack := response.GetSignalAck(); ack == nil || ack.RequestId != request.id {
			t.Fatalf("acknowledgment = %v, want request_id %d", response, request.id)
		}
	}

	const finalRequestID = 47
	sendExecTestSignal(t, stream, finalRequestID, syscall.SIGTERM, false)

	var result execTestResult
	finishExecTest(t, stream, &result)
	if len(result.acks) != 1 || result.acks[0] != finalRequestID {
		t.Fatalf("final acknowledgments = %v, want [%d]", result.acks, finalRequestID)
	}
	if result.exit != -1 {
		t.Fatalf("signaled exit code = %d, want -1", result.exit)
	}
}

func TestExecSignalAllIsolatesSiblingExecutions(t *testing.T) {
	_, client := newExecTestClient(t)
	marker := filepath.Join(t.TempDir(), "group-terminated")
	groupScript := `sh -c 'trap '"'"'printf terminated > "$1"; exit 0'"'"' TERM; printf "group-ready\n"; while :; do sleep 1; done' _ "$1" & wait`

	groupStream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name: "sh",
		Args: []string{"-c", groupScript, "group", marker},
	})
	var groupResult execTestResult
	waitForExecOutput(t, groupStream, &groupResult, "group-ready")

	siblingStream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name:        "sh",
		Args:        []string{"-c", "cat"},
		Interactive: true,
	})

	const groupRequestID = 101
	sendExecTestSignal(t, groupStream, groupRequestID, syscall.SIGTERM, true)
	finishExecTest(t, groupStream, &groupResult)
	if len(groupResult.acks) != 1 || groupResult.acks[0] != groupRequestID {
		t.Fatalf("group acknowledgments = %v, want [%d]", groupResult.acks, groupRequestID)
	}

	deadline := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		contents, err := os.ReadFile(marker)
		if err == nil {
			if string(contents) != "terminated" {
				t.Fatalf("group child marker = %q, want %q", contents, "terminated")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read group child marker: %v", err)
		}

		select {
		case <-deadline.C:
			t.Fatal("signal-all did not reach the managed process group's child")
		case <-ticker.C:
		}
	}

	for _, data := range [][]byte{[]byte("sibling still alive\n"), {}} {
		if err := siblingStream.Send(&ExecRequest{
			Type: &ExecRequest_StandardInput{StandardInput: &IOChunk{Data: data}},
		}); err != nil {
			t.Fatalf("write to isolated sibling execution: %v", err)
		}
	}

	var siblingResult execTestResult
	finishExecTest(t, siblingStream, &siblingResult)
	if got := siblingResult.stdout.String(); got != "sibling still alive\n" {
		t.Fatalf("sibling output = %q, want %q", got, "sibling still alive\n")
	}
	if siblingResult.exit != 0 {
		t.Fatalf("sibling exit code = %d, want 0", siblingResult.exit)
	}
}

func TestExecRejectsInvalidSignalRequestsWithoutAcknowledging(t *testing.T) {
	tests := []struct {
		name    string
		request *ExecRequest_Signal
	}{
		{
			name:    "missing-request-id",
			request: &ExecRequest_Signal{Signal: uint32(syscall.SIGTERM)},
		},
		{
			name:    "zero-signal",
			request: &ExecRequest_Signal{RequestId: 1},
		},
		{
			name:    "unsupported-signal",
			request: &ExecRequest_Signal{RequestId: 1, Signal: ^uint32(0)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, client := newExecTestClient(t)
			stream, _ := startExecTest(t, client, &ExecRequest_Command{
				Name: "sh",
				Args: []string{"-c", "exec sleep 30"},
			})

			if err := stream.Send(&ExecRequest{
				Type: &ExecRequest_Signal_{Signal: test.request},
			}); err != nil {
				t.Fatalf("send invalid signal request: %v", err)
			}

			for {
				response, err := stream.Recv()
				if err != nil {
					if got := status.Code(err); got != codes.InvalidArgument {
						t.Fatalf("invalid signal status = %v (%v), want InvalidArgument", got, err)
					}
					break
				}
				if ack := response.GetSignalAck(); ack != nil {
					t.Fatalf("invalid request unexpectedly acknowledged: %v", ack)
				}
				if exit := response.GetExit(); exit != nil {
					t.Fatalf("invalid request produced a successful exit event: %v", exit)
				}
			}
		})
	}
}

func TestExecRejectsReusedSignalRequestID(t *testing.T) {
	_, client := newExecTestClient(t)
	stream, _ := startExecTest(t, client, &ExecRequest_Command{
		Name: "sh",
		Args: []string{"-c", `trap '' USR1; printf ready; exec sleep 30`},
	})
	var result execTestResult
	waitForExecOutput(t, stream, &result, "ready")

	const requestID = 19
	sendExecTestSignal(t, stream, requestID, syscall.SIGUSR1, false)
	response, err := stream.Recv()
	if err != nil {
		t.Fatalf("receive original signal acknowledgment: %v", err)
	}
	if ack := response.GetSignalAck(); ack == nil || ack.RequestId != requestID {
		t.Fatalf("original acknowledgment = %v, want request_id %d", response, requestID)
	}

	sendExecTestSignal(t, stream, requestID, syscall.SIGUSR1, false)
	for {
		response, err = stream.Recv()
		if err != nil {
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("reused request status = %v (%v), want InvalidArgument", got, err)
			}
			break
		}
		if ack := response.GetSignalAck(); ack != nil {
			t.Fatalf("reused request unexpectedly acknowledged: %v", ack)
		}
	}
}

func TestExecDetachedRetainsLegacyExitOnlyResponse(t *testing.T) {
	_, client := newExecTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), execTestTimeout)
	defer cancel()

	stream, err := client.Exec(ctx)
	if err != nil {
		t.Fatalf("open detached exec stream: %v", err)
	}
	if err := stream.Send(&ExecRequest{
		Type: &ExecRequest_Command_{
			Command: &ExecRequest_Command{
				Name:   "sh",
				Args:   []string{"-c", "exit 0"},
				Detach: true,
			},
		},
	}); err != nil {
		t.Fatalf("send detached command: %v", err)
	}

	response, err := stream.Recv()
	if err != nil {
		t.Fatalf("receive detached exit: %v", err)
	}
	if exit := response.GetExit(); exit == nil || exit.Code != 0 {
		t.Fatalf("first detached response = %v, want legacy exit code 0", response)
	}
	if response, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("response after detached Exit = %v, %v; want EOF", response, err)
	}
}

func legacyExecDescriptors(t *testing.T) (protoreflect.MessageDescriptor, protoreflect.MessageDescriptor) {
	t.Helper()

	file := proto.Clone(protodesc.ToFileDescriptorProto(File_rpc_agent_proto)).(*descriptorpb.FileDescriptorProto)
	for _, message := range file.MessageType {
		switch message.GetName() {
		case "ExecRequest":
			fields := message.Field[:0]
			for _, field := range message.Field {
				if field.GetNumber() <= 3 {
					fields = append(fields, field)
				}
			}
			message.Field = fields

			nested := message.NestedType[:0]
			for _, child := range message.NestedType {
				if child.GetName() != "Signal" {
					nested = append(nested, child)
				}
			}
			message.NestedType = nested
		case "ExecResponse":
			fields := message.Field[:0]
			for _, field := range message.Field {
				if field.GetNumber() <= 3 {
					fields = append(fields, field)
				}
			}
			message.Field = fields

			nested := message.NestedType[:0]
			for _, child := range message.NestedType {
				if child.GetName() == "Exit" {
					nested = append(nested, child)
				}
			}
			message.NestedType = nested
		}
	}

	legacy, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build original Exec protocol descriptors: %v", err)
	}

	return legacy.Messages().ByName("ExecRequest"), legacy.Messages().ByName("ExecResponse")
}

func TestExecLegacyClientIgnoresStartedAndReceivesOriginalEvents(t *testing.T) {
	connection, _ := newExecTestClient(t)
	requestDescriptor, responseDescriptor := legacyExecDescriptors(t)
	ctx, cancel := context.WithTimeout(context.Background(), execTestTimeout)
	defer cancel()

	stream, err := connection.NewStream(ctx, &grpc.StreamDesc{
		StreamName:    "Exec",
		ServerStreams: true,
		ClientStreams: true,
	}, Agent_Exec_FullMethodName)
	if err != nil {
		t.Fatalf("open legacy exec stream: %v", err)
	}

	request := dynamicpb.NewMessage(requestDescriptor)
	commandField := requestDescriptor.Fields().ByName("command")
	command := dynamicpb.NewMessage(commandField.Message())
	command.Set(command.Descriptor().Fields().ByName("name"), protoreflect.ValueOfString("sh"))
	args := command.Mutable(command.Descriptor().Fields().ByName("args")).List()
	args.Append(protoreflect.ValueOfString("-c"))
	args.Append(protoreflect.ValueOfString("printf legacy-output; printf legacy-error >&2"))
	request.Set(commandField, protoreflect.ValueOfMessage(command))
	if err := stream.SendMsg(request); err != nil {
		t.Fatalf("send legacy-format command: %v", err)
	}

	first := dynamicpb.NewMessage(responseDescriptor)
	if err := stream.RecvMsg(first); err != nil {
		t.Fatalf("receive legacy-format Started: %v", err)
	}
	if field := first.WhichOneof(responseDescriptor.Oneofs().ByName("type")); field != nil {
		t.Fatalf("legacy client recognized new Started event as %s", field.FullName())
	}
	if len(first.GetUnknown()) == 0 {
		t.Fatal("legacy client did not retain the additive unknown Started field")
	}

	var stdout, stderr strings.Builder
	for {
		response := dynamicpb.NewMessage(responseDescriptor)
		if err := stream.RecvMsg(response); err != nil {
			t.Fatalf("receive legacy-format response: %v", err)
		}
		field := response.WhichOneof(responseDescriptor.Oneofs().ByName("type"))
		if field == nil {
			continue
		}

		event := response.Get(field).Message()
		switch field.Name() {
		case "standard_output":
			_, _ = stdout.Write(event.Get(event.Descriptor().Fields().ByName("data")).Bytes())
		case "standard_error":
			_, _ = stderr.Write(event.Get(event.Descriptor().Fields().ByName("data")).Bytes())
		case "exit":
			if code := event.Get(event.Descriptor().Fields().ByName("code")).Int(); code != 0 {
				t.Fatalf("legacy exit code = %d, want 0", code)
			}
			if got := stdout.String(); got != "legacy-output" {
				t.Fatalf("legacy standard output = %q, want %q", got, "legacy-output")
			}
			if got := stderr.String(); got != "legacy-error" {
				t.Fatalf("legacy standard error = %q, want %q", got, "legacy-error")
			}
			if err := stream.RecvMsg(dynamicpb.NewMessage(responseDescriptor)); !errors.Is(err, io.EOF) {
				t.Fatalf("legacy response after Exit = %v, want EOF", err)
			}
			return
		default:
			t.Fatalf("unexpected legacy event %s", field.FullName())
		}
	}
}

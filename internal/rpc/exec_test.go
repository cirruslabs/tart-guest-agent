package rpc_test

import (
	"context"
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

	"github.com/cirruslabs/tart-guest-agent/internal/rpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	execTestTimeout      = 15 * time.Second
	execTestSleepCommand = "exec sleep 30"
)

type execTestResult struct {
	stdout strings.Builder
	stderr strings.Builder
	acks   []uint64
	exit   int32
}

type legacyExecDescriptorSet struct {
	request  protoreflect.MessageDescriptor
	response protoreflect.MessageDescriptor
}

type legacyExecStream struct {
	stream grpc.ClientStream
}

func newExecTestConnection(t *testing.T) *grpc.ClientConn {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server, err := rpc.New(listener)
	require.NoError(t, err, "create RPC server")

	serverContext, stopServer := context.WithCancel(context.Background())

	t.Cleanup(func() {
		stopServer()

		_ = listener.Close()
	})

	go func() {
		_ = server.Run(serverContext)
	}()

	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err, "create in-memory gRPC client")

	t.Cleanup(func() {
		_ = connection.Close()
	})

	return connection
}

func newExecTestCommand(script string) *rpc.ExecRequest_Command {
	command := new(rpc.ExecRequest_Command)
	command.Name = "sh"
	command.Args = []string{"-c", script}

	return command
}

func newExecTestCommandRequest(command *rpc.ExecRequest_Command) *rpc.ExecRequest {
	request := new(rpc.ExecRequest)
	request.Type = &rpc.ExecRequest_Command_{Command: command}

	return request
}

func newExecTestInputRequest(data []byte) *rpc.ExecRequest {
	chunk := new(rpc.IOChunk)
	chunk.Data = data

	request := new(rpc.ExecRequest)
	request.Type = &rpc.ExecRequest_StandardInput{StandardInput: chunk}

	return request
}

func newExecTestSignal(requestID uint64, signal uint32, all bool) *rpc.ExecRequest_Signal {
	request := new(rpc.ExecRequest_Signal)
	request.RequestId = requestID
	request.Signal = signal
	request.All = all

	return request
}

func newExecTestSignalRequest(signal *rpc.ExecRequest_Signal) *rpc.ExecRequest {
	request := new(rpc.ExecRequest)
	request.Type = &rpc.ExecRequest_Signal_{Signal: signal}

	return request
}

func newExecTestTerminalSize(rows, cols uint32) *rpc.TerminalSize {
	size := new(rpc.TerminalSize)
	size.Rows = rows
	size.Cols = cols

	return size
}

func startExecTest(
	t *testing.T,
	connection *grpc.ClientConn,
	command *rpc.ExecRequest_Command,
) (grpc.BidiStreamingClient[rpc.ExecRequest, rpc.ExecResponse], *rpc.ExecResponse_Started) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), execTestTimeout)
	t.Cleanup(cancel)

	stream, err := rpc.NewAgentClient(connection).Exec(ctx)
	require.NoError(t, err, "open exec stream")

	err = stream.Send(newExecTestCommandRequest(command))
	require.NoError(t, err, "send exec command")

	response, err := stream.Recv()
	require.NoError(t, err, "receive first exec response")

	started := response.GetStarted()
	require.NotNil(t, started, "first exec response must be Started")
	require.NotZero(t, started.GetPid(), "Started must contain the managed process ID")

	return stream, started
}

func addExecTestResponse(t *testing.T, result *execTestResult, response *rpc.ExecResponse) bool {
	t.Helper()

	switch event := response.GetType().(type) {
	case *rpc.ExecResponse_StandardOutput:
		_, _ = result.stdout.Write(event.StandardOutput.GetData())
	case *rpc.ExecResponse_StandardError:
		_, _ = result.stderr.Write(event.StandardError.GetData())
	case *rpc.ExecResponse_SignalAck_:
		result.acks = append(result.acks, event.SignalAck.GetRequestId())
	case *rpc.ExecResponse_Exit_:
		result.exit = event.Exit.GetCode()

		return true
	default:
		t.Fatalf("unexpected exec response: %T", event)
	}

	return false
}

func finishExecTest(
	t *testing.T,
	stream grpc.BidiStreamingClient[rpc.ExecRequest, rpc.ExecResponse],
	result *execTestResult,
) {
	t.Helper()

	for {
		response, err := stream.Recv()
		require.NoError(t, err, "receive exec response")

		if addExecTestResponse(t, result, response) {
			break
		}
	}

	_, err := stream.Recv()
	require.ErrorIs(t, err, io.EOF, "the final Exit must be the last response")
}

func waitForExecOutput(
	t *testing.T,
	stream grpc.BidiStreamingClient[rpc.ExecRequest, rpc.ExecResponse],
	result *execTestResult,
	text string,
) {
	t.Helper()

	for !strings.Contains(result.stdout.String(), text) {
		response, err := stream.Recv()
		require.NoError(t, err, "wait for process output")
		require.False(t, addExecTestResponse(t, result, response),
			"process exited before producing %q; stdout = %q", text, result.stdout.String())
	}
}

func sendExecTestSignal(
	t *testing.T,
	stream grpc.BidiStreamingClient[rpc.ExecRequest, rpc.ExecResponse],
	requestID uint64,
	signal syscall.Signal,
	all bool,
) {
	t.Helper()

	request := newExecTestSignal(requestID, uint32(signal), all)
	err := stream.Send(newExecTestSignalRequest(request))
	require.NoError(t, err, "send signal request %d", requestID)
}

func TestExecStartedContainsRealPIDAndPrecedesOutput(t *testing.T) {
	connection := newExecTestConnection(t)
	command := newExecTestCommand(`printf '%s\n' "$$"; printf 'standard-error\n' >&2`)
	stream, started := startExecTest(t, connection, command)

	var result execTestResult

	finishExecTest(t, stream, &result)

	actualPID, err := strconv.ParseUint(strings.TrimSpace(result.stdout.String()), 10, 32)
	require.NoError(t, err, "parse the managed shell PID")
	require.Equal(t, uint32(actualPID), started.GetPid())
	require.Equal(t, "standard-error\n", result.stderr.String())
	require.Zero(t, result.exit)
}

func TestExecStartedPrecedesFastCommandOutput(t *testing.T) {
	connection := newExecTestConnection(t)

	for iteration := range 24 {
		t.Run(fmt.Sprintf("command-%02d", iteration), func(t *testing.T) {
			stream, _ := startExecTest(t, connection, newExecTestCommand("printf fast"))

			var result execTestResult

			finishExecTest(t, stream, &result)
			require.Equal(t, "fast", result.stdout.String())
			require.Zero(t, result.exit)
		})
	}
}

func TestExecSerializesConcurrentStandardStreams(t *testing.T) {
	connection := newExecTestConnection(t)
	command := newExecTestCommand(
		`i=0; while [ "$i" -lt 128 ]; do ` +
			`printf 'out-%s\n' "$i"; printf 'err-%s\n' "$i" >&2; i=$((i + 1)); done`,
	)
	stream, _ := startExecTest(t, connection, command)

	var result execTestResult

	finishExecTest(t, stream, &result)
	assertStreamLineCount(t, result.stdout.String())
	assertStreamLineCount(t, result.stderr.String())
	require.Zero(t, result.exit)
}

func assertStreamLineCount(t *testing.T, output string) {
	t.Helper()

	require.Equal(t, 128, strings.Count(output, "\n"))
}

func TestExecPreservesEnvironmentAndWorkingDirectory(t *testing.T) {
	connection := newExecTestConnection(t)
	workdir := t.TempDir()
	resolvedWorkdir, err := filepath.EvalSymlinks(workdir)
	require.NoError(t, err, "resolve test working directory")

	command := newExecTestCommand(`printf '%s:%s' "$TART_EXEC_LIFECYCLE_TEST" "$PWD"`)
	command.Env = map[string]string{"TART_EXEC_LIFECYCLE_TEST": "preserved"}
	command.Workdir = workdir

	stream, _ := startExecTest(t, connection, command)

	var result execTestResult

	finishExecTest(t, stream, &result)
	require.Equal(t, "preserved:"+resolvedWorkdir, result.stdout.String())
	require.Zero(t, result.exit)
}

func TestExecInteractiveStandardInputAndEOF(t *testing.T) {
	connection := newExecTestConnection(t)
	command := newExecTestCommand("cat")
	command.Interactive = true

	stream, _ := startExecTest(t, connection, command)

	for _, data := range [][]byte{[]byte("interactive input\n"), {}} {
		err := stream.Send(newExecTestInputRequest(data))
		require.NoError(t, err, "send standard input %q", data)
	}

	err := stream.CloseSend()
	require.NoError(t, err, "half-close client stream")

	var result execTestResult

	finishExecTest(t, stream, &result)
	require.Equal(t, "interactive input\n", result.stdout.String())
	require.Zero(t, result.exit)
}

func TestExecInteractivePTYAndResize(t *testing.T) {
	connection := newExecTestConnection(t)
	command := newExecTestCommand(
		`stty size; IFS= read -r line; stty size; printf 'input:%s\n' "$line"`,
	)
	command.Interactive = true
	command.Tty = true
	command.TerminalSize = newExecTestTerminalSize(24, 80)

	stream, _ := startExecTest(t, connection, command)

	var result execTestResult

	waitForExecOutput(t, stream, &result, "24 80")

	resize := new(rpc.ExecRequest)
	resize.Type = &rpc.ExecRequest_TerminalResize{
		TerminalResize: newExecTestTerminalSize(41, 101),
	}

	err := stream.Send(resize)
	require.NoError(t, err, "resize pseudo-terminal")

	err = stream.Send(newExecTestInputRequest([]byte("hello from a tty\n")))
	require.NoError(t, err, "send pseudo-terminal input")

	finishExecTest(t, stream, &result)

	for _, want := range []string{"24 80", "41 101", "input:hello from a tty"} {
		require.Contains(t, result.stdout.String(), want)
	}

	require.Zero(t, result.exit)
}

func TestExecSignalAcknowledgesSuccessfulDelivery(t *testing.T) {
	tests := []struct {
		name   string
		signal syscall.Signal
		all    bool
		tty    bool
	}{
		{name: "process-SIGTERM", signal: syscall.SIGTERM, all: false, tty: false},
		{name: "process-SIGKILL", signal: syscall.SIGKILL, all: false, tty: false},
		{name: "group-SIGTERM", signal: syscall.SIGTERM, all: true, tty: false},
		{name: "group-SIGKILL", signal: syscall.SIGKILL, all: true, tty: false},
		{name: "pty-group-SIGTERM", signal: syscall.SIGTERM, all: true, tty: true},
		{name: "pty-group-SIGKILL", signal: syscall.SIGKILL, all: true, tty: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := newExecTestConnection(t)
			command := newExecTestCommand(execTestSleepCommand)
			command.Tty = test.tty

			stream, started := startExecTest(t, connection, command)
			groupID, err := syscall.Getpgid(int(started.GetPid()))
			require.NoError(t, err, "read managed process group")
			require.Equal(t, int(started.GetPid()), groupID)
			require.NotEqual(t, syscall.Getpgrp(), groupID)

			const requestID uint64 = 73

			sendExecTestSignal(t, stream, requestID, test.signal, test.all)

			var result execTestResult

			finishExecTest(t, stream, &result)
			require.Equal(t, []uint64{requestID}, result.acks)
			require.EqualValues(t, -1, result.exit)
		})
	}
}

func TestExecSignalRequestsAreCorrelated(t *testing.T) {
	connection := newExecTestConnection(t)
	stream, _ := startExecTest(t, connection, newExecTestCommand(execTestSleepCommand))

	for _, request := range []struct {
		id     uint64
		signal syscall.Signal
	}{
		{id: 11, signal: syscall.SIGSTOP},
		{id: 29, signal: syscall.SIGCONT},
	} {
		sendExecTestSignal(t, stream, request.id, request.signal, false)

		response, err := stream.Recv()
		require.NoError(t, err, "receive acknowledgment for request %d", request.id)

		ack := response.GetSignalAck()
		require.NotNil(t, ack)
		require.Equal(t, request.id, ack.GetRequestId())
	}

	const finalRequestID uint64 = 47

	sendExecTestSignal(t, stream, finalRequestID, syscall.SIGTERM, false)

	var result execTestResult

	finishExecTest(t, stream, &result)
	require.Equal(t, []uint64{finalRequestID}, result.acks)
	require.EqualValues(t, -1, result.exit)
}

func waitForGroupTermination(t *testing.T, root *os.Root) {
	t.Helper()

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		contents, err := root.ReadFile("group-terminated")
		if err == nil {
			require.Equal(t, "terminated", string(contents))

			return
		}

		require.ErrorIs(t, err, os.ErrNotExist, "read group child marker")

		select {
		case <-deadline.C:
			t.Fatal("signal-all did not reach the managed process group's child")
		case <-ticker.C:
		}
	}
}

func TestExecSignalAllIsolatesSiblingExecutions(t *testing.T) {
	connection := newExecTestConnection(t)
	markerDir := t.TempDir()
	markerRoot, err := os.OpenRoot(markerDir)
	require.NoError(t, err, "open confined group-marker directory")

	t.Cleanup(func() {
		_ = markerRoot.Close()
	})

	marker := filepath.Join(markerDir, "group-terminated")
	groupScript := `sh -c 'trap '"'"'printf terminated > "$1"; exit 0'"'"' TERM; ` +
		`printf "group-ready\n"; while :; do sleep 1; done' _ "$1" & wait`
	groupCommand := newExecTestCommand(groupScript)
	groupCommand.Args = append(groupCommand.Args, "group", marker)

	groupStream, _ := startExecTest(t, connection, groupCommand)

	var groupResult execTestResult

	waitForExecOutput(t, groupStream, &groupResult, "group-ready")

	siblingCommand := newExecTestCommand("cat")
	siblingCommand.Interactive = true

	siblingStream, _ := startExecTest(t, connection, siblingCommand)

	const groupRequestID uint64 = 101

	sendExecTestSignal(t, groupStream, groupRequestID, syscall.SIGTERM, true)
	finishExecTest(t, groupStream, &groupResult)
	require.Equal(t, []uint64{groupRequestID}, groupResult.acks)

	waitForGroupTermination(t, markerRoot)

	for _, data := range [][]byte{[]byte("sibling still alive\n"), {}} {
		err = siblingStream.Send(newExecTestInputRequest(data))
		require.NoError(t, err, "write to isolated sibling execution")
	}

	var siblingResult execTestResult

	finishExecTest(t, siblingStream, &siblingResult)
	require.Equal(t, "sibling still alive\n", siblingResult.stdout.String())
	require.Zero(t, siblingResult.exit)
}

func TestExecRejectsInvalidSignalRequestsWithoutAcknowledging(t *testing.T) {
	tests := []struct {
		name    string
		request *rpc.ExecRequest_Signal
	}{
		{
			name:    "missing-request-id",
			request: newExecTestSignal(0, uint32(syscall.SIGTERM), false),
		},
		{
			name:    "zero-signal",
			request: newExecTestSignal(1, 0, false),
		},
		{
			name:    "unsupported-signal",
			request: newExecTestSignal(1, ^uint32(0), false),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := newExecTestConnection(t)
			stream, _ := startExecTest(t, connection, newExecTestCommand(execTestSleepCommand))

			err := stream.Send(newExecTestSignalRequest(test.request))
			require.NoError(t, err, "send invalid signal request")

			assertInvalidExecSignal(t, stream)
		})
	}
}

func assertInvalidExecSignal(
	t *testing.T,
	stream grpc.BidiStreamingClient[rpc.ExecRequest, rpc.ExecResponse],
) {
	t.Helper()

	for {
		response, err := stream.Recv()
		if err != nil {
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			return
		}

		require.Nil(t, response.GetSignalAck(), "invalid request must not be acknowledged")
		require.Nil(t, response.GetExit(), "invalid request must not produce a successful exit")
	}
}

func TestExecRejectsReusedSignalRequestID(t *testing.T) {
	connection := newExecTestConnection(t)
	command := newExecTestCommand(`trap '' USR1; printf ready; exec sleep 30`)
	stream, _ := startExecTest(t, connection, command)

	var result execTestResult

	waitForExecOutput(t, stream, &result, "ready")

	const requestID uint64 = 19

	sendExecTestSignal(t, stream, requestID, syscall.SIGUSR1, false)

	response, err := stream.Recv()
	require.NoError(t, err, "receive original signal acknowledgment")

	ack := response.GetSignalAck()
	require.NotNil(t, ack)
	require.Equal(t, requestID, ack.GetRequestId())

	sendExecTestSignal(t, stream, requestID, syscall.SIGUSR1, false)
	assertInvalidExecSignal(t, stream)
}

func TestExecDetachedRetainsLegacyExitOnlyResponse(t *testing.T) {
	connection := newExecTestConnection(t)

	ctx, cancel := context.WithTimeout(context.Background(), execTestTimeout)
	defer cancel()

	stream, err := rpc.NewAgentClient(connection).Exec(ctx)
	require.NoError(t, err, "open detached exec stream")

	command := newExecTestCommand("exit 0")
	command.Detach = true

	err = stream.Send(newExecTestCommandRequest(command))
	require.NoError(t, err, "send detached command")

	response, err := stream.Recv()
	require.NoError(t, err, "receive detached exit")

	exit := response.GetExit()
	require.NotNil(t, exit, "the first detached response must remain Exit")
	require.Zero(t, exit.GetCode())

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func retainLegacyFields(message *descriptorpb.DescriptorProto) {
	fields := message.GetField()[:0]

	for _, field := range message.GetField() {
		if field.GetNumber() <= 3 {
			fields = append(fields, field)
		}
	}

	message.Field = fields
}

func retainLegacyNestedMessages(message *descriptorpb.DescriptorProto) {
	nested := message.GetNestedType()[:0]

	for _, child := range message.GetNestedType() {
		if child.GetName() != "Signal" && child.GetName() != "Started" &&
			child.GetName() != "SignalAck" {
			nested = append(nested, child)
		}
	}

	message.NestedType = nested
}

func legacyExecDescriptors(t *testing.T) *legacyExecDescriptorSet {
	t.Helper()

	file := protodesc.ToFileDescriptorProto(rpc.File_rpc_agent_proto)

	for _, message := range file.GetMessageType() {
		if message.GetName() == "ExecRequest" || message.GetName() == "ExecResponse" {
			retainLegacyFields(message)
			retainLegacyNestedMessages(message)
		}
	}

	legacy, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err, "build original Exec protocol descriptors")

	descriptors := new(legacyExecDescriptorSet)
	descriptors.request = legacy.Messages().ByName("ExecRequest")
	descriptors.response = legacy.Messages().ByName("ExecResponse")

	return descriptors
}

func newLegacyExecStream(
	t *testing.T,
	connection *grpc.ClientConn,
	requestDescriptor protoreflect.MessageDescriptor,
) *legacyExecStream {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), execTestTimeout)
	t.Cleanup(cancel)

	description := new(grpc.StreamDesc)
	description.StreamName = "Exec"
	description.ServerStreams = true
	description.ClientStreams = true

	stream, err := connection.NewStream(ctx, description, rpc.Agent_Exec_FullMethodName)
	require.NoError(t, err, "open legacy exec stream")

	request := dynamicpb.NewMessage(requestDescriptor)
	commandField := requestDescriptor.Fields().ByName("command")
	command := dynamicpb.NewMessage(commandField.Message())
	command.Set(command.Descriptor().Fields().ByName("name"), protoreflect.ValueOfString("sh"))

	args := command.Mutable(command.Descriptor().Fields().ByName("args")).List()
	args.Append(protoreflect.ValueOfString("-c"))
	args.Append(protoreflect.ValueOfString("printf legacy-output; printf legacy-error >&2"))
	request.Set(commandField, protoreflect.ValueOfMessage(command))

	err = stream.SendMsg(request)
	require.NoError(t, err, "send legacy-format command")

	legacyStream := new(legacyExecStream)
	legacyStream.stream = stream

	return legacyStream
}

func assertLegacyStarted(
	t *testing.T,
	stream *legacyExecStream,
	descriptor protoreflect.MessageDescriptor,
) {
	t.Helper()

	first := dynamicpb.NewMessage(descriptor)
	err := stream.stream.RecvMsg(first)
	require.NoError(t, err, "receive legacy-format Started")
	require.Nil(t, first.WhichOneof(descriptor.Oneofs().ByName("type")),
		"legacy client must ignore the additive Started event")
	require.NotEmpty(t, first.GetUnknown(),
		"legacy client must retain the unknown Started field")
}

func readLegacyExecEvents(
	t *testing.T,
	stream *legacyExecStream,
	descriptor protoreflect.MessageDescriptor,
) {
	t.Helper()

	var stdout, stderr strings.Builder

	for {
		response := dynamicpb.NewMessage(descriptor)
		err := stream.stream.RecvMsg(response)
		require.NoError(t, err, "receive legacy-format response")

		field := response.WhichOneof(descriptor.Oneofs().ByName("type"))
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
			require.Zero(t, event.Get(event.Descriptor().Fields().ByName("code")).Int())
			require.Equal(t, "legacy-output", stdout.String())
			require.Equal(t, "legacy-error", stderr.String())

			err = stream.stream.RecvMsg(dynamicpb.NewMessage(descriptor))
			require.ErrorIs(t, err, io.EOF)

			return
		default:
			t.Fatalf("unexpected legacy event %s", field.FullName())
		}
	}
}

func TestExecLegacyClientIgnoresStartedAndReceivesOriginalEvents(t *testing.T) {
	connection := newExecTestConnection(t)
	descriptors := legacyExecDescriptors(t)
	stream := newLegacyExecStream(t, connection, descriptors.request)

	assertLegacyStarted(t, stream, descriptors.response)
	readLegacyExecEvents(t, stream, descriptors.response)
}

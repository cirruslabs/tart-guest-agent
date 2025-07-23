package rpc

import (
	"context"
	"google.golang.org/grpc"
	"net"
)

type RPC struct {
	grpcServer *grpc.Server
	listener   net.Listener

	UnimplementedAgentServer
}

func New(listener net.Listener) (*RPC, error) {
	rpc := &RPC{
		grpcServer: grpc.NewServer(),
		listener:   listener,
	}

	RegisterAgentServer(rpc.grpcServer, rpc)

	return rpc, nil
}

func (rpc *RPC) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()

		rpc.grpcServer.Stop()
	}()

	return rpc.grpcServer.Serve(rpc.listener)
}

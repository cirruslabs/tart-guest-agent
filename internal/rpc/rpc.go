package rpc

import (
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

func (rpc *RPC) Run() error {
	return rpc.grpcServer.Serve(rpc.listener)
}

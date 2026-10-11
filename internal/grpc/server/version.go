package server

import (
	"context"

	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/version"
)

func (n *nipaServer) GetServerVersion(context.Context, *pb.GetServerVersionRequest) (*pb.GetServerVersionResponse, error) {
	info := version.Resolve()
	return &pb.GetServerVersionResponse{
		Version:   info.Version,
		Revision:  info.Revision,
		GoVersion: info.GoVersion,
	}, nil
}

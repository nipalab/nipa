package grpc

import (
	"context"

	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/version"
)

// GetServerVersion probes the public version RPC of the connected server.
func (c *Client) GetServerVersion(ctx context.Context) (*version.Info, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetServerVersion(ctx, &pb.GetServerVersionRequest{})
	if err != nil {
		return nil, toDomainError(err)
	}
	return &version.Info{
		Version:   res.GetVersion(),
		Revision:  res.GetRevision(),
		GoVersion: res.GetGoVersion(),
	}, nil
}

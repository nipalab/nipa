package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/version"
)

type versionClient interface {
	Connect(ctx context.Context, host string) error
	GetServerVersion(ctx context.Context) (*version.Info, error)
}

// Version probes the build version of a nipa server.
type Version struct {
	client versionClient
}

func NewVersion(client versionClient) *Version {
	return &Version{client: client}
}

// Server returns the build info reported by the server at url, which must be a
// nipa repository URL (e.g. https://nipa.example.com/org/project).
func (v *Version) Server(ctx context.Context, url string) (*version.Info, error) {
	nipaUrl, err := domain.ParseNipaUrl(url)
	if err != nil {
		return nil, err
	}
	if err := v.client.Connect(ctx, nipaUrl.Origin); err != nil {
		return nil, err
	}
	return v.client.GetServerVersion(ctx)
}

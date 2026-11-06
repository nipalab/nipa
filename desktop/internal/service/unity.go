package service

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type MetaWarning struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"` // missing-meta, orphan-meta, meta-not-staged, guid-changed
	Related string `json:"related"`
	Message string `json:"message"`
}

// MetaWarnings checks the pending changes of Unity projects inside the clone
// for .meta sidecar mistakes.
func (s *Service) MetaWarnings(ctx context.Context, root string) ([]MetaWarning, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	resp, err := client.MetaWarnings(ctx, &daemonpb.MetaWarningsRequest{Root: root})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	out := make([]MetaWarning, 0, len(resp.GetWarnings()))
	for _, w := range resp.GetWarnings() {
		out = append(out, MetaWarning{Path: w.GetPath(), Kind: w.GetKind(), Related: w.GetRelated(), Message: w.GetMessage()})
	}
	return out, nil
}

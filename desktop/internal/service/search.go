package service

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type FileHit struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Binary bool   `json:"binary"`
	Kind   string `json:"kind"`
}

// FileSearch is a ranked list of tracked files; Truncated means the clone
// had more candidates than the daemon ranks, so a better match may be missing.
type FileSearch struct {
	Hits      []FileHit `json:"hits"`
	Truncated bool      `json:"truncated"`
}

// SearchFiles finds tracked files of a clone by a fuzzy path query.
func (s *Service) SearchFiles(ctx context.Context, root, query string, limit int) (*FileSearch, error) {
	var res *daemonpb.SearchFilesResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.SearchFiles(ctx, &daemonpb.SearchFilesRequest{Root: root, Query: query, Limit: int32(max(0, min(limit, 200)))})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := &FileSearch{Hits: make([]FileHit, 0, len(res.GetHits())), Truncated: res.GetTruncated()}
	for _, h := range res.GetHits() {
		out.Hits = append(out.Hits, FileHit{Path: h.GetPath(), Name: h.GetName(), Size: h.GetSizeBytes(), Binary: h.GetIsBinary(), Kind: h.GetKind()})
	}
	return out, nil
}

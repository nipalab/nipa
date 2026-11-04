package service

import (
	"context"
	"os"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/unitymeta"
)

const maxGUIDChecks = 256

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
	resp, err := client.Status(ctx, &daemonpb.StatusRequest{Root: root})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	c := toChanges(resp)
	var changes []unitymeta.Change
	unity := false
	for i, list := range [][]FileChange{c.Staged, c.Unstaged} {
		for _, f := range list {
			changes = append(changes, unitymeta.Change{Path: f.Path, Status: f.Status, Staged: i == 0})
			unity = unity || strings.HasPrefix(f.Path, "Assets/") || strings.Contains(f.Path, "/Assets/")
		}
	}
	out := []MetaWarning{}
	if !unity {
		return out, nil
	}
	checks := 0
	oldMeta := func(path string) ([]byte, error) {
		if checks++; checks > maxGUIDChecks {
			return nil, errBlobTooLarge
		}
		_, data, err := s.readBlob(ctx, client, root, &daemonpb.BlobRef{Source: "snapshot", Path: path}, 1<<20)
		return data, err
	}
	for _, w := range unitymeta.Lint(os.DirFS(root), changes, oldMeta) {
		out = append(out, MetaWarning{Path: w.Path, Kind: string(w.Kind), Related: w.Related, Message: w.Message})
	}
	return out, nil
}

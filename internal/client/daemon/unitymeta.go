package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/unitymeta"
)

const (
	maxGUIDChecks = 256
	maxMetaBytes  = 1 << 20
)

var (
	errMetaBudget   = errors.New("too many .meta files to compare")
	errMetaTooLarge = errors.New(".meta file too large")
)

// MetaWarnings checks the pending changes of Unity projects inside the clone
// for .meta sidecar mistakes.
func (s *Server) MetaWarnings(ctx context.Context, req *daemonpb.MetaWarningsRequest) (*daemonpb.MetaWarningsResponse, error) {
	rp, done, err := s.acquireDiff(ctx, req.GetRoot())
	if err != nil {
		return nil, toStatusError(err)
	}
	defer done()
	st, err := rp.wc.Status(ctx)
	if err != nil {
		return nil, toStatusError(err)
	}
	res := &daemonpb.MetaWarningsResponse{Warnings: []*daemonpb.MetaWarning{}}
	changes, unity := metaChanges(statusToPB(st))
	if !unity {
		return res, nil
	}
	checks := 0
	oldMeta := func(path string) ([]byte, error) {
		if checks++; checks > maxGUIDChecks {
			return nil, errMetaBudget
		}
		return readSnapshotSmall(ctx, rp.diff, rp.root, path)
	}
	for _, w := range unitymeta.Lint(os.DirFS(rp.root), changes, oldMeta) {
		res.Warnings = append(res.Warnings, &daemonpb.MetaWarning{Path: w.Path, Kind: string(w.Kind), Related: w.Related, Message: w.Message})
	}
	return res, nil
}

func metaChanges(st *daemonpb.StatusResponse) ([]unitymeta.Change, bool) {
	added := map[string]bool{}
	for _, p := range st.GetAdded() {
		added[p] = true
	}
	var changes []unitymeta.Change
	unity := false
	add := func(paths []string, staged bool, statusOf func(string) string) {
		for _, p := range paths {
			changes = append(changes, unitymeta.Change{Path: p, Status: statusOf(p), Staged: staged})
			unity = unity || strings.HasPrefix(p, "Assets/") || strings.Contains(p, "/Assets/")
		}
	}
	constant := func(v string) func(string) string { return func(string) string { return v } }
	add(st.GetStaged(), true, func(p string) string {
		if added[p] {
			return "added"
		}
		return "modified"
	})
	add(st.GetDeleted(), true, constant("deleted"))
	add(st.GetModified(), false, constant("modified"))
	add(st.GetMissing(), false, constant("deleted"))
	add(st.GetUntracked(), false, constant("untracked"))
	return changes, unity
}

func readSnapshotSmall(ctx context.Context, runner DiffRunner, root, path string) ([]byte, error) {
	var data []byte
	err := runner.ReadBlob(ctx, root, usecase.BlobRequest{Source: usecase.BlobSource("snapshot"), Path: path}, func(e diff.Entry, r io.Reader) error {
		if e.SizeBytes > maxMetaBytes {
			return errMetaTooLarge
		}
		var err error
		data, err = io.ReadAll(io.LimitReader(r, maxMetaBytes+1))
		if err == nil && len(data) > maxMetaBytes {
			return errMetaTooLarge
		}
		return err
	})
	return data, err
}

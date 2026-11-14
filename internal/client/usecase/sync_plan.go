package usecase

import (
	"context"
	"sort"
	"strings"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

// SyncTarget is what an update or switch would check out: a tag, a branch,
// or with both empty the clone's own branch (or pinned tag commit).
type SyncTarget struct {
	Branch string
	Tag    string
}

// TargetTree fetches the manifest a sync to target would materialize,
// without touching the working copy.
func (u *Update) TargetTree(ctx context.Context, root string, target SyncTarget) (*domain.TreeNode, error) {
	if err := u.localRepo.Init(root); err != nil {
		return nil, err
	}
	unlock, err := lockShared(u.localRepo)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cfg, err := u.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, err
	}
	if err := u.client.Connect(ctx, nu.Origin); err != nil {
		return nil, err
	}
	if err := u.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return nil, err
	}
	tag := strings.TrimSpace(target.Tag)
	branch := strings.TrimSpace(target.Branch)
	switch {
	case tag != "":
		t, err := u.client.GetTagByName(ctx, nu.Org, nu.Project, tag)
		if err != nil {
			return nil, err
		}
		return u.client.GetCommitTreeManifest(ctx, nu.Org, nu.Project, t.CommitID, cfg.Sparse)
	case branch != "":
		return u.client.GetTreeNodeManifest(ctx, nu.Org, nu.Project, branch, cfg.Sparse)
	case cfg.Head != nil:
		commit, err := u.localRepo.LoadCommit()
		if err != nil {
			return nil, err
		}
		if commit == nil || commit.CommitID == "" {
			return nil, clientDomain.NewUserError("no commit checked out; run nipa switch to a branch first")
		}
		return u.client.GetCommitTreeManifest(ctx, nu.Org, nu.Project, commit.CommitID, cfg.Sparse)
	default:
		return u.client.GetTreeNodeManifest(ctx, nu.Org, nu.Project, cfg.Branch, cfg.Sparse)
	}
}

// OverwrittenPaths lists local changes a sync to tree would replace: edited,
// deleted or new files whose path the target writes with a version other
// than the synced one. When switching, staged paths are listed too, since a
// switch refuses to run with them.
func OverwrittenPaths(snapshot *clientDomain.Snapshot, tree *domain.TreeNode, st *clientDomain.Status, switching bool) []string {
	base := make(map[string]clientDomain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		base[f.Path] = f
	}
	target := map[string]materializedFile{}
	for _, f := range flattenTree(tree, "") {
		target[f.Path] = f
	}
	hits := map[string]bool{}
	check := func(p string) {
		t, ok := target[p]
		if !ok {
			return
		}
		b, tracked := base[p]
		if !tracked || b.Hash != t.FileHash || b.Mode != t.Mode {
			hits[p] = true
		}
	}
	for _, list := range [][]string{st.Modified, st.Missing, st.Untracked, st.Staged, st.Deleted} {
		for _, p := range list {
			check(p)
		}
	}
	if switching {
		for _, p := range st.Staged {
			hits[p] = true
		}
		for _, p := range st.Deleted {
			hits[p] = true
		}
	}
	out := make([]string, 0, len(hits))
	for p := range hits {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

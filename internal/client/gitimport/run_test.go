package gitimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/gitimport"
)

type recordingProgress struct {
	started int
	updates []int
	ended   bool
}

func (p *recordingProgress) ImportStart(n int)             { p.started = n }
func (p *recordingProgress) ImportProgress(n int, _ int64) { p.updates = append(p.updates, n) }
func (p *recordingProgress) ImportEnd()                    { p.ended = true }
func baseOptions(dir string) Options {
	return Options{GitPath: dir, Host: "nipa.test", Org: "acme", Project: "game"}
}

func statuses(sum *Summary) map[string]string {
	out := map[string]string{}
	for _, r := range sum.Refs {
		out[r.Kind+":"+r.Name] = r.Status
	}
	return out
}

func TestRun_ImportsHistoryRefsAndAuthors(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	authors, err := ParseAuthorMap(strings.NewReader("Bob <BOB@example.com> = 2a\n"))
	require.NoError(t, err)
	opts := baseOptions(repo.dir)
	opts.AuthorMap = authors
	var confirmed *Summary
	opts.Confirm = func(s *Summary) error { confirmed = s; return nil }
	progress := &recordingProgress{}

	sum, err := NewImporter(srv).Run(context.Background(), opts, progress)
	require.NoError(t, err)
	require.NotNil(t, confirmed)
	require.Equal(t, 4, sum.Commits)
	require.Equal(t, 4, sum.Imported)
	require.Equal(t, 0, sum.Known)
	require.Equal(t, "main", sum.DefaultBranch)
	require.Equal(t, 4, progress.started)
	require.Equal(t, []int{1, 2, 3, 4}, progress.updates)
	require.True(t, progress.ended)
	require.Positive(t, sum.UploadedBytes)
	require.Equal(t, map[string]string{"branch:feature": RefCreated, "branch:main": RefCreated, "tag:v1": RefCreated, "default:main": RefUpdated}, statuses(sum))
	require.Empty(t, sum.RefWarnings())
	require.Empty(t, sum.RefErrors())
	require.Equal(t, "first release", srv.setRefs[0].Tags[0].Message)
	require.Equal(t, 4, srv.setRefs[0].CommitCount)

	root := srv.commits[gitimport.Origin("default", repo.root)].commit
	require.Equal(t, "Ann", root.AuthorName)
	require.Equal(t, "ann@example.com", root.AuthorEmail)
	require.Empty(t, root.AuthorUserID)
	require.Equal(t, time.Date(2020, 3, 4, 10, 59, 0, 0, time.UTC), root.AuthoredAt.UTC())
	require.Len(t, root.Files, 2)
	merge := srv.commits[gitimport.Origin("default", repo.merge)].commit
	require.Equal(t, "2a", merge.AuthorUserID)
	require.Equal(t, []string{gitimport.Origin("default", repo.child), gitimport.Origin("default", repo.feature)}, merge.ParentOrigins)
	for h, n := range srv.offered {
		require.Equal(t, 1, n, "chunk %s offered once", h)
	}

	_, err = os.Stat(filepath.Join(repo.dir, ".git", StateDirName, StateFileName))
	require.NoError(t, err)
}

func TestRun_ResumeAfterFailureDoesNotRechunkOrReupload(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	srv.failAt = 2

	sum, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.ErrorContains(t, err, "injected failure")
	require.Equal(t, 2, sum.Imported)
	require.Empty(t, srv.setRefs)
	firstRead := sum.BlobsRead

	srv.failAt = -1
	sum, err = NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)
	require.Equal(t, 2, sum.Known)
	require.Equal(t, 2, sum.Commits)
	require.Equal(t, 2, sum.Imported)
	require.Len(t, srv.commits, 4)
	require.Positive(t, sum.BlobHits)
	require.Equal(t, int64(4), firstRead+sum.BlobsRead, "every distinct blob is chunked once across both runs")
	for h, n := range srv.offered {
		require.Equal(t, 1, n, "chunk %s offered once across both runs", h)
	}
}

func TestRun_UpdateImportsNewCommitAndFastForwards(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	_, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)

	next := repo.commit("Cid", map[string]string{"README.md": "hello again\n", "art/logo.png": "\x89PNG\x00\x01\x02", "docs/a.md": "feature\n", "new.txt": "new\n"}, "next\n", repo.merge)
	repo.branch("main", next)

	opts := baseOptions(repo.dir)
	opts.Update = true
	sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Equal(t, 1, sum.Commits)
	require.Equal(t, 1, sum.Imported)
	require.Equal(t, 4, sum.Known)
	require.Equal(t, int64(1), sum.BlobsRead)
	require.Equal(t, RefUpdated, statuses(sum)["branch:main"])
	require.Equal(t, RefUnchanged, statuses(sum)["branch:feature"])
	require.Equal(t, "", srv.setRefs[1].DefaultBranch, "update keeps the default branch unless asked")
	added := srv.commits[gitimport.Origin("default", next)].commit
	require.Len(t, added.Files, 1)
	require.Equal(t, "new.txt", added.Files[0].Path)
}

func TestRun_DivergedAndProtectedRefsAreWarnings(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	_, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)

	srv.mu.Lock()
	srv.nextID++
	srv.commits["git:default:"+strings.Repeat("e", 40)] = &fakeCommit{commit: nil, id: srv.nextID}
	srv.branches["feature"] = "git:default:" + strings.Repeat("e", 40)
	srv.protected["main"] = true
	srv.mu.Unlock()
	next := repo.commit("Cid", map[string]string{"README.md": "x\n"}, "next\n", repo.merge)
	repo.branch("main", next)
	feat := repo.commit("Cid", map[string]string{"README.md": "y\n"}, "feat\n", repo.feature)
	repo.branch("feature", feat)

	opts := baseOptions(repo.dir)
	opts.Update = true
	sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Equal(t, RefProtected, statuses(sum)["branch:main"])
	require.Equal(t, RefDiverged, statuses(sum)["branch:feature"])
	warnings := sum.RefWarnings()
	require.Len(t, warnings, 2)
	require.Empty(t, sum.RefErrors())
}

func TestRun_StateTargetMismatch(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	_, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)

	other := baseOptions(repo.dir)
	other.Project = "other"
	_, err = NewImporter(newFakeServer()).Run(context.Background(), other, nil)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Error(), "acme/game")
	require.Contains(t, de.Action, "--reset-state")

	other.ResetState = true
	srv2 := newFakeServer()
	sum, err := NewImporter(srv2).Run(context.Background(), other, nil)
	require.NoError(t, err)
	require.Equal(t, int64(4), sum.BlobsRead, "a reset state chunks everything again")
}

func TestRun_DryRunWritesNothing(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	opts := baseOptions(repo.dir)
	opts.DryRun = true
	opts.Confirm = func(*Summary) error { t.Fatal("dry run never confirms"); return nil }
	sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.True(t, sum.DryRun)
	require.Equal(t, 4, sum.Report.Commits)
	require.Equal(t, 4, sum.Report.Blobs)
	require.Zero(t, srv.streams)
	require.Empty(t, srv.offered)
	require.Empty(t, srv.setRefs)
	_, err = os.Stat(filepath.Join(repo.dir, ".git", StateDirName))
	require.True(t, os.IsNotExist(err))
}

func TestRun_ConfirmDeclinedWritesNothing(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	opts := baseOptions(repo.dir)
	opts.Confirm = func(*Summary) error { return errors.New("cancelled") }
	_, err := NewImporter(srv).Run(context.Background(), opts, nil)
	require.EqualError(t, err, "cancelled")
	require.Zero(t, srv.streams)
	require.Empty(t, srv.offered)
}

func TestRun_InvalidPathsStopBeforeWriting(t *testing.T) {
	f := newFixture(t)
	tree := f.store(&object.Tree{Entries: []object.TreeEntry{{Name: `a\b.txt`, Mode: filemode.Regular, Hash: f.blob("x")}}})
	c := f.store(&object.Commit{
		Author:    object.Signature{Name: "A", Email: "a@example.com", When: f.clock},
		Committer: object.Signature{Name: "A", Email: "a@example.com", When: f.clock},
		Message:   "bad", TreeHash: tree,
	})
	f.branch("main", c)
	srv := newFakeServer()
	_, err := NewImporter(srv).Run(context.Background(), baseOptions(f.dir), nil)
	require.ErrorContains(t, err, `a\b.txt`)
	require.Zero(t, srv.streams)

	opts := baseOptions(f.dir)
	opts.DryRun = true
	sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Len(t, sum.Report.InvalidPaths, 1)
}

func TestRun_AllKnownOnlySetsRefs(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	_, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)
	sum, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)
	require.Zero(t, sum.Commits)
	require.Equal(t, 1, srv.streams)
	require.Equal(t, RefUnchanged, statuses(sum)["branch:main"])
}

func TestRun_OptionAndSourceErrors(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	ctx := context.Background()

	opts := baseOptions(repo.dir)
	opts.SourceKey = "a:b"
	_, err := NewImporter(srv).Run(ctx, opts, nil)
	require.ErrorContains(t, err, "invalid source key")

	opts = baseOptions(repo.dir)
	opts.Symlinks = "follow"
	_, err = NewImporter(srv).Run(ctx, opts, nil)
	require.ErrorContains(t, err, "invalid symlink policy")

	_, err = NewImporter(srv).Run(ctx, baseOptions(t.TempDir()), nil)
	require.ErrorContains(t, err, "open git repository")

	opts = baseOptions(repo.dir)
	opts.Refs = []string{"refs/heads/nothing-*"}
	_, err = NewImporter(srv).Run(ctx, opts, nil)
	require.ErrorContains(t, err, "no git refs match")

	opts = baseOptions(repo.dir)
	opts.DefaultBranch = "missing"
	_, err = NewImporter(srv).Run(ctx, opts, nil)
	require.ErrorContains(t, err, "default branch")

	opts = baseOptions(repo.dir)
	opts.Depth = -1
	_, err = NewImporter(srv).Run(ctx, opts, nil)
	require.ErrorContains(t, err, "depth")

	srv.lookupErr = errors.New("lookup down")
	_, err = NewImporter(srv).Run(ctx, baseOptions(repo.dir), nil)
	require.ErrorContains(t, err, "lookup down")
	srv.lookupErr = nil

	srv.openErr = errors.New("stream refused")
	_, err = NewImporter(srv).Run(ctx, baseOptions(repo.dir), nil)
	require.ErrorContains(t, err, "stream refused")
	srv.openErr = nil

	srv.uploadErr = errors.New("upload refused")
	_, err = NewImporter(srv).Run(ctx, baseOptions(repo.dir), nil)
	require.ErrorContains(t, err, "upload refused")
	srv.uploadErr = nil

	srv.setRefsErr = errors.New("refs refused")
	_, err = NewImporter(srv).Run(ctx, baseOptions(repo.dir), nil)
	require.ErrorContains(t, err, "refs refused")
}

func TestRun_CancelledContext(t *testing.T) {
	repo := newSampleRepo(t)
	srv := newFakeServer()
	ctx, cancel := context.WithCancel(context.Background())
	opts := baseOptions(repo.dir)
	opts.Confirm = func(*Summary) error { cancel(); return nil }
	_, err := NewImporter(srv).Run(ctx, opts, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, srv.commits)

	sum, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)
	require.Equal(t, 4, sum.Imported)
}

func TestWithStateHint(t *testing.T) {
	require.NoError(t, withStateHint(nil))
	plain := errors.New("other")
	require.Equal(t, plain, withStateHint(plain))
	require.Equal(t, "missing chunk data for x", withStateHint(errors.New("missing chunk data for x")).Error())
	de := domain.NewUserError("missing chunk data for x")
	var got *domain.Error
	require.ErrorAs(t, withStateHint(de), &got)
	require.Contains(t, got.Action, "--reset-state")
}

func TestSummaryRefClassification(t *testing.T) {
	sum := &Summary{Refs: []RefResult{{Status: RefCreated}, {Status: RefExists}, {Status: RefInvalid}, {Status: RefDiverged}}}
	require.Len(t, sum.RefWarnings(), 2)
	require.Len(t, sum.RefErrors(), 1)
	require.Equal(t, "abcdefg", short("abcdefgh"))
	require.Equal(t, "abc", short("abc"))
	_ = plumbing.ZeroHash
}

func TestRun_LookupRefusalStopsBeforeUpload(t *testing.T) {
	for _, dry := range []bool{false, true} {
		repo := newSampleRepo(t)
		srv := newFakeServer()
		srv.lookupErr = &domain.Error{Code: 403, Message: "permission denied"}
		opts := baseOptions(repo.dir)
		opts.DryRun = dry
		opts.Confirm = func(*Summary) error { t.Fatal("refused before confirming"); return nil }
		_, err := NewImporter(srv).Run(context.Background(), opts, nil)
		require.ErrorContains(t, err, "permission denied")
		require.Zero(t, srv.uploads)
		require.Zero(t, srv.streams)
		require.Empty(t, srv.setRefs)
	}
}

func TestRun_QuotaEstimate(t *testing.T) {
	t.Run("no quota", func(t *testing.T) {
		repo := newSampleRepo(t)
		srv := newFakeServer()
		sum, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
		require.NoError(t, err)
		require.False(t, sum.QuotaChecked)
		require.Equal(t, []int64{sum.Report.Bytes}, srv.estimates)
		require.Equal(t, sum.Report.Bytes, sum.UploadEstimate)
		require.Positive(t, sum.Imported)
	})
	t.Run("fits", func(t *testing.T) {
		repo := newSampleRepo(t)
		srv := newFakeServer()
		srv.hasQuota, srv.quota = true, 1<<30
		sum, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
		require.NoError(t, err)
		require.True(t, sum.QuotaChecked)
		require.False(t, sum.QuotaBlocked())
		require.Positive(t, sum.Imported)
	})
	t.Run("exceeded stops before upload", func(t *testing.T) {
		repo := newSampleRepo(t)
		srv := newFakeServer()
		srv.hasQuota, srv.quota = true, 1
		opts := baseOptions(repo.dir)
		opts.Confirm = func(*Summary) error { t.Fatal("refused before confirming"); return nil }
		sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
		var de *domain.Error
		require.ErrorAs(t, err, &de)
		require.Equal(t, 402, de.Code)
		require.Equal(t, "nothing was imported: storage quota exceeded", de.Message)
		require.Contains(t, de.Hint, "before deduplication")
		require.Contains(t, de.Action, "--since")
		require.True(t, sum.QuotaChecked)
		require.Zero(t, srv.uploads)
		require.Zero(t, srv.streams)
		_, err = os.Stat(filepath.Join(repo.dir, ".git", StateDirName))
		require.True(t, os.IsNotExist(err))
	})
	t.Run("exceeded in a dry run", func(t *testing.T) {
		repo := newSampleRepo(t)
		srv := newFakeServer()
		srv.hasQuota, srv.quota = true, 1
		opts := baseOptions(repo.dir)
		opts.DryRun = true
		sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
		require.NoError(t, err)
		require.True(t, sum.QuotaBlocked())
		require.Equal(t, "storage quota exceeded", sum.QuotaExceeded)
	})
	t.Run("other errors pass through", func(t *testing.T) {
		repo := newSampleRepo(t)
		srv := newFakeServer()
		srv.estimateErr = errors.New("unavailable")
		_, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
		require.EqualError(t, err, "unavailable")
		require.Zero(t, srv.uploads)
	})
	t.Run("nothing to upload", func(t *testing.T) {
		repo := newSampleRepo(t)
		srv := newFakeServer()
		_, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
		require.NoError(t, err)
		srv.estimates = nil
		sum, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
		require.NoError(t, err)
		require.Zero(t, sum.UploadEstimate)
		require.Empty(t, srv.estimates)
	})
}

package aicontext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubDiffer struct {
	files     []diff.FileDiff
	err       error
	base      string
	baseErr   error
	revs      []string
	opts      usecase.DiffOptions
	baseCalls int
}

func (s *stubDiffer) Run(_ context.Context, _ string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error) {
	s.revs, s.opts = revs, opts
	return s.files, s.err
}

func (s *stubDiffer) MergeBaseCommit(context.Context, string, string, string) (string, error) {
	s.baseCalls++
	return s.base, s.baseErr
}

type stubHistory struct {
	branches []*serverDomain.Branch
	listErr  error
	log      []*serverDomain.CommitLogEntry
	logErr   error
	branch   string
}

func (s *stubHistory) ListBranches(context.Context, string, string, string) ([]*serverDomain.Branch, error) {
	return s.branches, s.listErr
}

func (s *stubHistory) Log(_ context.Context, _, _, _, branch string, _ ...usecase.CommitLogOption) ([]*serverDomain.CommitLogEntry, error) {
	s.branch = branch
	return s.log, s.logErr
}

type stubConnector struct {
	err   error
	hosts []string
}

func (s *stubConnector) Connect(_ context.Context, host string) error {
	s.hosts = append(s.hosts, host)
	return s.err
}

type stubMR struct {
	mr         *domain.MergeRequest
	m          *domain.Mergeability
	files      []*domain.MergeRequestDiffFile
	commits    []*serverDomain.CommitLogEntry
	threads    []*domain.MergeRequestThread
	viewErr    error
	diffErr    error
	commitsErr error
	threadsErr error
}

func (s *stubMR) View(context.Context, string, string) (*domain.MergeRequest, *domain.Mergeability, error) {
	return s.mr, s.m, s.viewErr
}

func (s *stubMR) Diff(context.Context, string, string) ([]*domain.MergeRequestDiffFile, error) {
	return s.files, s.diffErr
}

func (s *stubMR) Commits(context.Context, string, string) ([]*serverDomain.CommitLogEntry, error) {
	return s.commits, s.commitsErr
}

func (s *stubMR) Threads(context.Context, string, string) ([]*domain.MergeRequestThread, error) {
	return s.threads, s.threadsErr
}

func memLoader(files map[string]string) assetdiff.Loader {
	return func(_ context.Context, ref assetdiff.Ref, _ int64) ([]byte, error) {
		data, ok := files[ref.Source+":"+ref.Revision+":"+ref.Path]
		if !ok {
			return nil, assetdiff.ErrMissing
		}
		return []byte(data), nil
	}
}

func cfg(branch string) func(string) (*domain.Config, error) {
	return func(string) (*domain.Config, error) {
		return &domain.Config{Url: "http://example.com/org/project", Branch: branch}, nil
	}
}

func textChange(p string, status diff.Status, oldText, newText string) diff.FileDiff {
	return diff.FileDiff{
		Change: diff.Change{Path: p, Status: status, Old: diff.Entry{Path: p, Mode: 2, Hash: [32]byte{1}}, New: diff.Entry{Path: p, Mode: 2, Hash: [32]byte{2}, SizeBytes: int64(len(newText))}},
		Old:    []byte(oldText), New: []byte(newText),
	}
}

const budget = "version: 1\nrules:\n  - name: naming\n    paths: [\"*.txt\"]\n    name_pattern: \"^[a-z]+\\\\.txt$\"\n"

func TestCommit(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nipaassets.yml"), []byte(budget), 0o644))
	d := &stubDiffer{files: []diff.FileDiff{
		textChange("a.txt", diff.Modified, "one\n", "one\ntwo\n"),
		textChange("Bad_1.txt", diff.Added, "", "x\n"),
	}}
	c := &Collector{Diff: d, Config: cfg("main"), Load: memLoader(map[string]string{
		"snapshot::a.txt": "one\n", "working::a.txt": "one\ntwo\n", "working::Bad_1.txt": "x\n",
	})}
	in, err := c.Commit(context.Background(), root)
	require.NoError(t, err)
	require.True(t, d.opts.Staged)
	require.Empty(t, d.revs)
	require.Equal(t, "main", in.Branch)
	require.Len(t, in.Diff, 2)
	require.Len(t, in.Summaries, 2)
	require.Len(t, in.Violations, 1)
	require.Equal(t, "Bad_1.txt", in.Violations[0].Path)
	p := aiprompt.CommitMessage(*in)
	require.Contains(t, p.User, "+two")
}

func TestCommit_Errors(t *testing.T) {
	ctx := context.Background()
	_, err := (&Collector{}).Commit(ctx, t.TempDir())
	require.ErrorContains(t, err, "diff is not configured")

	cfgErr := errors.New("no config")
	c := &Collector{Diff: &stubDiffer{}, Config: func(string) (*domain.Config, error) { return nil, cfgErr }}
	_, err = c.Commit(ctx, t.TempDir())
	require.ErrorIs(t, err, cfgErr)

	c = &Collector{Diff: &stubDiffer{err: errors.New("diff failed")}, Config: cfg("main")}
	_, err = c.Commit(ctx, t.TempDir())
	require.ErrorContains(t, err, "diff failed")

	c = &Collector{Diff: &stubDiffer{}, Config: cfg("main")}
	_, err = c.Commit(ctx, t.TempDir())
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, "nipa add <path>", domErr.Action)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nipaassets.yml"), []byte("rules: ["), 0o644))
	c = &Collector{Diff: &stubDiffer{files: []diff.FileDiff{textChange("a.txt", diff.Modified, "a", "b")}}, Config: cfg("main"), Load: memLoader(nil)}
	_, err = c.Commit(ctx, root)
	require.Error(t, err, "a broken budget file must be reported")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = c.Commit(cancelled, t.TempDir())
	require.ErrorIs(t, err, context.Canceled)
}

func TestCommit_DefaultConfigFromLocalRepo(t *testing.T) {
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "dev"}))
	require.NoError(t, lr.Close())
	c := &Collector{Diff: &stubDiffer{files: []diff.FileDiff{textChange("a.txt", diff.Modified, "a\n", "b\n")}}}
	in, err := c.Commit(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, "dev", in.Branch)
	require.Empty(t, in.Summaries, "no loader means no summaries")
}

func entry(id int64, msg string) *serverDomain.CommitLogEntry {
	return &serverDomain.CommitLogEntry{Commit: serverDomain.Commit{ID: snow.ID(id), Message: msg}, AuthorName: "alice"}
}

func TestMRDescription(t *testing.T) {
	base := snow.ID(5).Base36()
	d := &stubDiffer{base: base, files: []diff.FileDiff{textChange("a.txt", diff.Modified, "a\n", "b\n")}}
	h := &stubHistory{
		branches: []*serverDomain.Branch{nil, {Name: "dev"}, {Name: "main", IsDefault: true}},
		log:      []*serverDomain.CommitLogEntry{entry(9, "second\n\nbody"), nil, entry(7, "first"), entry(5, "base"), entry(3, "older")},
	}
	conn := &stubConnector{}
	c := &Collector{Diff: d, History: h, Connector: conn, Config: cfg("feature"), Load: memLoader(map[string]string{
		"revision:" + base + ":a.txt": "a\n", "revision:feature:a.txt": "b\n",
	})}
	in, err := c.MRDescription(context.Background(), t.TempDir(), "", "")
	require.NoError(t, err)
	require.Equal(t, "feature", in.Source)
	require.Equal(t, "main", in.Target)
	require.Equal(t, []string{"main", "feature"}, d.revs)
	require.True(t, d.opts.MergeBase)
	require.Equal(t, "feature", h.branch)
	require.Equal(t, []aiprompt.Commit{{ID: snow.ID(9).Base36(), Subject: "second", Author: "alice"}, {ID: snow.ID(7).Base36(), Subject: "first", Author: "alice"}}, in.Commits)
	require.Len(t, in.Summaries, 1)
	require.Empty(t, in.Summaries[0].Error)
	require.Equal(t, []string{"http://example.com", "http://example.com"}, conn.hosts)

	in, err = c.MRDescription(context.Background(), t.TempDir(), "topic", "dev")
	require.NoError(t, err)
	require.Equal(t, "topic", in.Source)
	require.Equal(t, "dev", in.Target)

	noHistory := &Collector{Diff: d, Config: cfg("feature")}
	in, err = noHistory.MRDescription(context.Background(), t.TempDir(), "", "main")
	require.NoError(t, err)
	require.Empty(t, in.Commits)
}

func TestMRDescription_Errors(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_, err := (&Collector{}).MRDescription(ctx, root, "", "")
	require.ErrorContains(t, err, "diff is not configured")

	cfgErr := errors.New("cfg")
	_, err = (&Collector{Diff: &stubDiffer{}, Config: func(string) (*domain.Config, error) { return nil, cfgErr }}).MRDescription(ctx, root, "", "")
	require.ErrorIs(t, err, cfgErr)

	badURL := func(string) (*domain.Config, error) { return &domain.Config{Url: "::", Branch: "f"}, nil }
	_, err = (&Collector{Diff: &stubDiffer{}, Config: badURL}).MRDescription(ctx, root, "", "")
	require.Error(t, err)

	_, err = (&Collector{Diff: &stubDiffer{}, Config: cfg("f")}).MRDescription(ctx, root, "", "")
	require.ErrorContains(t, err, "pass a target")

	_, err = (&Collector{Diff: &stubDiffer{}, Config: cfg("f"), History: &stubHistory{}}).MRDescription(ctx, root, "", "")
	require.ErrorContains(t, err, "no default branch")

	_, err = (&Collector{Diff: &stubDiffer{}, Config: cfg("f"), History: &stubHistory{listErr: errors.New("list")}}).MRDescription(ctx, root, "", "")
	require.ErrorContains(t, err, "list")

	_, err = (&Collector{Diff: &stubDiffer{}, Config: cfg("f"), History: &stubHistory{}, Connector: &stubConnector{err: errors.New("dial")}}).MRDescription(ctx, root, "", "")
	require.ErrorContains(t, err, "dial")

	_, err = (&Collector{Diff: &stubDiffer{}, Config: cfg("main")}).MRDescription(ctx, root, "", "main")
	require.ErrorContains(t, err, "must differ")

	_, err = (&Collector{Diff: &stubDiffer{err: errors.New("run")}, Config: cfg("f")}).MRDescription(ctx, root, "", "main")
	require.ErrorContains(t, err, "run")

	_, err = (&Collector{Diff: &stubDiffer{baseErr: errors.New("base")}, Config: cfg("f")}).MRDescription(ctx, root, "", "main")
	require.ErrorContains(t, err, "base")

	_, err = (&Collector{Diff: &stubDiffer{}, Config: cfg("f"), History: &stubHistory{logErr: errors.New("log")}}).MRDescription(ctx, root, "", "main")
	require.ErrorContains(t, err, "log")

	_, err = (&Collector{Diff: &stubDiffer{}, Config: cfg("f"), History: &stubHistory{}, Connector: &stubConnector{err: errors.New("dial2")}}).MRDescription(ctx, root, "", "main")
	require.ErrorContains(t, err, "dial2")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = (&Collector{Diff: &stubDiffer{files: []diff.FileDiff{textChange("a.txt", diff.Modified, "a", "b")}}, Config: cfg("f"), Load: memLoader(nil)}).MRDescription(cancelled, root, "", "main")
	require.ErrorIs(t, err, context.Canceled)
}

func TestReview(t *testing.T) {
	line := int64(2)
	mr := &stubMR{
		mr: &domain.MergeRequest{Number: 3, Title: "T", Description: "D", SourceBranch: "feature", TargetBranch: "main", Status: "open", Draft: true},
		m:  &domain.Mergeability{SourceCommitID: "b", MergeBaseCommitID: "a", BlockedBy: "draft"},
		files: []*domain.MergeRequestDiffFile{
			{Path: "a.txt", Status: "M", Patch: []string{"@@ -1 +1 @@", "-a", "+b"}},
			nil,
			{Path: "c.txt", Status: "A", Patch: []string{"@@ -0,0 +1 @@", "+c"}},
		},
		commits: []*serverDomain.CommitLogEntry{entry(2, "change a"), nil},
		threads: []*domain.MergeRequestThread{
			{FilePath: "a.txt", NewLine: &line, Comments: []*domain.MergeRequestComment{{User: domain.ReviewActor{Name: "bob"}, Body: "why?"}, nil}},
			nil,
		},
	}
	c := &Collector{Diff: &stubDiffer{}, MR: mr, Load: memLoader(map[string]string{
		"revision:a:a.txt": "a\n", "revision:b:a.txt": "b\n", "revision:b:c.txt": "c\n",
	})}
	in, err := c.Review(context.Background(), t.TempDir(), 3)
	require.NoError(t, err)
	require.Equal(t, "T", in.Title)
	require.Equal(t, "draft", in.BlockedBy)
	require.True(t, in.Draft)
	require.Len(t, in.Diff, 2)
	require.Len(t, in.Summaries, 2)
	require.Len(t, in.Commits, 1)
	require.Len(t, in.Threads, 1)
	require.Equal(t, []aiprompt.Comment{{Author: "bob", Body: "why?"}}, in.Threads[0].Comments)
	for _, s := range in.Summaries {
		require.Empty(t, s.Error, s.Path)
	}

	mr.mr, mr.m = nil, nil
	in, err = c.Review(context.Background(), t.TempDir(), 3)
	require.NoError(t, err)
	require.Equal(t, int64(3), in.Number)
}

func TestReview_Errors(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_, err := (&Collector{}).Review(ctx, root, 1)
	require.ErrorContains(t, err, "merge requests are not configured")
	_, err = (&Collector{MR: &stubMR{}}).Review(ctx, root, 1)
	require.ErrorContains(t, err, "diff is not configured")
	_, err = (&Collector{MR: &stubMR{}, Diff: &stubDiffer{}}).Review(ctx, root, 0)
	require.ErrorContains(t, err, "number is required")
	for _, mr := range []*stubMR{
		{viewErr: errors.New("view")},
		{diffErr: errors.New("diffx")},
		{commitsErr: errors.New("commits")},
		{threadsErr: errors.New("threads")},
	} {
		_, err = (&Collector{MR: mr, Diff: &stubDiffer{}}).Review(ctx, root, 1)
		require.Error(t, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = (&Collector{MR: &stubMR{files: []*domain.MergeRequestDiffFile{{Path: "a", Status: "M"}}}, Diff: &stubDiffer{}, Load: memLoader(nil)}).Review(cancelled, root, 1)
	require.ErrorIs(t, err, context.Canceled)
}

func TestNew(t *testing.T) {
	d := usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())
	c := New(d, t.TempDir())
	require.NotNil(t, c.Load)
	require.Equal(t, d, c.Diff)
}

func TestMRSides(t *testing.T) {
	base, head := MRSides(nil, nil)
	require.Equal(t, Side{Kind: "target_branch"}, base)
	require.Equal(t, Side{Kind: "source_branch"}, head)
	base, head = MRSides(&domain.MergeRequest{MergeBaseCommitID: "x", Review: &domain.MergeRequestReviewState{HeadCommitID: "h"}}, nil)
	require.Equal(t, Side{Revision: "x", Kind: "merge_base"}, base)
	require.Equal(t, Side{Revision: "h", Kind: "source_head"}, head)
	base, _ = MRSides(nil, &domain.Mergeability{TargetCommitID: "t"})
	require.Equal(t, Side{Revision: "t", Kind: "target_head"}, base)
}

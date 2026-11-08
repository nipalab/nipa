// Package aicontext gathers the repository context the aiprompt builders
// describe: the staged changes of a working copy, the changes a merge request
// would bring, or an open merge request with its threads. It is shared by the
// MCP prompts and the CLI --generate flags.
package aicontext

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/assetscan"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const (
	DefaultMaxFiles   = 50
	DefaultMaxCommits = 50
)

type Differ interface {
	Run(ctx context.Context, root string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error)
	MergeBaseCommit(ctx context.Context, root, revA, revB string) (string, error)
}

type MRReader interface {
	View(ctx context.Context, root, id string) (*domain.MergeRequest, *domain.Mergeability, error)
	Diff(ctx context.Context, root, id string) ([]*domain.MergeRequestDiffFile, error)
	Commits(ctx context.Context, root, id string) ([]*serverDomain.CommitLogEntry, error)
	Threads(ctx context.Context, root, id string) ([]*domain.MergeRequestThread, error)
}

type History interface {
	ListBranches(ctx context.Context, host, org, project string) ([]*serverDomain.Branch, error)
	Log(ctx context.Context, host, org, project, branch string, opts ...usecase.CommitLogOption) ([]*serverDomain.CommitLogEntry, error)
}

type Connector interface {
	Connect(ctx context.Context, host string) error
}

// Collector needs Diff and Load for every prompt; MR for reviews and
// History plus Connector for merge request descriptions.
type Collector struct {
	Diff       Differ
	Load       assetdiff.Loader
	MR         MRReader
	History    History
	Connector  Connector
	Config     func(root string) (*domain.Config, error)
	MaxFiles   int
	MaxCommits int
	DiffBytes  int
}

// New builds a collector over the clone's diff usecase.
func New(d *usecase.Diff, root string) *Collector {
	return &Collector{Diff: d, Load: assetscan.BlobLoader(d, root)}
}

func (c *Collector) config(root string) (*domain.Config, error) {
	if c.Config != nil {
		return c.Config(root)
	}
	return localrepo.NewLocalRepoWithTarget(root).LoadConfig()
}

// Commit describes the changes staged for the next push. It runs offline.
func (c *Collector) Commit(ctx context.Context, root string) (*aiprompt.CommitInput, error) {
	if c.Diff == nil {
		return nil, domain.NewUserError("diff is not configured")
	}
	cfg, err := c.config(root)
	if err != nil {
		return nil, err
	}
	files, err := c.Diff.Run(ctx, root, nil, usecase.DiffOptions{Staged: true})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, domain.NewUserError("nothing staged to describe").WithHint("stage the files of the commit first", "nipa add <path>")
	}
	cs, err := c.changeset(ctx, root, files, assetscan.SideSnapshot, assetscan.SideWorking)
	if err != nil {
		return nil, err
	}
	return &aiprompt.CommitInput{Branch: cfg.Branch, Changeset: cs}, nil
}

// MRDescription describes what a merge request from source into target
// would bring: the merge base vs source diff and the source commits since
// the merge base. Empty source is the working copy branch, empty target the
// project default branch.
func (c *Collector) MRDescription(ctx context.Context, root, source, target string) (*aiprompt.MRInput, error) {
	if c.Diff == nil {
		return nil, domain.NewUserError("diff is not configured")
	}
	cfg, err := c.config(root)
	if err != nil {
		return nil, err
	}
	nu, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, err
	}
	source, target = strings.TrimSpace(source), strings.TrimSpace(target)
	if source == "" {
		source = cfg.Branch
	}
	if target == "" {
		if target, err = c.defaultBranch(ctx, nu); err != nil {
			return nil, err
		}
	}
	if source == target {
		return nil, domain.NewUserError(fmt.Sprintf("source and target branches must differ (both are %q); pass a target", source))
	}
	files, err := c.Diff.Run(ctx, root, []string{target, source}, usecase.DiffOptions{MergeBase: true})
	if err != nil {
		return nil, err
	}
	base, err := c.Diff.MergeBaseCommit(ctx, root, target, source)
	if err != nil {
		return nil, err
	}
	commits, err := c.commitsSince(ctx, nu, source, base)
	if err != nil {
		return nil, err
	}
	cs, err := c.changeset(ctx, root, files, base, source)
	if err != nil {
		return nil, err
	}
	return &aiprompt.MRInput{Source: source, Target: target, Commits: commits, Changeset: cs}, nil
}

func (c *Collector) defaultBranch(ctx context.Context, nu *domain.NipaUrl) (string, error) {
	if c.History == nil {
		return "", domain.NewUserError("no target branch given and branches cannot be listed; pass a target")
	}
	if err := c.connect(ctx, nu); err != nil {
		return "", err
	}
	branches, err := c.History.ListBranches(ctx, nu.Host, nu.Org, nu.Project)
	if err != nil {
		return "", err
	}
	for _, b := range branches {
		if b != nil && b.IsDefault {
			return b.Name, nil
		}
	}
	return "", domain.NewUserError("the project has no default branch; pass a target")
}

func (c *Collector) connect(ctx context.Context, nu *domain.NipaUrl) error {
	if c.Connector == nil {
		return nil
	}
	return c.Connector.Connect(ctx, nu.Origin)
}

func (c *Collector) commitsSince(ctx context.Context, nu *domain.NipaUrl, branch, stop string) ([]aiprompt.Commit, error) {
	if c.History == nil {
		return nil, nil
	}
	if err := c.connect(ctx, nu); err != nil {
		return nil, err
	}
	limit := c.MaxCommits
	if limit <= 0 {
		limit = DefaultMaxCommits
	}
	entries, err := c.History.Log(ctx, nu.Host, nu.Org, nu.Project, branch, usecase.WithCommitLogMax(limit))
	if err != nil {
		return nil, err
	}
	var out []aiprompt.Commit
	for _, e := range entries {
		if e == nil {
			continue
		}
		if stop != "" && e.ID.Base36() == stop {
			break
		}
		out = append(out, commitOf(e))
	}
	return out, nil
}

// Review gathers merge request number: metadata, commits, threads and the
// merge base vs source head changes.
func (c *Collector) Review(ctx context.Context, root string, number int64) (*aiprompt.ReviewInput, error) {
	if c.MR == nil {
		return nil, domain.NewUserError("merge requests are not configured")
	}
	if c.Diff == nil {
		return nil, domain.NewUserError("diff is not configured")
	}
	if number <= 0 {
		return nil, domain.NewUserError("a merge request number is required")
	}
	id := strconv.FormatInt(number, 10)
	mr, m, err := c.MR.View(ctx, root, id)
	if err != nil {
		return nil, err
	}
	if mr == nil {
		mr = &domain.MergeRequest{Number: number}
	}
	files, err := c.MR.Diff(ctx, root, id)
	if err != nil {
		return nil, err
	}
	entries, err := c.MR.Commits(ctx, root, id)
	if err != nil {
		return nil, err
	}
	threads, err := c.MR.Threads(ctx, root, id)
	if err != nil {
		return nil, err
	}
	base, head := MRSides(mr, m)
	var items []assetdiff.Item
	for _, f := range files {
		if f != nil {
			items = append(items, assetscan.ChangeItem(f.Path, f.OldPath, f.Status, base.Revision, head.Revision))
		}
	}
	changes := &assetscan.Changes{Old: base.Revision, New: head.Revision}
	for _, it := range items {
		changes.Files = append(changes.Files, assetscan.Change{Item: it})
	}
	sizes := func(ctx context.Context, paths []string) (map[string]int64, error) {
		return c.newSizes(ctx, root, base.Revision, head.Revision, paths)
	}
	cs, err := c.summarize(ctx, root, aiprompt.FromMRDiff(files), items, changes, sizes)
	if err != nil {
		return nil, err
	}
	in := &aiprompt.ReviewInput{
		Number: number, Title: mr.Title, Description: mr.Description, Source: mr.SourceBranch, Target: mr.TargetBranch,
		Status: mr.Status, Draft: mr.Draft, Changeset: cs,
	}
	if m != nil {
		in.BlockedBy = m.BlockedBy
	}
	for _, e := range entries {
		if e != nil {
			in.Commits = append(in.Commits, commitOf(e))
		}
	}
	for _, t := range threads {
		if t != nil {
			in.Threads = append(in.Threads, threadOf(t))
		}
	}
	return in, nil
}

func (c *Collector) changeset(ctx context.Context, root string, files []diff.FileDiff, oldSide, newSide string) (aiprompt.Changeset, error) {
	changes := &assetscan.Changes{Old: oldSide, New: newSide}
	items := make([]assetdiff.Item, 0, len(files))
	for _, f := range files {
		ch := f.Change
		it := assetscan.ChangeItem(ch.Path, ch.Old.Path, ch.Status.String(), oldSide, newSide)
		items = append(items, it)
		changes.Files = append(changes.Files, assetscan.Change{Item: it, NewSize: ch.New.SizeBytes})
	}
	return c.summarize(ctx, root, aiprompt.FromFileDiffs(files), items, changes, nil)
}

type sizer func(ctx context.Context, paths []string) (map[string]int64, error)

// newSizes reads the new-side sizes of paths from the base..head manifests;
// binary content is not downloaded.
func (c *Collector) newSizes(ctx context.Context, root, base, head string, paths []string) (map[string]int64, error) {
	if base == "" || head == "" {
		return nil, nil
	}
	files, err := c.Diff.Run(ctx, root, []string{base, head}, usecase.DiffOptions{Paths: paths})
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(files))
	for _, f := range files {
		out[f.Change.Path] = f.Change.New.SizeBytes
	}
	return out, nil
}

// fillSizes sets the new-side size of covered files that have none, so
// max_bytes rules see it.
func fillSizes(ctx context.Context, budget *assetbudget.Config, changes *assetscan.Changes, sizes sizer) error {
	var paths []string
	for _, ch := range changes.Files {
		if ch.Item.New != nil && ch.NewSize == 0 && budget.Covers(ch.Item.Path) {
			paths = append(paths, ch.Item.Path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	got, err := sizes(ctx, paths)
	if err != nil {
		return err
	}
	for i := range changes.Files {
		if ch := &changes.Files[i]; ch.NewSize == 0 {
			ch.NewSize = got[ch.Item.Path]
		}
	}
	return nil
}

func (c *Collector) summarize(ctx context.Context, root string, text []aiprompt.FileText, items []assetdiff.Item, changes *assetscan.Changes, sizes sizer) (aiprompt.Changeset, error) {
	cs := aiprompt.Changeset{Diff: text, DiffBytes: c.DiffBytes}
	if c.Load == nil || len(items) == 0 {
		return cs, nil
	}
	maxFiles := c.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	sums, err := assetdiff.Summarize(ctx, c.Load, items, assetdiff.Options{MaxFiles: maxFiles, GUIDs: assetdiff.WorkingGUIDs(root)})
	if err != nil {
		return cs, err
	}
	cs.Summaries, cs.OmittedSummaries = sums.Files, sums.Omitted
	budget, err := assetbudget.Load(root)
	if err != nil || budget == nil {
		return cs, err
	}
	if sizes != nil {
		if err := fillSizes(ctx, budget, changes, sizes); err != nil {
			return cs, err
		}
	}
	report, err := assetscan.CheckChanges(ctx, c.Load, budget, changes)
	if err != nil {
		return cs, err
	}
	cs.Violations = report.Violations
	return cs, nil
}

func commitOf(e *serverDomain.CommitLogEntry) aiprompt.Commit {
	return aiprompt.Commit{ID: e.ID.Base36(), Subject: aiprompt.Subject(e.Message), Author: e.AuthorName}
}

func threadOf(t *domain.MergeRequestThread) aiprompt.Thread {
	out := aiprompt.Thread{Path: t.FilePath, OldLine: t.OldLine, NewLine: t.NewLine, Resolved: t.Resolved, Outdated: t.Outdated}
	for _, c := range t.Comments {
		if c != nil {
			out.Comments = append(out.Comments, aiprompt.Comment{Author: c.User.Name, Body: c.Body})
		}
	}
	return out
}

// Side is one compared revision of a merge request and where it came from.
type Side struct {
	Revision string `json:"revision"`
	Kind     string `json:"kind"`
}

// MRSides picks the revisions a merge request diff compares: the live merge
// base (falling back to the recorded one, then the target head or branch)
// and the source head commit (falling back to the source branch).
func MRSides(mr *domain.MergeRequest, m *domain.Mergeability) (Side, Side) {
	if m == nil {
		m = &domain.Mergeability{}
	}
	if mr == nil {
		mr = &domain.MergeRequest{}
	}
	var base Side
	switch {
	case m.MergeBaseCommitID != "":
		base = Side{Revision: m.MergeBaseCommitID, Kind: "merge_base"}
	case mr.MergeBaseCommitID != "":
		base = Side{Revision: mr.MergeBaseCommitID, Kind: "merge_base"}
	case m.TargetCommitID != "":
		base = Side{Revision: m.TargetCommitID, Kind: "target_head"}
	default:
		base = Side{Revision: mr.TargetBranch, Kind: "target_branch"}
	}
	var head Side
	switch {
	case m.SourceCommitID != "":
		head = Side{Revision: m.SourceCommitID, Kind: "source_head"}
	case mr.Review != nil && mr.Review.HeadCommitID != "":
		head = Side{Revision: mr.Review.HeadCommitID, Kind: "source_head"}
	default:
		head = Side{Revision: mr.SourceBranch, Kind: "source_branch"}
	}
	return base, head
}

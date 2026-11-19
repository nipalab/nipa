package gitimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/gitimport"
	"github.com/nipalab/nipa/internal/gitimport/lfs"
)

const (
	RefCreated   = "created"
	RefUpdated   = "updated"
	RefUnchanged = "unchanged"
	RefExists    = "exists"
	RefDiverged  = "diverged"
	RefProtected = "protected"
	RefInvalid   = "invalid"
)

type Options struct {
	GitPath       string
	Host          string
	Org           string
	Project       string
	SourceKey     string
	Refs          []string
	DefaultBranch string
	Depth         int
	Since         time.Time
	Snapshot      bool
	Symlinks      gitimport.SymlinkPolicy
	LFS           lfs.Mode
	DropCache     bool
	AuthorMap     *AuthorMap
	Update        bool
	ResetState    bool
	DryRun        bool
	Confirm       func(*Summary) error
}

type Progress interface {
	ImportStart(commits int)
	ImportProgress(commits int, bytes int64)
	ImportEnd()
}

type RefResult struct {
	Kind     string
	Name     string
	Status   string
	CommitID string
	Message  string
}

func (r RefResult) Warning() bool {
	switch r.Status {
	case RefDiverged, RefProtected, RefExists:
		return true
	}
	return false
}

type Summary struct {
	Target         string
	Source         string
	Mirror         string
	CacheRemoved   bool
	DryRun         bool
	Commits        int
	Known          int
	Imported       int
	UploadedBytes  int64
	BlobsRead      int64
	BlobHits       int64
	LFSMode        lfs.Mode
	LFSEndpoint    string
	LFSRead        int64
	LFSHits        int64
	Branches       int
	Tags           int
	DefaultBranch  string
	Refs           []RefResult
	Warnings       []gitimport.Warning
	Report         *gitimport.Report
	UploadEstimate int64
	QuotaChecked   bool
	QuotaExceeded  string
}

func (s *Summary) RefWarnings() []RefResult {
	var out []RefResult
	for _, r := range s.Refs {
		if r.Warning() {
			out = append(out, r)
		}
	}
	return out
}

func (s *Summary) LFSBlocked() bool {
	if s.Report == nil || len(s.Report.LFSMissing) == 0 || s.LFSMode == lfs.ModeOff {
		return false
	}
	return s.LFSMode == lfs.ModeLocal || s.LFSEndpoint == ""
}

func (s *Summary) RefErrors() []RefResult {
	var out []RefResult
	for _, r := range s.Refs {
		if r.Status == RefInvalid {
			out = append(out, r)
		}
	}
	return out
}

type Importer struct {
	transport   Transport
	http        lfs.Doer
	credentials lfs.CredentialHelper
	cacheRoot   string
	lfsBackoff  time.Duration
}

func NewImporter(t Transport) *Importer {
	return &Importer{transport: t, credentials: lfs.NewGitCredentials()}
}

func (im *Importer) WithHTTPClient(c lfs.Doer) *Importer {
	im.http = c
	return im
}

func (im *Importer) WithCredentials(c lfs.CredentialHelper) *Importer {
	im.credentials = c
	return im
}

func (im *Importer) WithCacheRoot(dir string) *Importer {
	im.cacheRoot = dir
	return im
}

func (im *Importer) WithLFSBackoff(d time.Duration) *Importer {
	im.lfsBackoff = d
	return im
}

func (im *Importer) Run(ctx context.Context, opts Options, progress Progress) (*Summary, error) {
	if opts.SourceKey == "" {
		opts.SourceKey = gitimport.DefaultSourceKey
	}
	if err := gitimport.ValidateSourceKey(opts.SourceKey); err != nil {
		return nil, domain.NewUserError(err.Error())
	}
	symlinks, err := gitimport.ParseSymlinkPolicy(string(opts.Symlinks))
	if err != nil {
		return nil, domain.NewUserError(err.Error())
	}
	opts.Symlinks = symlinks
	if opts.LFS, err = lfs.ParseMode(string(opts.LFS)); err != nil {
		return nil, domain.NewUserError(err.Error())
	}
	if progress == nil {
		progress = noProgress{}
	}
	path, display := opts.GitPath, opts.GitPath
	var remote *remoteSource
	if IsRemoteURL(opts.GitPath) {
		if remote, err = im.mirror(ctx, opts.GitPath); err != nil {
			return nil, err
		}
		path, display = remote.dir, remote.display
	}
	src, err := gitimport.Open(path)
	if err != nil {
		return nil, domain.NewUserError(err.Error())
	}
	sum, err := im.run(ctx, src, remote, display, opts, progress)
	_ = src.Close()
	if err == nil && remote != nil && opts.DropCache {
		if rmErr := os.RemoveAll(remote.dir); rmErr != nil {
			return sum, fmt.Errorf("remove git mirror: %w", rmErr)
		}
		sum.CacheRemoved = true
	}
	return sum, err
}

func (im *Importer) run(ctx context.Context, src *gitimport.Source, remote *remoteSource, display string, opts Options, progress Progress) (*Summary, error) {
	sink := NewSink(im.transport, opts.Org, opts.Project)
	plan, known, err := im.plan(ctx, src, sink, opts)
	if err != nil {
		return nil, err
	}
	sum := &Summary{
		Target:        fmt.Sprintf("%s/%s", opts.Org, opts.Project),
		DryRun:        opts.DryRun,
		Commits:       len(plan.Commits),
		Known:         known,
		Branches:      len(plan.Branches()),
		Tags:          len(plan.Tags()),
		DefaultBranch: plan.DefaultBranch,
		Warnings:      plan.Warnings,
		LFSMode:       opts.LFS,
		Source:        display,
	}
	if remote != nil {
		sum.Mirror = remote.dir
	}
	gitDir, err := GitDir(src)
	if err != nil {
		return nil, err
	}
	resolver, err := im.lfsResolver(src, remote, opts.LFS, gitDir)
	if err != nil {
		return nil, err
	}
	var lfsSource gitimport.LFSSource
	if resolver != nil {
		lfsSource = resolver
		if c, ok := resolver.Remote.(*lfs.Client); ok {
			sum.LFSEndpoint = c.Endpoint()
		}
	}
	if sum.Report, err = im.preview(src, plan, gitDir, lfsSource, opts); err != nil {
		return nil, err
	}
	sum.Warnings = sum.Report.Warnings
	if len(sum.Report.InvalidPaths) > 0 && !opts.DryRun {
		return sum, invalidPathsError(sum.Report.InvalidPaths)
	}
	if err := im.estimate(ctx, sink, sum); err != nil {
		return sum, err
	}
	if opts.DryRun {
		return sum, nil
	}
	if sum.LFSBlocked() {
		return sum, missingLFSError(sum.Report.LFSMissing, opts.LFS)
	}
	if opts.Confirm != nil {
		if err := opts.Confirm(sum); err != nil {
			return sum, err
		}
	}

	state, err := OpenState(filepath.Join(gitDir, StateDirName), opts.ResetState)
	if err != nil {
		return sum, err
	}
	defer func() { _ = state.Close() }()
	if err := state.CheckTarget(StateTarget{Host: opts.Host, Org: opts.Org, Project: opts.Project, SourceKey: opts.SourceKey}); err != nil {
		return sum, err
	}

	if err := im.commit(ctx, src, sink, state, lfsSource, plan, opts, sum, progress); err != nil {
		return sum, withLFSHint(withStateHint(err))
	}
	if err := im.setRefs(ctx, sink, plan, opts, sum); err != nil {
		return sum, err
	}
	return sum, nil
}

func (im *Importer) preview(src *gitimport.Source, plan *gitimport.Plan, gitDir string, lfsSource gitimport.LFSSource, opts Options) (*gitimport.Report, error) {
	copts := gitimport.ConverterOptions{Symlinks: opts.Symlinks, LFS: lfsSource}
	if !opts.ResetState {
		resumed, err := OpenResumeState(filepath.Join(gitDir, StateDirName), StateTarget{Host: opts.Host, Org: opts.Org, Project: opts.Project, SourceKey: opts.SourceKey})
		if err != nil {
			return nil, err
		}
		if resumed != nil {
			defer func() { _ = resumed.Close() }()
			copts.Cache = resumed
		}
	}
	conv, err := gitimport.NewConverter(src, copts)
	if err != nil {
		return nil, err
	}
	return conv.DryRun(plan)
}

func (im *Importer) estimate(ctx context.Context, sink *Sink, sum *Summary) error {
	sum.UploadEstimate = sum.Report.Bytes
	if sum.UploadEstimate <= 0 {
		return nil
	}
	checked, err := sink.Estimate(ctx, sum.UploadEstimate)
	sum.QuotaChecked = checked
	var de *domain.Error
	if err == nil || !errors.As(err, &de) || de.Code != 402 {
		return err
	}
	sum.QuotaChecked = true
	if sum.DryRun {
		sum.QuotaExceeded = de.Message
		return nil
	}
	refused := &domain.Error{Code: de.Code, Message: "nothing was imported: " + de.Message, Cause: err}
	return refused.WithHint("the upload estimate counts the content of every imported commit before deduplication",
		"free storage or ask the organization's billing owner for more, or import less history with --since, --depth or --snapshot")
}

func (s *Summary) QuotaBlocked() bool {
	return s.QuotaExceeded != ""
}

func (im *Importer) plan(ctx context.Context, src *gitimport.Source, sink *Sink, opts Options) (*gitimport.Plan, int, error) {
	planner := gitimport.NewPlanner(src)
	popts := gitimport.Options{
		SourceKey:     opts.SourceKey,
		Refs:          opts.Refs,
		DefaultBranch: opts.DefaultBranch,
		Depth:         opts.Depth,
		Since:         opts.Since,
		Snapshot:      opts.Snapshot,
	}
	plan, err := planner.Plan(popts)
	if err != nil {
		return nil, 0, domain.NewUserError(err.Error())
	}
	if len(plan.Refs) == 0 {
		return nil, 0, domain.NewUserError("no git refs match the selected patterns")
	}
	origins := make([]string, 0, len(plan.Commits))
	for _, c := range plan.Commits {
		origins = append(origins, c.Origin)
	}
	known, err := sink.Lookup(ctx, origins)
	if err != nil {
		return nil, 0, err
	}
	if len(known) == 0 {
		return plan, 0, nil
	}
	popts.Known = known
	plan, err = planner.Plan(popts)
	if err != nil {
		return nil, 0, domain.NewUserError(err.Error())
	}
	return plan, len(known), nil
}

func (im *Importer) commit(ctx context.Context, src *gitimport.Source, sink *Sink, state *State, lfsSource gitimport.LFSSource, plan *gitimport.Plan, opts Options, sum *Summary, progress Progress) error {
	var uploaded atomic.Int64
	var imported atomic.Int64
	var current *usecase.ChunkUploader
	conv, err := gitimport.NewConverter(src, gitimport.ConverterOptions{
		Symlinks: opts.Symlinks,
		Cache:    state,
		LFS:      lfsSource,
		Sink: func(ch chunker.EncodedChunk) error {
			return current.Add(&serverDomain.ChunkData{Hash: ch.Hash, Data: ch.Data, RawSize: ch.RawSize})
		},
	})
	if err != nil {
		return err
	}
	onChunk := func(ch *serverDomain.ChunkData) { uploaded.Add(int64(len(ch.Data))) }
	next := func(ctx context.Context, i int) (*grpc.ImportCommit, error) {
		current = sink.Uploader(ctx, onChunk)
		ev, err := conv.ConvertContext(ctx, plan.Commits[i])
		if err != nil {
			current.Abort()
			return nil, err
		}
		if err := current.Close(); err != nil {
			return nil, err
		}
		if err := state.MarkUploaded(state.PendingKeys()); err != nil {
			return nil, err
		}
		return importCommit(ev, opts.AuthorMap), nil
	}
	progress.ImportStart(len(plan.Commits))
	err = sink.Commit(ctx, len(plan.Commits), next, func(int, *grpc.ImportedCommit) {
		progress.ImportProgress(int(imported.Add(1)), uploaded.Load())
	})
	stats := conv.Stats()
	sum.Imported = int(imported.Load())
	sum.UploadedBytes = uploaded.Load()
	sum.BlobsRead, sum.BlobHits = stats.BlobsRead, stats.BlobHits
	sum.LFSRead, sum.LFSHits = stats.LFSRead, stats.LFSHits
	if err == nil {
		progress.ImportEnd()
	}
	return err
}

func importCommit(ev *gitimport.Event, authors *AuthorMap) *grpc.ImportCommit {
	c := &grpc.ImportCommit{
		Origin:        ev.Origin,
		ParentOrigins: ev.Parents,
		Message:       ev.Message,
		AuthorName:    ev.Author.Name,
		AuthorEmail:   ev.Author.Email,
		AuthoredAt:    ev.CreatedAt(),
		Removed:       ev.Removed,
	}
	if user, ok := authors.Lookup(ev.Author.Name, ev.Author.Email); ok {
		c.AuthorUserID = user
	}
	c.Files = make([]*serverDomain.PushFile, 0, len(ev.Files))
	for i := range ev.Files {
		f := ev.Files[i].PushFile
		c.Files = append(c.Files, &f)
	}
	return c
}

func (im *Importer) setRefs(ctx context.Context, sink *Sink, plan *gitimport.Plan, opts Options, sum *Summary) error {
	refs := grpc.ImportRefs{Update: opts.Update, CommitCount: sum.Imported}
	for _, b := range plan.Branches() {
		refs.Branches = append(refs.Branches, grpc.ImportRef{Name: b.Target, Origin: b.Origin})
	}
	for _, t := range plan.Tags() {
		refs.Tags = append(refs.Tags, grpc.ImportRef{Name: t.Target, Origin: t.Origin, Message: strings.TrimRight(t.Message, "\n")})
	}
	if !opts.Update || opts.DefaultBranch != "" {
		refs.DefaultBranch = plan.DefaultBranch
	}
	results, err := sink.SetRefs(ctx, refs)
	if err != nil {
		return err
	}
	for _, r := range results {
		sum.Refs = append(sum.Refs, RefResult{Kind: r.Kind, Name: r.Name, Status: r.Status, CommitID: r.CommitID, Message: r.Message})
	}
	return nil
}

func invalidPathsError(issues []gitimport.PathIssue) error {
	lines := make([]string, 0, len(issues))
	for _, p := range issues {
		lines = append(lines, fmt.Sprintf("%s (commit %s)", p.Path, short(p.Commit)))
	}
	return domain.NewUserError(fmt.Sprintf("the git history has %d path(s) Nipa cannot store, nothing was imported: %s", len(issues), strings.Join(lines, ", "))).
		WithHint("nothing was imported", "rename or remove these paths in git (for example with git filter-repo), then import again")
}

func withStateHint(err error) error {
	if err == nil || !strings.Contains(err.Error(), "missing chunk data") {
		return err
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return de.WithHint("the server no longer holds content the import state recorded as uploaded", "rerun with --reset-state")
	}
	return err
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

type noProgress struct{}

func (noProgress) ImportStart(int)           {}
func (noProgress) ImportProgress(int, int64) {}
func (noProgress) ImportEnd()                {}

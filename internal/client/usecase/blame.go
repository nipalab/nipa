package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	// BlameMaxBytes caps every version blame reads.
	BlameMaxBytes = 2 << 20
	// BlameMaxCommits matches the server's path history scan.
	BlameMaxCommits = 500
)

type blameClient interface {
	diffClient
	GetPathCommitLog(ctx context.Context, org, project, rev, path string, limit int) ([]*serverDomain.CommitLogEntry, error)
	GetCommitDirManifest(ctx context.Context, org, project, commitID, dir string) (*serverDomain.TreeNode, error)
}

type BlameCommit struct {
	ID          string
	Hash        string
	AuthorName  string
	AuthorEmail string
	Message     string
	CreatedAt   time.Time
}

// BlameLine is one line of the blamed version. CommitID is empty when the line
// is not pushed yet (Uncommitted) or older than the history blame could read.
type BlameLine struct {
	Text        string
	CommitID    string
	Uncommitted bool
}

type BlameResult struct {
	Path string
	// Revision is the commit blamed, or empty for the working copy.
	Revision string
	Lines    []BlameLine
	// Commits are the commits lines point at, newest first.
	Commits []BlameCommit
	// Truncated reports lines older than the history blame could read.
	Truncated bool
}

// BlameProgress reports versions read out of the most blame may need.
type BlameProgress func(done, total int)

// Blame attributes every line of a text file to the commit that last changed
// it, walking the file's history newest first through the commit-scoped
// download and stopping once every line is attributed.
type Blame struct {
	auth   *Auth
	client blameClient
	repo   diffLocalRepo
}

func NewBlame(auth *Auth, client blameClient, repo diffLocalRepo) *Blame {
	return &Blame{auth: auth, client: client, repo: repo}
}

type blameVersion struct {
	lines []string
	ok    bool
}

// Run blames path as of rev, or the working file when rev is empty.
func (b *Blame) Run(ctx context.Context, root, filePath, rev string, progress BlameProgress) (res *BlameResult, err error) {
	p, err := cleanBlobPath(filePath)
	if err != nil {
		return nil, err
	}
	if err := b.repo.Init(root); err != nil {
		return nil, err
	}
	defer func() { pruneAfterSuccess(b.repo, err) }()
	unlock, err := lockShared(b.repo)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cfg, err := b.repo.LoadConfig()
	if err != nil {
		return nil, err
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, err
	}

	var working []string
	if strings.TrimSpace(rev) == "" {
		if working, err = readWorkingText(root, p); err != nil {
			return nil, err
		}
	}
	if err := b.client.Connect(ctx, nu.Origin); err != nil {
		return nil, err
	}
	if err := b.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return nil, err
	}
	start, err := b.startCommit(ctx, nu, cfg, rev)
	if err != nil {
		return nil, err
	}
	w := &blameWalk{b: b, ctx: ctx, nu: nu, path: p, progress: progress}
	res = &BlameResult{Path: p}
	if working == nil {
		res.Revision = start
	}
	if start == "" {
		if working == nil {
			return nil, clientDomain.NewUserError("the branch has no commits yet")
		}
		return w.attribute(res, working, &blameVersion{}, nil)
	}
	log, err := b.client.GetPathCommitLog(ctx, nu.Org, nu.Project, start, p, BlameMaxCommits)
	if err != nil {
		return nil, err
	}
	w.total = len(log) + 1
	newestID := start
	if len(log) > 0 {
		newestID = log[0].ID.Base36()
	}
	newest, err := w.version(newestID)
	if err != nil && (working == nil || !isBlameLimit(err)) {
		return nil, err
	}
	if working != nil {
		if err != nil {
			res.Lines, res.Truncated = blameLines(working), len(working) > 0
			return res, nil
		}
		return w.attribute(res, working, &newest, log)
	}
	if !newest.ok {
		return nil, missingFile(fmt.Sprintf("%s does not exist in %s", p, start))
	}
	return w.attribute(res, newest.lines, nil, log)
}

func (b *Blame) startCommit(ctx context.Context, nu *clientDomain.NipaUrl, cfg *clientDomain.Config, rev string) (string, error) {
	token := strings.TrimSpace(rev)
	if token == "" || token == "HEAD" || token == "@" {
		pin, err := b.repo.LoadCommit()
		if err != nil {
			return "", err
		}
		if pin != nil && pin.CommitID != "" {
			return pin.CommitID, nil
		}
		token = cfg.Branch
	}
	branch, err := b.client.GetBranchByName(ctx, nu.Org, nu.Project, token)
	if err == nil {
		if branch == nil || branch.CommitID == nil {
			return "", nil
		}
		return branch.CommitID.Base36(), nil
	}
	if !isNotFoundError(err) {
		return "", err
	}
	tag, err := b.client.GetTagByName(ctx, nu.Org, nu.Project, token)
	if err == nil {
		return tag.CommitID, nil
	}
	if !isNotFoundError(err) {
		return "", err
	}
	if _, perr := snow.ParseBase36(token); perr != nil {
		return "", clientDomain.NewUserError(fmt.Sprintf("revision %q not found", token))
	}
	return token, nil
}

type blameWalk struct {
	b        *Blame
	ctx      context.Context
	nu       *clientDomain.NipaUrl
	path     string
	progress BlameProgress
	done     int
	total    int
}

func blameLines(target []string) []BlameLine {
	lines := make([]BlameLine, len(target))
	for i, line := range target {
		lines[i].Text = line
	}
	return lines
}

// attribute carries every target line back through the file's versions,
// newest first. A line a version adds compared with the next older one
// belongs to that version's commit; with a working base, lines the base
// lacks are not pushed yet.
func (w *blameWalk) attribute(res *BlameResult, target []string, base *blameVersion, log []*serverDomain.CommitLogEntry) (*BlameResult, error) {
	res.Lines = blameLines(target)
	remaining := len(target)
	pos := make([]int, len(target))
	for i := range pos {
		pos[i] = i
	}
	cur := target
	if base != nil {
		pos, remaining = carry(cur, pos, base.lines, remaining, func(t int) { res.Lines[t].Uncommitted = true })
		cur = base.lines
	}
	used := map[string]bool{}
	for i := 0; i < len(log) && remaining > 0; i++ {
		id := log[i].ID.Base36()
		older, err := w.olderVersion(log, i)
		if isBlameLimit(err) {
			break
		}
		if err != nil {
			return nil, err
		}
		pos, remaining = carry(cur, pos, older.lines, remaining, func(t int) {
			res.Lines[t].CommitID = id
			used[id] = true
		})
		cur = older.lines
	}
	res.Truncated = remaining > 0
	for _, entry := range log {
		if used[entry.ID.Base36()] {
			res.Commits = append(res.Commits, blameCommit(entry))
		}
	}
	if w.progress != nil && w.done > 0 {
		w.progress(w.done, w.done)
	}
	return res, nil
}

// olderVersion reads the file before log[i]: the next older commit that
// changed it, or the first parent of the oldest one.
func (w *blameWalk) olderVersion(log []*serverDomain.CommitLogEntry, i int) (blameVersion, error) {
	if i+1 < len(log) {
		return w.version(log[i+1].ID.Base36())
	}
	if parent := log[i].Parent1ID; parent != nil {
		return w.version(parent.Base36())
	}
	return blameVersion{}, nil
}

// carry maps the still unattributed lines of cur (pos: index in the target,
// -1 once attributed) onto older and hands the lines older lacks to
// attribute.
func carry(cur []string, pos []int, older []string, remaining int, attribute func(target int)) ([]int, int) {
	next := make([]int, len(older))
	for i := range next {
		next[i] = -1
	}
	for _, op := range diff.Lines(older, cur) {
		switch op.Kind {
		case ' ':
			next[op.A] = pos[op.B]
		case '+':
			if t := pos[op.B]; t >= 0 {
				attribute(t)
				remaining--
			}
		}
	}
	return next, remaining
}

func (w *blameWalk) version(commitID string) (blameVersion, error) {
	defer func() {
		w.done++
		if w.progress != nil {
			w.progress(w.done, max(w.total, w.done))
		}
	}()
	tree, err := w.b.client.GetCommitDirManifest(w.ctx, w.nu.Org, w.nu.Project, commitID, parentDir(w.path))
	if err != nil {
		if isNotFoundError(err) {
			return blameVersion{}, nil
		}
		return blameVersion{}, err
	}
	entry, ok := diff.FromTree(tree)[w.path]
	if !ok {
		return blameVersion{}, nil
	}
	if entry.IsBinary {
		return blameVersion{}, &blameLimit{notText(w.path)}
	}
	if entry.SizeBytes > BlameMaxBytes {
		return blameVersion{}, &blameLimit{tooLargeToBlame(w.path)}
	}
	missing, err := w.b.repo.MissingChunks(entry.ChunkHashes)
	if err != nil {
		return blameVersion{}, err
	}
	scope := clientDomain.ChunkScope{Org: w.nu.Org, Project: w.nu.Project, CommitIDs: commitIDs(commitID), Paths: []string{w.path}}
	if err := downloadMissing(w.ctx, w.b.client, w.b.repo, scope, missing, entry.SizeBytes); err != nil {
		return blameVersion{}, err
	}
	lines, err := readText(w.path, &blobReader{load: w.b.repo.LoadChunk, entry: entry})
	if err != nil {
		return blameVersion{}, err
	}
	return blameVersion{lines: lines, ok: true}, nil
}

// blameLimit marks a version blame cannot read (binary or too large): an
// error for the blamed version, the end of history for older ones.
type blameLimit struct{ err error }

func (e *blameLimit) Error() string { return e.err.Error() }
func (e *blameLimit) Unwrap() error { return e.err }

func isBlameLimit(err error) bool {
	var limit *blameLimit
	return errors.As(err, &limit)
}

func readWorkingText(root, p string) ([]string, error) {
	var lines []string
	err := readWorkingBlob(root, p, func(e diff.Entry, r io.Reader) error {
		if e.IsBinary {
			return notText(p)
		}
		var err error
		lines, err = readText(p, r)
		return err
	})
	var limit *blameLimit
	if errors.As(err, &limit) {
		return nil, limit.err
	}
	return lines, err
}

func readText(p string, r io.Reader) ([]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, BlameMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > BlameMaxBytes {
		return nil, &blameLimit{tooLargeToBlame(p)}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, &blameLimit{notText(p)}
	}
	return splitBlameLines(data), nil
}

func splitBlameLines(data []byte) []string {
	if len(data) == 0 {
		return []string{}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

func parentDir(p string) string {
	if dir := path.Dir(p); dir != "." {
		return dir
	}
	return ""
}

func notText(p string) error {
	return clientDomain.NewUserError(fmt.Sprintf("%s is not a text file; blame works on text files only", p))
}

func tooLargeToBlame(p string) error {
	return clientDomain.NewUserError(fmt.Sprintf("%s is larger than %d MiB; blame works on smaller text files", p, BlameMaxBytes>>20))
}

func blameCommit(e *serverDomain.CommitLogEntry) BlameCommit {
	c := BlameCommit{ID: e.ID.Base36(), AuthorName: e.AuthorName, AuthorEmail: e.AuthorEmail, Message: e.Message, CreatedAt: e.CreatedAt}
	if e.Hash != (serverDomain.Hash{}) {
		c.Hash = e.Hash.String()
	}
	return c
}

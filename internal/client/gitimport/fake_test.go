package gitimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fixture struct {
	t     *testing.T
	dir   string
	repo  *git.Repository
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	return &fixture{t: t, dir: dir, repo: repo, clock: time.Date(2020, 3, 4, 10, 0, 0, 0, time.UTC)}
}

func (f *fixture) store(o object.Object) plumbing.Hash {
	obj := f.repo.Storer.NewEncodedObject()
	require.NoError(f.t, o.Encode(obj))
	h, err := f.repo.Storer.SetEncodedObject(obj)
	require.NoError(f.t, err)
	return h
}

func (f *fixture) blob(content string) plumbing.Hash {
	obj := f.repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	require.NoError(f.t, err)
	_, err = w.Write([]byte(content))
	require.NoError(f.t, err)
	require.NoError(f.t, w.Close())
	h, err := f.repo.Storer.SetEncodedObject(obj)
	require.NoError(f.t, err)
	return h
}

func (f *fixture) tree(files map[string]string) plumbing.Hash {
	direct := map[string]string{}
	nested := map[string]map[string]string{}
	for p, c := range files {
		if i := strings.IndexByte(p, '/'); i >= 0 {
			if nested[p[:i]] == nil {
				nested[p[:i]] = map[string]string{}
			}
			nested[p[:i]][p[i+1:]] = c
			continue
		}
		direct[p] = c
	}
	var entries []object.TreeEntry
	for name, c := range direct {
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Regular, Hash: f.blob(c)})
	}
	for name, sub := range nested {
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: f.tree(sub)})
	}
	key := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	sort.Slice(entries, func(i, j int) bool { return key(entries[i]) < key(entries[j]) })
	return f.store(&object.Tree{Entries: entries})
}

func (f *fixture) commit(author string, files map[string]string, msg string, parents ...plumbing.Hash) plumbing.Hash {
	f.clock = f.clock.Add(time.Hour)
	return f.store(&object.Commit{
		Author:       object.Signature{Name: author, Email: strings.ToLower(author) + "@example.com", When: f.clock.Add(-time.Minute)},
		Committer:    object.Signature{Name: author, Email: strings.ToLower(author) + "@example.com", When: f.clock},
		Message:      msg,
		TreeHash:     f.tree(files),
		ParentHashes: parents,
	})
}

func (f *fixture) branch(name string, h plumbing.Hash) {
	require.NoError(f.t, f.repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/"+name), h)))
}

func (f *fixture) tag(name, msg string, target plumbing.Hash) {
	h := f.store(&object.Tag{Name: name, Tagger: object.Signature{Name: "Ada", Email: "ada@example.com", When: f.clock}, Message: msg, TargetType: plumbing.CommitObject, Target: target})
	require.NoError(f.t, f.repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/tags/"+name), h)))
}

func (f *fixture) head(branch string) {
	require.NoError(f.t, f.repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.ReferenceName("refs/heads/"+branch))))
}

type sampleRepo struct {
	*fixture
	root, child, feature, merge plumbing.Hash
}

func newSampleRepo(t *testing.T) *sampleRepo {
	f := newFixture(t)
	r := &sampleRepo{fixture: f}
	r.root = f.commit("Ann", map[string]string{"README.md": "hello\n", "art/logo.png": "\x89PNG\x00\x01\x02"}, "root\n")
	r.child = f.commit("Bob", map[string]string{"README.md": "hello again\n", "art/logo.png": "\x89PNG\x00\x01\x02"}, "child\n", r.root)
	r.feature = f.commit("Ann", map[string]string{"README.md": "hello\n", "art/logo.png": "\x89PNG\x00\x01\x02", "docs/a.md": "feature\n"}, "feature\n", r.root)
	r.merge = f.commit("Bob", map[string]string{"README.md": "hello again\n", "art/logo.png": "\x89PNG\x00\x01\x02", "docs/a.md": "feature\n"}, "merge\n", r.child, r.feature)
	f.branch("main", r.merge)
	f.branch("feature", r.feature)
	f.tag("v1", "first release\n", r.child)
	f.head("main")
	return r
}

type fakeCommit struct {
	commit *grpc.ImportCommit
	id     snow.ID
}

type fakeServer struct {
	mu          sync.Mutex
	nextID      snow.ID
	commits     map[string]*fakeCommit
	order       []string
	stored      map[serverDomain.Hash]bool
	offered     map[serverDomain.Hash]int
	branches    map[string]string
	tags        map[string]string
	protected   map[string]bool
	defaultBr   string
	lookups     [][]string
	failAt      int
	sent        int
	streams     int
	setRefs     []grpc.ImportRefs
	lookupErr   error
	setRefsErr  error
	openErr     error
	uploadErr   error
	quota       int64
	hasQuota    bool
	estimates   []int64
	estimateErr error
	uploads     int
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		nextID:    100,
		commits:   map[string]*fakeCommit{},
		stored:    map[serverDomain.Hash]bool{},
		offered:   map[serverDomain.Hash]int{},
		branches:  map[string]string{},
		tags:      map[string]string{},
		protected: map[string]bool{},
		failAt:    -1,
	}
}

func (s *fakeServer) ImportLookup(_ context.Context, _, _ string, origins []string) (map[string]grpc.ImportedCommit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	s.lookups = append(s.lookups, append([]string(nil), origins...))
	out := map[string]grpc.ImportedCommit{}
	for _, o := range origins {
		if c, ok := s.commits[o]; ok {
			out[o] = grpc.ImportedCommit{Origin: o, CommitID: c.id}
		}
	}
	return out, nil
}

func (s *fakeServer) ImportEstimate(_ context.Context, _, _ string, bytes int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookupErr != nil {
		return false, s.lookupErr
	}
	s.estimates = append(s.estimates, bytes)
	if s.estimateErr != nil {
		return false, s.estimateErr
	}
	if !s.hasQuota {
		return false, nil
	}
	if bytes > s.quota {
		return true, &domain.Error{Code: 402, Message: "storage quota exceeded"}
	}
	return true, nil
}

func (s *fakeServer) UploadChunks(_ context.Context, _ domain.ChunkScope, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads++
	if s.uploadErr != nil {
		return 0, 0, s.uploadErr
	}
	for _, ch := range chunks {
		s.offered[ch.Hash]++
		s.stored[ch.Hash] = true
		if len(onChunk) > 0 && onChunk[0] != nil {
			onChunk[0](ch)
		}
	}
	return len(chunks), 0, nil
}

func (s *fakeServer) OpenImport(_ context.Context, _, _ string) (CommitStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openErr != nil {
		return nil, s.openErr
	}
	s.streams++
	return &fakeStream{s: s, answers: make(chan streamAnswer, 1024), done: make(chan struct{})}, nil
}

func (s *fakeServer) apply(c *grpc.ImportCommit) (*grpc.ImportedCommit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt >= 0 && s.sent >= s.failAt {
		return nil, errors.New("injected failure")
	}
	s.sent++
	for _, p := range c.ParentOrigins {
		if _, ok := s.commits[p]; !ok {
			return nil, fmt.Errorf("unknown parent %s", p)
		}
	}
	for _, f := range c.Files {
		for _, h := range f.ChunkHashes {
			if !s.stored[h] {
				return nil, fmt.Errorf("missing chunk data for %s", h)
			}
		}
	}
	if existing, ok := s.commits[c.Origin]; ok {
		return &grpc.ImportedCommit{Origin: c.Origin, CommitID: existing.id}, nil
	}
	s.nextID++
	s.commits[c.Origin] = &fakeCommit{commit: c, id: s.nextID}
	s.order = append(s.order, c.Origin)
	return &grpc.ImportedCommit{Origin: c.Origin, CommitID: s.nextID}, nil
}

func (s *fakeServer) ancestor(anc, desc string) bool {
	queue := []string{desc}
	seen := map[string]bool{}
	for len(queue) > 0 {
		o := queue[0]
		queue = queue[1:]
		if o == anc {
			return true
		}
		if seen[o] {
			continue
		}
		seen[o] = true
		if c, ok := s.commits[o]; ok {
			queue = append(queue, c.commit.ParentOrigins...)
		}
	}
	return false
}

func (s *fakeServer) ImportSetRefs(_ context.Context, _, _ string, refs grpc.ImportRefs) ([]grpc.ImportRefResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setRefsErr != nil {
		return nil, s.setRefsErr
	}
	s.setRefs = append(s.setRefs, refs)
	var out []grpc.ImportRefResult
	for _, b := range refs.Branches {
		res := grpc.ImportRefResult{Kind: "branch", Name: b.Name}
		cur, exists := s.branches[b.Name]
		switch {
		case !exists:
			s.branches[b.Name] = b.Origin
			res.Status = RefCreated
		case cur == b.Origin:
			res.Status = RefUnchanged
		case s.protected[b.Name]:
			res.Status = RefProtected
		case !refs.Update:
			res.Status = RefExists
		case !s.ancestor(cur, b.Origin):
			res.Status = RefDiverged
		default:
			s.branches[b.Name] = b.Origin
			res.Status = RefUpdated
		}
		out = append(out, res)
	}
	for _, t := range refs.Tags {
		res := grpc.ImportRefResult{Kind: "tag", Name: t.Name, Status: RefCreated}
		if cur, ok := s.tags[t.Name]; ok {
			res.Status = RefUnchanged
			if cur != t.Origin {
				res.Status = RefExists
			}
		} else {
			s.tags[t.Name] = t.Origin
		}
		out = append(out, res)
	}
	if refs.DefaultBranch != "" {
		s.defaultBr = refs.DefaultBranch
		out = append(out, grpc.ImportRefResult{Kind: "default", Name: refs.DefaultBranch, Status: RefUpdated})
	}
	return out, nil
}

type streamAnswer struct {
	commit *grpc.ImportedCommit
	err    error
}

type fakeStream struct {
	s       *fakeServer
	answers chan streamAnswer
	broken  bool
	closed  bool
	done    chan struct{}
	once    sync.Once
}

func (f *fakeStream) Send(c *grpc.ImportCommit) error {
	if f.broken {
		return nil
	}
	res, err := f.s.apply(c)
	if err != nil {
		f.broken = true
	}
	f.answers <- streamAnswer{commit: res, err: err}
	return nil
}

func (f *fakeStream) Recv() (*grpc.ImportedCommit, error) {
	select {
	case a := <-f.answers:
		return a.commit, a.err
	case <-f.done:
		return nil, io.EOF
	}
}

func (f *fakeStream) CloseSend() error {
	f.closed = true
	return nil
}

func (f *fakeStream) Close() { f.once.Do(func() { close(f.done) }) }

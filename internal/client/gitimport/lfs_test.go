package gitimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/gitimport"
	"github.com/nipalab/nipa/internal/gitimport/lfs"
)

func pointerFor(content string) lfs.Pointer {
	sum := sha256.Sum256([]byte(content))
	return lfs.Pointer{OID: hex.EncodeToString(sum[:]), Size: int64(len(content))}
}

func storeLFS(t *testing.T, gitDir, content string) {
	t.Helper()
	p := pointerFor(content)
	s := lfs.NewLocalStore(gitDir)
	require.NoError(t, os.MkdirAll(filepath.Dir(s.Path(p.OID)), 0o755))
	require.NoError(t, os.WriteFile(s.Path(p.OID), []byte(content), 0o644))
}

func setLFSURL(t *testing.T, f *fixture, url string) {
	t.Helper()
	cfg, err := f.repo.Config()
	require.NoError(t, err)
	cfg.Raw.Section("lfs").SetOption("url", url)
	require.NoError(t, f.repo.SetConfig(cfg))
}

type lfsRepo struct {
	*fixture
	hero, map1 string
	first      string
	second     string
}

func newLFSRepo(t *testing.T) *lfsRepo {
	f := newFixture(t)
	r := &lfsRepo{fixture: f, hero: "\x89PNG\x00" + strings.Repeat("hero", 64), map1: "\x00MAP" + strings.Repeat("tile", 64)}
	root := f.commit("Ann", map[string]string{"art/hero.png": pointerFor(r.hero).String(), "README.md": "game\n"}, "root\n")
	next := f.commit("Bob", map[string]string{"art/hero.png": pointerFor(r.hero).String(), "README.md": "game\n", "maps/one.map": pointerFor(r.map1).String()}, "map\n", root)
	f.branch("main", next)
	f.head("main")
	r.first, r.second = gitimport.Origin("default", root), gitimport.Origin("default", next)
	return r
}

func fileOf(srv *fakeServer, origin, path string) *struct {
	size int64
	bin  bool
} {
	for _, f := range srv.commits[origin].commit.Files {
		if f.Path == path {
			return &struct {
				size int64
				bin  bool
			}{f.SizeBytes, f.IsBinary}
		}
	}
	return nil
}

func TestRun_LFSLocalStore(t *testing.T) {
	repo := newLFSRepo(t)
	storeLFS(t, filepath.Join(repo.dir, ".git"), repo.hero)
	storeLFS(t, filepath.Join(repo.dir, ".git"), repo.map1)
	srv := newFakeServer()

	sum, err := NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.NoError(t, err)
	require.Equal(t, lfs.ModeAuto, sum.LFSMode)
	require.Empty(t, sum.LFSEndpoint)
	require.Equal(t, 2, sum.Report.LFSObjects)
	require.Empty(t, sum.Report.LFSMissing)
	require.Equal(t, int64(2), sum.LFSRead)
	require.Equal(t, int64(len(repo.hero)), fileOf(srv, repo.first, "art/hero.png").size)
	require.True(t, fileOf(srv, repo.second, "maps/one.map").bin)

	state, err := OpenState(filepath.Join(repo.dir, ".git", StateDirName), false)
	require.NoError(t, err)
	defer func() { _ = state.Close() }()
	n, err := state.LFSObjects()
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestRun_LFSOffKeepsPointers(t *testing.T) {
	repo := newLFSRepo(t)
	srv := newFakeServer()
	opts := baseOptions(repo.dir)
	opts.LFS = lfs.ModeOff
	sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Equal(t, 2, sum.Report.LFSObjects)
	require.Zero(t, sum.LFSRead)
	require.Equal(t, int64(len(pointerFor(repo.hero).String())), fileOf(srv, repo.first, "art/hero.png").size)
}

func TestRun_LFSMissingLocally(t *testing.T) {
	repo := newLFSRepo(t)
	storeLFS(t, filepath.Join(repo.dir, ".git"), repo.hero)
	srv := newFakeServer()

	opts := baseOptions(repo.dir)
	opts.LFS = lfs.ModeLocal
	opts.DryRun = true
	sum, err := NewImporter(srv).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Len(t, sum.Report.LFSMissing, 1)
	require.Equal(t, pointerFor(repo.map1).OID, sum.Report.LFSMissing[0].OID)
	require.True(t, sum.LFSBlocked())

	opts.DryRun = false
	_, err = NewImporter(srv).Run(context.Background(), opts, nil)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Message, "1 lfs object(s) are not in the local lfs store")
	require.Contains(t, de.Action, "--lfs auto")
	require.Empty(t, srv.commits)

	opts.LFS = lfs.ModeAuto
	_, err = NewImporter(srv).Run(context.Background(), opts, nil)
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Action, "lfs.url")

	opts.LFS = "remote"
	_, err = NewImporter(srv).Run(context.Background(), opts, nil)
	require.ErrorContains(t, err, "invalid lfs mode")
}

type testLFSServer struct {
	mu        sync.Mutex
	objects   map[string][]byte
	downloads int
	status    int
	srv       *httptest.Server
}

func newTestLFSServer(t *testing.T, contents ...string) *testLFSServer {
	s := &testLFSServer{objects: map[string][]byte{}}
	for _, c := range contents {
		s.objects[pointerFor(c).OID] = []byte(c)
	}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *testLFSServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/objects/batch") {
		var req struct {
			Objects []struct {
				OID  string `json:"oid"`
				Size int64  `json:"size"`
			} `json:"objects"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var objs []map[string]any
		for _, o := range req.Objects {
			obj := map[string]any{"oid": o.OID, "size": o.Size}
			if _, ok := s.objects[o.OID]; ok {
				obj["actions"] = map[string]any{"download": map[string]any{"href": s.srv.URL + "/dl/" + o.OID}}
			} else {
				obj["error"] = map[string]any{"code": 404, "message": "not found"}
			}
			objs = append(objs, obj)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"transfer": "basic", "objects": objs})
		return
	}
	s.downloads++
	_, _ = w.Write(s.objects[strings.TrimPrefix(r.URL.Path, "/dl/")])
}

func TestRun_LFSBatchAPIAndResume(t *testing.T) {
	repo := newLFSRepo(t)
	lfsSrv := newTestLFSServer(t, repo.hero, repo.map1)
	setLFSURL(t, repo.fixture, lfsSrv.srv.URL+"/info/lfs")
	srv := newFakeServer()
	srv.failAt = 1

	opts := baseOptions(repo.dir)
	dry := opts
	dry.DryRun = true
	sum, err := NewImporter(srv).Run(context.Background(), dry, nil)
	require.NoError(t, err)
	require.Len(t, sum.Report.LFSMissing, 2)
	require.False(t, sum.LFSBlocked())
	require.Equal(t, lfsSrv.srv.URL+"/info/lfs", sum.LFSEndpoint)

	sum, err = NewImporter(srv).WithLFSBackoff(-1).WithHTTPClient(http.DefaultClient).Run(context.Background(), opts, nil)
	require.ErrorContains(t, err, "injected failure")
	require.Equal(t, 1, sum.Imported)
	require.Equal(t, 2, lfsSrv.downloads)

	srv.failAt = -1
	sum, err = NewImporter(srv).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Equal(t, 1, sum.Imported)
	require.Zero(t, sum.LFSRead)
	require.Equal(t, int64(1), sum.LFSHits)
	require.Equal(t, 2, lfsSrv.downloads, "a resumed import neither downloads nor rechunks lfs objects")
	require.Equal(t, int64(len(repo.map1)), fileOf(srv, repo.second, "maps/one.map").size)
	entries, _ := os.ReadDir(filepath.Join(repo.dir, ".git", StateDirName, "tmp"))
	require.Empty(t, entries)
}

func TestRun_LFSServerErrorsHaveHints(t *testing.T) {
	repo := newLFSRepo(t)
	lfsSrv := newTestLFSServer(t, repo.hero)
	setLFSURL(t, repo.fixture, lfsSrv.srv.URL+"/info/lfs")
	srv := newFakeServer()

	_, err := NewImporter(srv).WithCredentials(nil).Run(context.Background(), baseOptions(repo.dir), nil)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Hint, "does not have this object")

	lfsSrv.status = http.StatusUnauthorized
	_, err = NewImporter(srv).WithCredentials(nil).Run(context.Background(), baseOptions(repo.dir), nil)
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Action, "credential helper")

	setLFSURL(t, repo.fixture, "ftp://nowhere")
	_, err = NewImporter(srv).Run(context.Background(), baseOptions(repo.dir), nil)
	require.ErrorContains(t, err, "invalid lfs endpoint")
}

func TestWithLFSHint(t *testing.T) {
	require.NoError(t, withLFSHint(nil))
	plain := errors.New("x")
	require.Equal(t, plain, withLFSHint(plain))
	var de *domain.Error
	require.ErrorAs(t, withLFSHint(&lfs.MissingError{OID: "o"}), &de)
	require.Contains(t, de.Action, "git lfs fetch")
	require.ErrorAs(t, withLFSHint(lfs.ErrCorrupt), &de)
	require.Contains(t, de.Action, "fsck")
	require.ErrorAs(t, withLFSHint(&lfs.ServerError{Status: 403}), &de)
	require.Contains(t, de.Hint, "refused")

	var many []gitimport.LFSObject
	for i := 0; i < 12; i++ {
		many = append(many, gitimport.LFSObject{OID: "o", Path: "p"})
	}
	require.ErrorContains(t, missingLFSError(many, lfs.ModeLocal), "and 2 more")
}

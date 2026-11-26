package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treedelta"
	"github.com/nipalab/nipa/internal/treehash"
)

// deltaSeeds are the fixed seeds of the delta property test. A failure names
// its seed; run it alone with -run 'ManifestDeltaProperty/seed=N'.
var deltaSeeds = []int64{1, 2, 3, 5, 8, 13, 21, 34}

const deltaUserID = 47

// deltaFile is one file of the model tree the property test pushes.
type deltaFile struct {
	content string
	mode    int
	binary  bool
}

type deltaModel map[string]deltaFile

func (m deltaModel) clone() deltaModel {
	out := make(deltaModel, len(m))
	for path, f := range m {
		out[path] = f
	}
	return out
}

func (m deltaModel) paths() []string {
	out := make([]string, 0, len(m))
	for path := range m {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// dirs lists every directory that holds a file, root excluded.
func (m deltaModel) dirs() []string {
	seen := map[string]bool{}
	for path := range m {
		for i := 0; i < len(path); i++ {
			if path[i] == '/' {
				seen[path[:i]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for dir := range seen {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

// set writes a file, replacing a file that sits where a directory is needed
// and a directory that sits where the file goes.
func (m deltaModel) set(path string, f deltaFile) {
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			delete(m, path[:i])
		}
	}
	m.removeDir(path)
	m[path] = f
}

func (m deltaModel) removeDir(dir string) {
	for path := range m {
		if strings.HasPrefix(path, dir+"/") {
			delete(m, path)
		}
	}
}

var (
	deltaDirNames  = []string{"a", "b", "c", "d"}
	deltaFileNames = []string{"f1.txt", "f2.txt", "x.cfg", "tool.sh", "blob.dat"}
	deltaRootFiles = []string{".nipaignore", "README.md", "f1.txt", "f2.txt"}
	deltaModes     = []int{0o644, 0o755, 0o444}
)

type deltaGen struct {
	rng  *rand.Rand
	tick int
	// noSwaps keeps mutate from turning a file into a directory of the same
	// name or the reverse.
	noSwaps bool
}

func (g *deltaGen) pick(list []string) string { return list[g.rng.Intn(len(list))] }

func (g *deltaGen) dirPath(maxDepth int) string {
	depth := g.rng.Intn(maxDepth + 1)
	parts := make([]string, 0, depth)
	for i := 0; i < depth; i++ {
		parts = append(parts, g.pick(deltaDirNames))
	}
	return strings.Join(parts, "/")
}

func (g *deltaGen) filePath() string {
	dir := g.dirPath(3)
	if dir == "" {
		return g.pick(deltaRootFiles)
	}
	return dir + "/" + g.pick(deltaFileNames)
}

func (g *deltaGen) file() deltaFile {
	g.tick++
	f := deltaFile{mode: deltaModes[g.rng.Intn(len(deltaModes))]}
	switch g.rng.Intn(12) {
	case 0:
		// No content, no chunks.
	case 1:
		// Several chunks.
		raw := make([]byte, 700<<10)
		_, _ = g.rng.Read(raw)
		f.content, f.binary = string(raw), true
	case 2, 3:
		f.content, f.binary = fmt.Sprintf("bin\x00%d\x00%d", g.tick, g.rng.Int63()), true
	default:
		f.content = fmt.Sprintf("text %d %d\n", g.tick, g.rng.Int63())
	}
	return f
}

// mutate applies a few random operations to the model.
func (g *deltaGen) mutate(m deltaModel) {
	for ops := 1 + g.rng.Intn(4); ops > 0; ops-- {
		paths, dirs := m.paths(), m.dirs()
		switch op := g.rng.Intn(13); {
		case op <= 2 || len(paths) == 0:
			m.set(g.filePath(), g.file())
		case op == 3:
			path := g.pick(paths)
			f := m[path]
			f.content = g.file().content + "modified"
			m[path] = f
		case op == 4:
			delete(m, g.pick(paths))
		case op == 5:
			path := g.pick(paths)
			f := m[path]
			f.mode = deltaModes[(indexOfMode(f.mode)+1+g.rng.Intn(2))%len(deltaModes)]
			m[path] = f
		case op == 6:
			path := g.pick(paths)
			f := m[path]
			f.binary = !f.binary
			if f.binary {
				f.content = fmt.Sprintf("flip\x00%d", g.rng.Int63())
			} else {
				f.content = fmt.Sprintf("flip %d\n", g.rng.Int63())
			}
			m[path] = f
		case op == 7 && len(dirs) > 0:
			g.transplant(m, g.pick(dirs), true)
		case op == 8 && len(dirs) > 0:
			g.transplant(m, g.pick(dirs), false)
		case op == 9 && len(dirs) > 0:
			m.removeDir(g.pick(dirs))
		case op == 10 && !g.noSwaps:
			// A file becomes a directory of the same name.
			path := g.pick(paths)
			m.set(path+"/"+g.pick(deltaFileNames), g.file())
		case op == 11 && len(dirs) > 0 && !g.noSwaps:
			// A directory becomes a file of the same name.
			m.set(g.pick(dirs), g.file())
		default:
			m.set(g.filePath(), g.file())
		}
	}
}

func indexOfMode(mode int) int {
	for i, m := range deltaModes {
		if m == mode {
			return i
		}
	}
	return 0
}

// transplant copies the subtree at from to another directory, giving two
// identical directories, and removes the original when move is set.
func (g *deltaGen) transplant(m deltaModel, from string, move bool) {
	to := g.dirPath(2)
	if to == "" {
		to = g.pick(deltaDirNames)
	}
	if to == from || strings.HasPrefix(to, from+"/") || strings.HasPrefix(from, to+"/") {
		return
	}
	moved := deltaModel{}
	for path, f := range m {
		if strings.HasPrefix(path, from+"/") {
			moved[to+strings.TrimPrefix(path, from)] = f
			if move {
				delete(m, path)
			}
		}
	}
	m.removeDir(to)
	for _, path := range moved.paths() {
		m.set(path, moved[path])
	}
}

// deltaPush pushes the difference between two models as one commit and
// returns its id.
func deltaPush(t *testing.T, ctx context.Context, c *clientgrpc.Client, before, after deltaModel, baseCommit string) string {
	t.Helper()
	var files []*serverDomain.PushFile
	var chunks []*serverDomain.ChunkData
	for _, path := range after.paths() {
		f := after[path]
		if old, ok := before[path]; ok && old == f {
			continue
		}
		encoding, encoded, err := chunker.EncodeBytes([]byte(f.content), path, f.binary, "")
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, 0, len(encoded))
		for _, e := range encoded {
			hashes = append(hashes, e.Hash)
			chunks = append(chunks, &serverDomain.ChunkData{Hash: e.Hash, Data: e.Data, RawSize: e.RawSize})
		}
		files = append(files, &serverDomain.PushFile{
			Path: path, Mode: f.mode, SizeBytes: int64(len(f.content)), IsBinary: f.binary,
			Encoding: encoding, FileHash: treehash.FileHash(hashes), ChunkHashes: hashes,
		})
	}
	var removed []string
	for _, path := range before.paths() {
		if _, ok := after[path]; !ok {
			removed = append(removed, path)
		}
	}
	// Changing or removing a tracked binary file needs its lock; locks on
	// paths that turn out not to need one are harmless.
	locked := append([]string(nil), removed...)
	for _, f := range files {
		if _, tracked := before[f.Path]; tracked {
			locked = append(locked, f.Path)
		}
	}
	for _, path := range locked {
		if old := before[path]; old.binary || after[path].binary {
			_, _ = c.LockFile(ctx, e2eOrgSlug, e2eProjectSlug, path, "main", 0)
		}
	}
	_, _, err := c.UploadChunks(ctx, domain.ChunkScope{Org: e2eOrgSlug, Project: e2eProjectSlug}, chunks)
	require.NoError(t, err)
	res, err := c.Push(ctx, e2eOrgSlug, e2eProjectSlug, "main", "", "delta property", files, removed, "", baseCommit)
	require.NoError(t, err)
	return res.CommitID.Base36()
}

// deltaFilter is one set of read rules for the restricted user; admin runs the
// view as the super admin instead, whose filter allows everything.
type deltaFilter struct {
	name     string
	admin    bool
	rules    []string
	defaults map[string]serverDomain.Permission
}

var deltaFilters = []deltaFilter{
	{name: "allow all (admin)", admin: true},
	{name: "allow all (rule)", rules: []string{""}},
	{name: "deny files", defaults: map[string]serverDomain.Permission{
		"": serverDomain.PermissionRead, "f2.txt": 0, "a/f1.txt": 0, "a/b/x.cfg": 0, "b/tool.sh": 0, "c/d/blob.dat": 0, ".nipaignore": 0,
	}},
	{name: "deny subtrees", defaults: map[string]serverDomain.Permission{
		"": serverDomain.PermissionRead, "a/b": 0, "c": 0, "b/a/d": 0,
	}},
	{name: "allow one subtree", rules: []string{"a"}},
	{name: "allow nested subtrees with a hole", rules: []string{"b/c", "d"}, defaults: map[string]serverDomain.Permission{
		"a": serverDomain.PermissionRead, "a/a": 0, "a/c/f1.txt": 0, "README.md": serverDomain.PermissionRead,
	}},
}

var deltaSparseSets = [][]string{
	nil,
	{"a"},
	{"a/b"},
	{"b", "b/c"},
	{"a/b", "c", "d/f1.txt", "b/a/x.cfg"},
}

type deltaHarness struct {
	t      *testing.T
	ctx    context.Context
	db     *sql.DB
	uc     serverapp.Usecases
	host   string
	admin  *clientgrpc.Client
	user   *clientgrpc.Client
	filter string
	// The token stores of the two sessions, for building client usecases.
	adminStore, userStore *memoryStore
}

func newDeltaHarness(t *testing.T) *deltaHarness {
	t.Helper()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host, uc := startTestServerWithUsecases(t, dbConn)
	admin, adminStore := loginE2EClient(t, ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)

	passwordHasher := hasher.NewHasher(1)
	t.Cleanup(passwordHasher.Close)
	passwordHash, err := passwordHasher.Hash("regular-pass")
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password, is_admin) VALUES (?, ?, ?, ?, 0)`,
		deltaUserID, "Delta Reader", "delta@example.com", passwordHash,
	)
	require.NoError(t, err)
	h := &deltaHarness{t: t, ctx: ctx, db: dbConn, uc: uc, host: host, admin: admin, adminStore: adminStore}
	// The user needs a rule to log in to anything; setFilter replaces it.
	h.setFilter(deltaFilter{name: "bootstrap", rules: []string{""}})
	h.user, h.userStore = loginE2EClient(t, ctx, host, "delta@example.com", "regular-pass")
	return h
}

// setFilter replaces the restricted user's rules and the project's path
// defaults, and drops the server's cached rule sets.
func (h *deltaHarness) setFilter(f deltaFilter) {
	h.t.Helper()
	_, err := h.db.ExecContext(h.ctx, `DELETE FROM pbac_rules WHERE user_id = ?`, deltaUserID)
	require.NoError(h.t, err)
	_, err = h.db.ExecContext(h.ctx, `DELETE FROM project_paths_permissions WHERE project_id = 1`)
	require.NoError(h.t, err)
	repo := sqlite.NewPBACRepository(h.db)
	projectID, userID := snow.ID(1), snow.ID(deltaUserID)
	for _, prefix := range f.rules {
		_, err := repo.CreateRule(h.ctx, serverDomain.PBACRule{
			UserID: &userID, OrgID: 1, ProjectID: &projectID, PathPrefix: prefix, Permission: serverDomain.PermissionRead,
		})
		require.NoError(h.t, err)
	}
	for prefix, permission := range f.defaults {
		_, err := repo.UpsertPathPermission(h.ctx, serverDomain.ProjectPathPermission{ProjectID: projectID, PathPrefix: prefix, Permission: permission})
		require.NoError(h.t, err)
	}
	h.uc.Permission.InvalidateAll()
	h.filter = f.name
}

func (h *deltaHarness) client(f deltaFilter) *clientgrpc.Client {
	if f.admin {
		return h.admin
	}
	return h.user
}

func (h *deltaHarness) full(c *clientgrpc.Client, commit string, sparse []string) (*treedelta.Manifest, error) {
	return c.GetTreeManifestDelta(h.ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ManifestTarget{CommitID: commit}, sparse, treedelta.Base{})
}

func (h *deltaHarness) delta(c *clientgrpc.Client, target string, sparse []string, base treedelta.Base) (*treedelta.Manifest, error) {
	return c.GetTreeManifestDelta(h.ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ManifestTarget{CommitID: target}, sparse, base)
}

// deltaStats counts what a delta listed, to show the encoding is exercised.
type deltaStats struct {
	unchangedDirs, changedDirs, unchangedFiles, changedFiles int
}

func (s *deltaStats) add(dir *treedelta.Dir) {
	if dir.Unchanged {
		s.unchangedDirs++
		return
	}
	s.changedDirs++
	for _, f := range dir.Files {
		if f.Unchanged() {
			s.unchangedFiles++
		} else {
			s.changedFiles++
		}
	}
	for _, sub := range dir.Dirs {
		s.add(sub)
	}
}

var deltaPropertyCases atomic.Int64

// TestEndToEnd_ManifestDeltaProperty checks, over random histories, views and
// commit pairs, that applying the delta the server sends for BASE -> TARGET to
// the full manifest of BASE gives exactly the full manifest of TARGET: same
// files in the same order with the same chunk lists, same directories with
// the same rehashed hashes. Everything goes through the real server, the gRPC
// encoding and the client decoder.
func TestEndToEnd_ManifestDeltaProperty(t *testing.T) {
	t.Parallel()
	seeds := deltaSeeds
	if testing.Short() {
		seeds = seeds[:2]
	}
	t.Cleanup(func() { t.Logf("delta property: %d cases over %d seeds", deltaPropertyCases.Load(), len(seeds)) })
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			runDeltaProperty(t, seed)
		})
	}
}

func runDeltaProperty(t *testing.T, seed int64) {
	h := newDeltaHarness(t)
	gen := &deltaGen{rng: rand.New(rand.NewSource(seed))}

	const commitCount = 10
	model := deltaModel{}
	var commits []string
	for len(commits) < commitCount {
		next := model.clone()
		switch {
		case len(commits) == 0:
			// A first tree with enough in it for the views to differ.
			for i := 0; i < 14; i++ {
				next.set(gen.filePath(), gen.file())
			}
			next.set(".nipaignore", deltaFile{content: "*.tmp\n", mode: 0o644})
			// What the sparse sets name exists at least at first.
			for _, path := range []string{"a/b/f1.txt", "b/c/x.cfg", "b/a/x.cfg", "c/f2.txt", "d/f1.txt"} {
				next.set(path, gen.file())
			}
		case len(commits) == 4 && seed%2 == 1:
			// An empty tree in the middle of the history.
			next = deltaModel{}
		default:
			gen.mutate(next)
		}
		if len(next) == len(model) && func() bool {
			for path, f := range next {
				if model[path] != f {
					return false
				}
			}
			return true
		}() {
			continue
		}
		base := ""
		if len(commits) > 0 {
			base = commits[len(commits)-1]
		}
		commits = append(commits, deltaPush(t, h.ctx, h.admin, model, next, base))
		model = next
	}

	var stats deltaStats
	cases, provable, views := 0, 0, map[string]string{}
	for _, filter := range deltaFilters {
		if !filter.admin {
			h.setFilter(filter)
		}
		c := h.client(filter)
		for _, sparse := range deltaSparseSets {
			view := fmt.Sprintf("seed %d, filter %q, sparse %v", seed, filter.name, sparse)
			fulls := make([]*treedelta.Manifest, len(commits))
			errs := make([]error, len(commits))
			for i, commit := range commits {
				fulls[i], errs[i] = h.full(c, commit, sparse)
				if errs[i] != nil {
					continue
				}
				require.NotNil(t, fulls[i].Full, view)
				require.Nil(t, fulls[i].Delta, view)
				require.Len(t, fulls[i].View, 32, view)
				legacy, err := c.GetCommitTreeManifest(h.ctx, e2eOrgSlug, e2eProjectSlug, commit, sparse)
				require.NoError(t, err, view)
				require.Equal(t, legacy, fulls[i].Full, "%s: the manifest a delta-aware call returns is the plain one", view)
			}
			for i := range commits {
				if errs[i] != nil {
					continue
				}
				// One token per view, whatever the commit.
				if known, ok := views[view]; ok {
					require.Equal(t, known, string(fulls[i].View), view)
				}
				views[view] = string(fulls[i].View)
				for j := range commits {
					label := fmt.Sprintf("%s, base #%d %s -> target #%d %s", view, i, commits[i], j, commits[j])
					base := treedelta.Base{CommitID: commits[i], View: fulls[i].View}
					// Half of the requests name the base root hash, which is
					// what lets the server vouch for the result's.
					named := (i+j)%2 == 0
					if named {
						hash := fulls[i].Full.Hash
						base.TreeHash = &hash
					}
					got, err := h.delta(c, commits[j], sparse, base)
					if errs[j] != nil {
						require.Error(t, err, label)
						require.Equal(t, clientErrorCode(t, errs[j]), clientErrorCode(t, err), label)
						continue
					}
					require.NoError(t, err, label)
					require.NotNil(t, got.Delta, "%s: the server declined", label)
					require.Nil(t, got.Full, label)
					require.Equal(t, commits[j], got.CommitID, label)
					require.Equal(t, fulls[i].View, got.View, label)
					applied, err := treedelta.Apply(fulls[i].Full, got.Delta)
					require.NoError(t, err, label)
					require.Equal(t, fulls[j].Full, applied, label)
					if got.Delta.RootHash != nil {
						require.True(t, named, label)
						require.Equal(t, fulls[j].Full.Hash, *got.Delta.RootHash, label)
						provable++
					} else if named && (filter.admin || len(filter.defaults) == 0 && len(filter.rules) == 1 && filter.rules[0] == "") && sparse == nil {
						// Trees written by pushes hash soundly, so an
						// unfiltered view always gets its root hash.
						require.Fail(t, "no root hash for an unfiltered view", label)
					}
					if i == j {
						require.True(t, got.Delta.Root.Unchanged, label)
					}
					stats.add(got.Delta.Root)
					cases++
				}
			}
		}
	}
	// Every view has its own token.
	distinct := map[string]bool{}
	for _, token := range views {
		distinct[token] = true
	}
	require.Len(t, distinct, len(views))
	require.Greater(t, len(views), len(deltaFilters)*len(deltaSparseSets)/2, "most views must be readable for some commit")

	t.Logf("seed %d: %d views, %d cases, %d with a proven root hash; deltas listed %d unchanged and %d changed directories, %d unchanged and %d changed files",
		seed, len(views), cases, provable, stats.unchangedDirs, stats.changedDirs, stats.unchangedFiles, stats.changedFiles)
	require.Greater(t, cases, 500)
	require.Positive(t, provable)
	require.Positive(t, stats.unchangedDirs)
	require.Positive(t, stats.unchangedFiles)
	require.Positive(t, stats.changedFiles)
	deltaPropertyCases.Add(int64(cases))

	// The branch form leads to the head and says so.
	h.setFilter(deltaFilters[1])
	head := len(commits) - 1
	first, err := h.full(h.user, commits[0], nil)
	require.NoError(t, err)
	want, err := h.full(h.user, commits[head], nil)
	require.NoError(t, err)
	got, err := h.user.GetTreeManifestDelta(h.ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ManifestTarget{Branch: "main"}, nil,
		treedelta.Base{CommitID: commits[0], View: first.View})
	require.NoError(t, err)
	require.NotNil(t, got.Delta)
	require.Equal(t, commits[head], got.CommitID)
	applied, err := treedelta.Apply(first.Full, got.Delta)
	require.NoError(t, err)
	require.Equal(t, want.Full, applied)
}

// TestEndToEnd_ManifestDeltaDeclines checks that a delta request the server
// cannot honour is answered with the plain full manifest, never an error of
// its own, and that the view token only depends on the rules.
func TestEndToEnd_ManifestDeltaDeclines(t *testing.T) {
	t.Parallel()
	h := newDeltaHarness(t)
	ctx := h.ctx
	v1 := deltaModel{
		"README.md": {content: "one\n", mode: 0o644}, "a/f1.txt": {content: "a1\n", mode: 0o644},
		"a/b/x.cfg": {content: "x\n", mode: 0o755}, "c/f2.txt": {content: "c2\n", mode: 0o444},
	}
	v2 := v1.clone()
	v2["a/f1.txt"] = deltaFile{content: "a1 changed\n", mode: 0o644}
	v2["c/new.txt"] = deltaFile{content: "new\n", mode: 0o644}
	base := deltaPush(t, ctx, h.admin, deltaModel{}, v1, "")
	target := deltaPush(t, ctx, h.admin, v1, v2, base)

	allowAll := deltaFilter{name: "allow all", rules: []string{""}}
	h.setFilter(allowAll)
	held, err := h.full(h.user, base, nil)
	require.NoError(t, err)
	good := treedelta.Base{CommitID: base, View: held.View, TreeHash: &held.Full.Hash}

	// The control: this base in this view gets a delta.
	got, err := h.delta(h.user, target, nil, good)
	require.NoError(t, err)
	require.NotNil(t, got.Delta)
	require.NotNil(t, got.Delta.RootHash)
	var stats deltaStats
	stats.add(got.Delta.Root)
	require.Equal(t, deltaStats{unchangedDirs: 1, changedDirs: 3, unchangedFiles: 2, changedFiles: 2}, stats,
		"root and a and c are listed, a/b is not read; README.md and c/f2.txt are named, a/f1.txt and c/new.txt sent")

	// requirePlain asks for a delta and expects exactly what a client that
	// knows nothing about deltas gets for the same target and paths.
	requirePlain := func(name string, c *clientgrpc.Client, sparse []string, base treedelta.Base) *treedelta.Manifest {
		t.Helper()
		want, err := c.GetCommitTreeManifest(ctx, e2eOrgSlug, e2eProjectSlug, target, sparse)
		require.NoError(t, err, name)
		got, err := h.delta(c, target, sparse, base)
		require.NoError(t, err, name)
		require.Nil(t, got.Delta, name)
		require.Empty(t, got.CommitID, name)
		require.Equal(t, want, got.Full, name)
		require.Len(t, got.View, 32, name)
		return got
	}

	requirePlain("unknown base commit", h.user, nil, treedelta.Base{CommitID: snow.ID(987654321).Base36(), View: held.View})
	requirePlain("malformed base commit id", h.user, nil, treedelta.Base{CommitID: "!!", View: held.View})
	requirePlain("no view token", h.user, nil, treedelta.Base{CommitID: base})
	requirePlain("someone else's view token", h.user, nil, treedelta.Base{CommitID: base, View: []byte("not a token")})

	_, err = h.db.ExecContext(ctx, `INSERT INTO projects (id, org_id, slug, name) VALUES (2, 1, 'other', 'Other')`)
	require.NoError(t, err)
	foreign := snow.ID(424242)
	_, err = h.db.ExecContext(ctx,
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) SELECT ?, hash, 2, tree_id, user_id, 'foreign' FROM commits WHERE id = ?`,
		foreign.Int64(), mustParseBase36(t, base).Int64())
	require.NoError(t, err)
	requirePlain("base of another project", h.user, nil, treedelta.Base{CommitID: foreign.Base36(), View: held.View})

	// The sparse set changed: the token of the whole tree is not the token
	// of a part of it, and the other way round.
	sparse := requirePlain("sparse set changed", h.user, []string{"a"}, good)
	require.NotEqual(t, held.View, sparse.View)
	requirePlain("sparse set dropped", h.user, nil, treedelta.Base{CommitID: base, View: sparse.View})
	narrowed, err := h.delta(h.user, target, []string{"a"}, treedelta.Base{CommitID: base, View: sparse.View})
	require.NoError(t, err)
	require.NotNil(t, narrowed.Delta, "the same sparse set again gets a delta")

	// The read rules changed since the base was fetched.
	h.setFilter(deltaFilter{name: "deny c", defaults: map[string]serverDomain.Permission{"": serverDomain.PermissionRead, "c": 0}})
	changed := requirePlain("filter changed", h.user, nil, good)
	require.NotEqual(t, held.View, changed.View)
	require.Len(t, changed.Full.TreeChildren, 1, "c is hidden now")

	// Without any read access both requests fail the same way.
	h.setFilter(deltaFilter{name: "no access"})
	_, plainErr := h.user.GetCommitTreeManifest(ctx, e2eOrgSlug, e2eProjectSlug, target, nil)
	require.Error(t, plainErr)
	_, err = h.delta(h.user, target, nil, good)
	require.Error(t, err)
	require.Equal(t, clientErrorCode(t, plainErr), clientErrorCode(t, err))
	require.Equal(t, plainErr.Error(), err.Error())

	// The old rules again: the old token is good again, on this server and
	// on another instance over the same database.
	h.setFilter(allowAll)
	again, err := h.delta(h.user, target, nil, good)
	require.NoError(t, err)
	require.NotNil(t, again.Delta)
	require.Equal(t, held.View, again.View)
	otherHost, _ := startTestServerWithUsecases(t, h.db)
	otherClient, _ := loginE2EClient(t, ctx, otherHost, "delta@example.com", "regular-pass")
	elsewhere, err := otherClient.GetTreeManifestDelta(ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ManifestTarget{CommitID: target}, nil, good)
	require.NoError(t, err)
	require.NotNil(t, elsewhere.Delta)
	require.Equal(t, held.View, elsewhere.View)

	// The admin sees the same paths through another kind of filter; its
	// token is its own.
	adminFull, err := h.full(h.admin, base, nil)
	require.NoError(t, err)
	require.Equal(t, held.Full, adminFull.Full)
	require.NotEqual(t, held.View, adminFull.View)
	requirePlain("the token of another kind of filter", h.admin, nil, good)

	// A branch without commits has no tree, with or without a base.
	_, err = h.admin.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "side", "main", "", "")
	require.NoError(t, err)
	_, err = h.db.ExecContext(ctx, `UPDATE branches SET commit_id = NULL WHERE name = 'side'`)
	require.NoError(t, err)
	bare, err := h.admin.GetTreeManifestDelta(ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ManifestTarget{Branch: "side"}, nil,
		treedelta.Base{CommitID: base, View: adminFull.View})
	require.NoError(t, err)
	require.Nil(t, bare.Full)
	require.Nil(t, bare.Delta)
}

func mustParseBase36(t *testing.T, id string) snow.ID {
	t.Helper()
	parsed, err := snow.ParseBase36(id)
	require.NoError(t, err)
	return parsed
}

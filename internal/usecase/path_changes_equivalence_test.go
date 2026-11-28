package usecase_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db/dbtest"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// historyFile is one file of the generator's model of a branch.
type historyFile struct {
	content string
	mode    int
}

// noChunkReads is the chunk reader of the merge tests: the generated merges
// never change a file on both sides, so no text merge reads content.
type noChunkReads struct{}

func (noChunkReads) Get(context.Context, snow.ID, domain.Hash) ([]byte, error) {
	return nil, errors.New("unexpected chunk read")
}

type noChunkUploads struct{}

func (noChunkUploads) StoreChunks(context.Context, snow.ID, []usecase.ChunkContent) ([]usecase.ChunkRow, error) {
	return nil, errors.New("unexpected chunk upload")
}

func (noChunkUploads) RecordStoredChunks(context.Context, snow.ID, []usecase.ChunkRow) error {
	return nil
}

// historyFixture builds a random history of project 1 through the real push
// and merge usecases on SQLite, so every commit writer stores its own path
// change rows.
type historyFixture struct {
	t        *testing.T
	ctx      context.Context
	db       *sql.DB
	rnd      *rand.Rand
	push     *usecase.Push
	writer   *usecase.Branch
	repo     *sqlite.BranchRepository
	pushRepo *sqlite.PushRepository
	models   map[string]map[string]historyFile
	past     map[string][]string
	branchID int64
	step     int
	// pushes counts the pushes per branch; landed counts the lands per
	// strategy ("rebase+" is a rebase replaying two commits or more).
	pushes map[string]int
	landed map[string]int
}

func newHistoryFixture(t *testing.T, seed int64) *historyFixture {
	t.Helper()
	db := dbtest.SQLite(t)
	repo := sqlite.NewBranchRepository(db)
	pushRepo := sqlite.NewPushRepository(db)
	admin := &stubPermission{filter: usecase.AllowAllFilter()}
	node := mustSnowNode(t)
	writer := usecase.NewBranchWithChunks(admin, repo, node, noChunkReads{}).
		WithMergeCommitter(pushRepo).WithChunkUploader(noChunkUploads{})
	return &historyFixture{
		t:        t,
		ctx:      domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1, IsSuperAdmin: true, IsAdmin: true}),
		db:       db,
		rnd:      rand.New(rand.NewSource(seed)),
		push:     usecase.NewPush(admin, repo, pushRepo, node),
		writer:   writer,
		repo:     repo,
		pushRepo: pushRepo,
		models:   map[string]map[string]historyFile{"main": {}},
		past:     map[string][]string{},
		branchID: 100,
		pushes:   map[string]int{},
		landed:   map[string]int{},
	}
}

func (f *historyFixture) head(branch string) *domain.Branch {
	f.t.Helper()
	b, err := f.repo.GetBranchByName(f.ctx, 1, branch)
	require.NoError(f.t, err)
	return b
}

func (f *historyFixture) newBranch(name, from string) {
	f.t.Helper()
	f.branchID++
	head := f.head(from)
	_, err := f.db.Exec(`INSERT INTO branches (id, project_id, name, key, commit_id) VALUES (?, 1, ?, ?, ?)`,
		f.branchID, name, name, head.CommitID.Int64())
	require.NoError(f.t, err)
	f.models[name] = cloneModel(f.models[from])
}

func cloneModel(m map[string]historyFile) map[string]historyFile {
	out := make(map[string]historyFile, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// pushChange pushes files (path -> new state) and removed paths onto branch
// and applies them to the branch's model; parent2 makes it a merge commit.
func (f *historyFixture) pushChange(branch string, files map[string]historyFile, removed []string, parent2 string) {
	f.t.Helper()
	f.step++
	var push []*domain.PushFile
	var refs []usecase.ChunkRef
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		file := files[p]
		chunk := domain.Hash(sha256.Sum256([]byte(file.content)))
		refs = append(refs, usecase.ChunkRef{Hash: chunk, SizeBytes: int64(len(file.content))})
		push = append(push, &domain.PushFile{
			Path: p, Mode: file.mode, SizeBytes: int64(len(file.content)),
			FileHash: treehash.FileHash([]domain.Hash{chunk}), ChunkHashes: []domain.Hash{chunk},
		})
	}
	require.NoError(f.t, f.pushRepo.InsertChunksIfNotExist(f.ctx, 1, refs))
	base := ""
	if head := f.head(branch); head.CommitID != nil {
		base = head.CommitID.Base36()
	}
	_, err := f.push.Push(f.ctx, 1, branch, "", fmt.Sprintf("step %d", f.step), push, removed, parent2, base)
	require.NoError(f.t, err, "push %v removed %v", paths, removed)
	f.pushes[branch]++
	model := f.models[branch]
	for _, r := range removed {
		delete(model, r)
	}
	for p, file := range files {
		model[p] = file
		f.past[p] = append(f.past[p], file.content)
	}
}

// randomChange edits the model of branch within the given path universe: a
// modification, an addition (which removes whatever clashes with it, so a
// file and a folder swap), a deletion, a folder deletion, a rename, a mode
// flip, a revert to an earlier content or a push of identical content.
func (f *historyFixture) randomChange(branch string, universe []string) {
	model := f.models[branch]
	files := map[string]historyFile{}
	removedSet := map[string]bool{}
	existing := func() []string {
		var out []string
		for p := range model {
			if slices.Contains(universe, p) || underAny(p, universe) {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out
	}
	add := func(p string, file historyFile) {
		for q := range model {
			if strings.HasPrefix(q, p+"/") || strings.HasPrefix(p, q+"/") {
				removedSet[q] = true
				delete(files, q)
			}
		}
		for q := range files {
			if strings.HasPrefix(q, p+"/") || strings.HasPrefix(p, q+"/") {
				delete(files, q)
			}
		}
		delete(removedSet, p)
		files[p] = file
	}
	ops := 1 + f.rnd.Intn(3)
	for i := 0; i < ops; i++ {
		live := existing()
		switch op := f.rnd.Intn(9); {
		case op <= 1 || len(live) == 0:
			p := universe[f.rnd.Intn(len(universe))]
			add(p, historyFile{content: fmt.Sprintf("v%d %s", f.step*10+i, p), mode: 0o644})
		case op == 2:
			p := live[f.rnd.Intn(len(live))]
			add(p, historyFile{content: fmt.Sprintf("v%d %s", f.step*10+i, p), mode: model[p].mode})
		case op == 3:
			p := live[f.rnd.Intn(len(live))]
			if _, ok := files[p]; !ok {
				removedSet[p] = true
			}
		case op == 4:
			dir := dirOfPath(live[f.rnd.Intn(len(live))])
			for _, p := range live {
				if dir != "" && strings.HasPrefix(p, dir+"/") {
					if _, ok := files[p]; !ok {
						removedSet[p] = true
					}
				}
			}
		case op == 5:
			from := live[f.rnd.Intn(len(live))]
			to := universe[f.rnd.Intn(len(universe))]
			if to != from && !strings.HasPrefix(to, from+"/") && !strings.HasPrefix(from, to+"/") {
				if _, ok := files[from]; !ok {
					removedSet[from] = true
					add(to, model[from])
				}
			}
		case op == 6:
			p := live[f.rnd.Intn(len(live))]
			file := model[p]
			if file.mode == 0o755 {
				file.mode = 0o644
			} else {
				file.mode = 0o755
			}
			add(p, file)
		case op == 7:
			p := live[f.rnd.Intn(len(live))]
			if past := f.past[p]; len(past) > 0 {
				add(p, historyFile{content: past[f.rnd.Intn(len(past))], mode: model[p].mode})
			}
		default:
			p := live[f.rnd.Intn(len(live))]
			add(p, model[p])
		}
	}
	var removed []string
	for p := range removedSet {
		if _, ok := model[p]; ok {
			if _, pushed := files[p]; !pushed {
				removed = append(removed, p)
			}
		}
	}
	sort.Strings(removed)
	if len(files) == 0 && len(removed) == 0 {
		p := universe[f.rnd.Intn(len(universe))]
		add(p, historyFile{content: fmt.Sprintf("v%d %s", f.step*10+7, p), mode: 0o644})
		removed = removed[:0]
		for p := range removedSet {
			if _, ok := model[p]; ok {
				removed = append(removed, p)
			}
		}
		sort.Strings(removed)
	}
	f.pushChange(branch, files, removed, "")
}

func underAny(p string, universe []string) bool {
	for _, u := range universe {
		if strings.HasPrefix(p, u+"/") {
			return true
		}
	}
	return false
}

func dirOfPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// The two path sets a side branch and main edit while the side branch is
// open never name the same file and never clash as file and folder, so a
// merge of the two never needs a text merge or reports a conflict. Each holds
// a file/folder swap pair.
var (
	mainPaths = []string{"a/f1", "a/f2", "a/b/f3", "a/b/c/f4", "d", "d/f5", "top"}
	sidePaths = []string{"s/f6", "s/t/f7", "x/pub", "x/pub/p1", "x/sec/s1", "x/sec/deep/s2", "x/f8"}
)

// build generates the history: pushes on main, side branches with their own
// pushes, and lands of a side branch on main by every server strategy or by
// a client-side merge commit push.
func (f *historyFixture) build(steps int) {
	f.pushChange("main", map[string]historyFile{"a/f1": {"seed a", 0o644}, "top": {"seed top", 0o644}, "x/sec/s1": {"seed s", 0o644}}, nil, "")
	all := append(slices.Clone(mainPaths), sidePaths...)
	side := ""
	sides := 0
	for i := 0; i < steps; i++ {
		switch r := f.rnd.Intn(10); {
		case side == "" && r < 2:
			sides++
			side = fmt.Sprintf("side-%d", sides)
			f.newBranch(side, "main")
		case side != "" && r < 4:
			f.randomChange(side, sidePaths)
		case side != "" && r < 5:
			f.land(side)
			side = ""
		case side != "":
			f.randomChange("main", mainPaths)
		default:
			f.randomChange("main", all)
		}
	}
	if side != "" {
		f.randomChange(side, sidePaths)
		f.land(side)
	}
}

// land merges side into main with a random strategy.
func (f *historyFixture) land(side string) {
	f.t.Helper()
	mainHead, sideHead := f.head("main"), f.head(side)
	if *mainHead.CommitID == *sideHead.CommitID {
		return
	}
	strategies := []string{domain.MergeStrategyMerge, domain.MergeStrategySquash, domain.MergeStrategyRebase, "client"}
	ancestorOfSide := false
	log, err := f.repo.CommitLog(f.ctx, 1, *sideHead.CommitID, 1000)
	require.NoError(f.t, err)
	for _, entry := range log {
		if entry.ID == *mainHead.CommitID {
			ancestorOfSide = true
		}
	}
	if ancestorOfSide {
		strategies = append(strategies, domain.MergeStrategyFastForward)
	}
	strategy := strategies[f.rnd.Intn(len(strategies))]
	merged := cloneModel(f.models["main"])
	for p := range merged {
		if slices.Contains(sidePaths, p) || underAny(p, sidePaths) {
			delete(merged, p)
		}
	}
	for p, file := range f.models[side] {
		if slices.Contains(sidePaths, p) || underAny(p, sidePaths) {
			merged[p] = file
		}
	}
	if strategy == "client" {
		files := map[string]historyFile{}
		var removed []string
		for p, file := range merged {
			if old, ok := f.models["main"][p]; !ok || old != file {
				files[p] = file
			}
		}
		for p := range f.models["main"] {
			if _, ok := merged[p]; !ok {
				removed = append(removed, p)
			}
		}
		sort.Strings(removed)
		if len(files) == 0 && len(removed) == 0 {
			return
		}
		commit, err := f.repo.GetCommit(f.ctx, *sideHead.CommitID)
		require.NoError(f.t, err)
		f.pushChange("main", files, removed, commit.Hash.String())
		f.landed["client"]++
		return
	}
	_, err = f.writer.MergeForMergeRequest(f.ctx, 1, "main", side, usecase.MergeCommitOptions{Strategy: strategy, Author: 1})
	if err != nil && domain.IsErrorConflict(err) && strings.Contains(err.Error(), "already up to date") {
		return
	}
	require.NoError(f.t, err, "merge %s with %s", side, strategy)
	f.models["main"] = merged
	f.landed[strategy]++
	if strategy == domain.MergeStrategyRebase && f.pushes[side] >= 2 {
		f.landed["rebase+"]++
	}
}

// queryPaths lists every path the history ever held, every folder above one,
// the whole tree and a path that never existed.
func (f *historyFixture) queryPaths() []string {
	set := map[string]bool{"": true, "never/there": true}
	for p := range f.past {
		for q := p; q != ""; q = dirOfPath(q) {
			set[q] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func entryIDs(entries []*domain.CommitLogEntry) []snow.ID {
	out := make([]snow.ID, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// requireSameHistory compares the indexed history with the scan for every
// path, several starts and limits, as reader sees it.
func (f *historyFixture) requireSameHistory(reader *usecase.PathFilter, label string) int {
	f.t.Helper()
	perm := &stubPermission{filter: reader}
	uc := usecase.NewBranch(perm, f.repo, mustSnowNode(f.t))
	head := f.head("main")
	chain, err := f.repo.CommitLog(f.ctx, 1, *head.CommitID, 1000)
	require.NoError(f.t, err)
	starts := []*snow.ID{nil}
	for _, i := range []int{len(chain) / 2} {
		if i > 0 && i < len(chain) {
			id := chain[i].ID
			starts = append(starts, &id)
		}
	}
	nonEmpty := 0
	for _, path := range f.queryPaths() {
		for _, start := range starts {
			for _, limit := range []int{1, 0} {
				want, wantErr := uc.PathCommitLogScan(f.ctx, 1, "main", path, start, limit, 500)
				got, gotErr := uc.PathCommitLog(f.ctx, 1, "main", path, start, limit)
				if wantErr != nil {
					require.Error(f.t, gotErr, "%s: %q", label, path)
					require.Equal(f.t, domain.IsErrorNotFound(wantErr), domain.IsErrorNotFound(gotErr), "%s: %q", label, path)
					continue
				}
				require.NoError(f.t, gotErr, "%s: %q", label, path)
				require.Equal(f.t, entryIDs(want), entryIDs(got), "%s: path %q start %v limit %d", label, path, start, limit)
				if len(got) > 1 {
					nonEmpty++
				}
			}
		}
	}
	return nonEmpty
}

type storedPathChange struct {
	commit int64
	path   string
	kind   int64
}

func (f *historyFixture) storedPathChanges() []storedPathChange {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT commit_id, path, kind FROM commit_path_changes ORDER BY commit_id, path, kind`)
	require.NoError(f.t, err)
	defer rows.Close()
	var out []storedPathChange
	for rows.Next() {
		var row storedPathChange
		require.NoError(f.t, rows.Scan(&row.commit, &row.path, &row.kind))
		out = append(out, row)
	}
	require.NoError(f.t, rows.Err())
	return out
}

func (f *historyFixture) dropPathIndex() {
	f.t.Helper()
	_, err := f.db.Exec(`DELETE FROM commit_path_changes WHERE commit_id > 0`)
	require.NoError(f.t, err)
	_, err = f.db.Exec(`DELETE FROM commit_path_index WHERE commit_id > 0`)
	require.NoError(f.t, err)
}

// restrictedReader may read below a and x/pub only: top, d, s and x/sec are
// hidden, x can be descended into for x/pub.
func restrictedReader() *usecase.PathFilter {
	return usecase.NewTestPathFilter([]*domain.PBACRule{
		{PathPrefix: "a", Permission: domain.PermissionRead},
		{PathPrefix: "x/pub", Permission: domain.PermissionRead},
	}, nil)
}

// defaultsReader reads everything by the root path default except x/sec,
// which a deeper default withholds.
func defaultsReader() *usecase.PathFilter {
	return usecase.NewTestPathFilter(nil, []*domain.ProjectPathPermission{
		{PathPrefix: "", Permission: domain.PermissionRead},
		{PathPrefix: "x/sec", Permission: 0},
		{PathPrefix: "a/b", Permission: 0},
	})
}

// TestPathCommitLog_MatchesScan property-tests the path change index against
// the tree-comparing scan it replaced: random histories of pushes (edits,
// additions, deletions, folder deletions, renames, file/folder swaps, mode
// flips, reverts, identical re-pushes), side branches landed by merge, squash,
// rebase, fast-forward or a pushed merge commit, queried for every path,
// several starts and limits, by an admin and by readers whose rules hide
// paths. It then drops the index so every commit is indexed again by the
// first read and checks that the rows the writers stored equal the rows the
// read computes from the trees.
func TestPathCommitLog_MatchesScan(t *testing.T) {
	steps := 60
	seeds := []int64{1, 2, 3, 4}
	all := !testing.Short() && !raceEnabled
	if !all {
		seeds = seeds[:2]
	}
	landed := map[string]int{}
	kinds := map[int64]bool{}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			f := newHistoryFixture(t, seed)
			f.build(steps)
			for strategy, n := range f.landed {
				landed[strategy] += n
			}

			require.Positive(t, f.requireSameHistory(usecase.AllowAllFilter(), "admin"))
			f.requireSameHistory(restrictedReader(), "restricted")
			f.requireSameHistory(defaultsReader(), "defaults")

			written := f.storedPathChanges()
			require.NotEmpty(t, written)
			for _, row := range written {
				kinds[row.kind] = true
			}
			f.dropPathIndex()
			f.requireSameHistory(restrictedReader(), "restricted, re-indexed")
			// Side branches hold commits main's first-parent walk never
			// crosses (a merge commit's second parent); reading their history
			// indexes them too.
			reader := usecase.NewBranch(&stubPermission{filter: usecase.AllowAllFilter()}, f.repo, mustSnowNode(t))
			for branch := range f.models {
				_, err := reader.PathCommitLog(f.ctx, 1, branch, "", nil, 1)
				require.NoError(t, err)
			}
			require.Equal(t, written, f.storedPathChanges(), "rows the writers stored must equal the rows a read computes")
			f.requireSameHistory(usecase.AllowAllFilter(), "admin, re-indexed")
		})
	}
	t.Logf("lands by strategy: %v", landed)
	if all {
		// The seeds are fixed: every writer and every kind of row is covered.
		for _, strategy := range []string{"client", domain.MergeStrategyMerge, domain.MergeStrategySquash, domain.MergeStrategyRebase, "rebase+", domain.MergeStrategyFastForward} {
			require.Positive(t, landed[strategy], "no land by %s", strategy)
		}
		require.Equal(t, map[int64]bool{1: true, 2: true, 3: true}, kinds)
	}
}

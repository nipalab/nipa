package e2e

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/treedelta"
)

// deltaCountingClient is the real gRPC client, counting the manifest requests
// that named a base and the deltas the server answered them with.
type deltaCountingClient struct {
	*clientgrpc.Client
	requests, based, deltas int
}

func (c *deltaCountingClient) GetTreeManifestDelta(ctx context.Context, org, project string, target treedelta.Target, paths []string, base treedelta.Base) (*treedelta.Manifest, error) {
	c.requests++
	if base.CommitID != "" {
		c.based++
	}
	manifest, err := c.Client.GetTreeManifestDelta(ctx, org, project, target, paths, base)
	if err == nil && manifest != nil && manifest.Delta != nil {
		c.deltas++
	}
	return manifest, err
}

// deltaRecordingRepo is the real local repository, keeping the errors of the
// delta plan and apply: the usecase falls back on them silently, so a test
// that only compares results would pass without any delta being applied.
type deltaRecordingRepo struct {
	*localrepo.LocalRepo
	applied int
	errs    []error
}

func (r *deltaRecordingRepo) PlanTreeDelta(in domain.TreeDeltaApply) (*domain.TreeDeltaChanges, error) {
	changes, err := r.LocalRepo.PlanTreeDelta(in)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("plan %s -> %s: %w", in.BaseCommitID, in.CommitID, err))
	}
	return changes, err
}

func (r *deltaRecordingRepo) ApplyTreeDelta(in domain.TreeDeltaApply, planned *domain.TreeDeltaChanges) (*domain.TreeDeltaChanges, error) {
	changes, err := r.LocalRepo.ApplyTreeDelta(in, planned)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("apply %s -> %s: %w", in.BaseCommitID, in.CommitID, err))
	} else {
		r.applied++
	}
	return changes, err
}

// workingTree lists every file of a working copy with its permission bits
// and a digest of its content.
func workingTree(t *testing.T, target string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(target, p)
		if d.IsDir() {
			if rel == localrepo.ConfigDir {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s|%o|%x", filepath.ToSlash(rel), info.Mode().Perm(), sha256.Sum256(data)))
		return nil
	}))
	sort.Strings(out)
	return out
}

// cloneState is everything a clone holds that must not depend on how it got
// to its commit.
type cloneState struct {
	stored  []string
	working []string
}

func cloneStateOf(t *testing.T, target string) cloneState {
	t.Helper()
	return cloneState{stored: storedTree(t, target), working: workingTree(t, target)}
}

var deltaUpdateSparseSets = [][]string{
	nil,
	{"a"},
	{"a/b", "c", "d/f1.txt", "b/a/x.cfg"},
}

var deltaUpdateMoves atomic.Int64

// TestEndToEnd_DeltaUpdateProperty checks, over random histories and views
// (read rules and sparse sets), that a clone moved from commit to commit by
// update, switch and tag switch, which fetch and apply delta manifests, holds
// after every move exactly what a fresh clone of that commit holds: the same
// stored rows, tree hash and synced-tree record, and the same working files.
// Everything goes through the real server and the real client usecases.
func TestEndToEnd_DeltaUpdateProperty(t *testing.T) {
	t.Parallel()
	seeds := deltaSeeds[:4]
	if testing.Short() {
		seeds = seeds[:1]
	}
	t.Cleanup(func() { t.Logf("delta update property: %d moves over %d seeds", deltaUpdateMoves.Load(), len(seeds)) })
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			runDeltaUpdateProperty(t, seed)
		})
	}
}

// deltaHistory pushes a random history to main and names every commit with a
// branch c<i> and a tag t<i>. The generator must not turn files into
// directories or back (deltaGen.noSwaps): syncing a working copy across such
// a swap fails in the full sync as well ("not a directory" / "file exists"),
// which is not what this test is about. The stored rows handle swaps
// (localrepo TestApplyTreeDelta_EqualsSavingTheManifest), and so do the
// server and treedelta.Apply (TestEndToEnd_ManifestDeltaProperty).
func deltaHistory(t *testing.T, h *deltaHarness, gen *deltaGen, seed int64, count int) []string {
	t.Helper()
	model := deltaModel{}
	var commits []string
	for len(commits) < count {
		next := model.clone()
		switch {
		case len(commits) == 0:
			for i := 0; i < 14; i++ {
				next.set(gen.filePath(), gen.file())
			}
			next.set(".nipaignore", deltaFile{content: "*.tmp\n", mode: 0o644})
			for _, path := range []string{"a/b/f1.txt", "b/c/x.cfg", "b/a/x.cfg", "c/f2.txt", "d/f1.txt"} {
				next.set(path, gen.file())
			}
		case len(commits) == 4 && seed%2 == 1:
			// An empty tree in the middle of the history.
			next = deltaModel{}
		default:
			gen.mutate(next)
		}
		same := len(next) == len(model)
		for path, f := range next {
			same = same && model[path] == f
		}
		if same {
			continue
		}
		base := ""
		if len(commits) > 0 {
			base = commits[len(commits)-1]
		}
		commit := deltaPush(t, h.ctx, h.admin, model, next, base)
		i := len(commits)
		_, err := h.admin.CreateBranch(h.ctx, e2eOrgSlug, e2eProjectSlug, fmt.Sprintf("c%d", i), "", commit, "")
		require.NoError(t, err)
		_, err = h.admin.CreateTag(h.ctx, e2eOrgSlug, e2eProjectSlug, fmt.Sprintf("t%d", i), "", "", commit, "")
		require.NoError(t, err)
		commits = append(commits, commit)
		model = next
	}
	return commits
}

func runDeltaUpdateProperty(t *testing.T, seed int64) {
	h := newDeltaHarness(t)
	gen := &deltaGen{rng: rand.New(rand.NewSource(seed)), noSwaps: true}
	const commitCount, movesPerView = 9, 12
	commits := deltaHistory(t, h, gen, seed, commitCount)
	url := "http://" + h.host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	views, moves, deltas, failed, restored, skipped := 0, 0, 0, 0, 0, 0
	for fi, filter := range deltaFilters {
		if !filter.admin {
			h.setFilter(filter)
		}
		grpcClient, store := h.user, h.userStore
		if filter.admin {
			grpcClient, store = h.admin, h.adminStore
		}
		auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})

		for si, sparse := range deltaUpdateSparseSets {
			view := fmt.Sprintf("seed %d, filter %q, sparse %v", seed, filter.name, sparse)
			// What a fresh clone of each commit holds in this view. A commit
			// whose manifest the view cannot have (a sparse path the commit
			// lacks) has the manifest's error instead, which a move to it
			// must end with too. A commit whose manifest is fine but whose
			// clone fails while downloading is left out as a target
			// (unclonable): nothing can say what a clone of it holds, and
			// whether a move to it fails depends on what is cached. That
			// happens here because the test changes the reader's rules every
			// few seconds while the server keeps its answer to "which chunks
			// may this caller download for this commit" for two minutes
			// (usecase.visibleChunksTTL), so a chunk the new rules show can
			// still be refused.
			fresh := map[int]*cloneState{}
			freshErr := map[int]error{}
			unclonable := map[int]bool{}
			freshOf := func(i int) (*cloneState, error) {
				if _, ok := fresh[i]; ok {
					return fresh[i], freshErr[i]
				}
				fresh[i] = nil
				if _, err := grpcClient.GetCommitTreeManifest(h.ctx, e2eOrgSlug, e2eProjectSlug, commits[i], sparse); err != nil {
					freshErr[i] = err
					return nil, err
				}
				target := filepath.Join(t.TempDir(), "fresh")
				err := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).
					Clone(h.ctx, url, h.host, e2eOrgSlug, e2eProjectSlug, fmt.Sprintf("c%d", i), sparse, target)
				if err != nil {
					t.Logf("%s: commit #%d cannot be cloned: %v", view, i, err)
					unclonable[i] = true
					skipped++
					return nil, nil
				}
				state := cloneStateOf(t, target)
				fresh[i] = &state
				return fresh[i], nil
			}

			// The follower starts as a clone of the first commit the view
			// can read.
			at := -1
			for i := range commits {
				if state, _ := freshOf(i); state != nil {
					at = i
					break
				}
			}
			if at < 0 {
				continue
			}
			views++
			dir := filepath.Join(t.TempDir(), "follower")
			counting := &deltaCountingClient{Client: grpcClient}
			repo := &deltaRecordingRepo{LocalRepo: localrepo.NewLocalRepo()}
			t.Cleanup(func() { _ = repo.Close() })
			require.NoError(t, clientusecase.NewRepo(auth, counting, repo).
				Clone(h.ctx, url, h.host, e2eOrgSlug, e2eProjectSlug, fmt.Sprintf("c%d", at), sparse, dir), view)
			counting.requests = 0
			// Every other follower is forced, like the daemon's updates.
			forced := (fi+si)%2 == 1
			updater := clientusecase.NewUpdate(auth, counting, repo).WithForce(forced)
			moving := fmt.Sprintf("mv%d-%d", fi, si)
			branch, detached := fmt.Sprintf("c%d", at), false

			for step := 0; step < movesPerView; step++ {
				j := gen.rng.Intn(len(commits))
				kind := []string{"update", "tag", "branch"}[gen.rng.Intn(3)]
				label := fmt.Sprintf("%s, move %d: %s #%d -> #%d", view, step, kind, at, j)
				want, wantErr := freshOf(j)
				if unclonable[j] {
					continue
				}

				// Switching to the branch the clone is on does nothing at all.
				noop := kind == "branch" && !detached && branch == fmt.Sprintf("c%d", j)
				if forced && !noop && fresh[at] != nil && len(fresh[at].working) > 0 {
					// A tracked file deleted from disk: a forced move brings
					// it back whether or not the delta touches it.
					lost := fresh[at].working[gen.rng.Intn(len(fresh[at].working))]
					for k := 0; k < len(lost); k++ {
						if lost[k] == '|' {
							lost = lost[:k]
							break
						}
					}
					// It may be gone already: a refused move restores nothing.
					if err := os.Remove(filepath.Join(dir, filepath.FromSlash(lost))); !os.IsNotExist(err) {
						require.NoError(t, err, label)
					}
					restored++
				}

				var err error
				switch kind {
				case "update":
					// The moving branch is put on the commit, then reached by
					// an update (when the follower is on it) or a switch.
					_ = h.admin.DeleteBranch(h.ctx, e2eOrgSlug, e2eProjectSlug, moving)
					_, err = h.admin.CreateBranch(h.ctx, e2eOrgSlug, e2eProjectSlug, moving, "", commits[j], "")
					require.NoError(t, err, label)
					if branch == moving && !detached {
						err = updater.Run(h.ctx, dir)
					} else {
						err = updater.Switch(h.ctx, dir, moving)
						if err == nil {
							branch, detached = moving, false
						}
					}
				case "tag":
					err = updater.SwitchTag(h.ctx, dir, fmt.Sprintf("t%d", j))
					if err == nil {
						detached = true
					}
				default:
					err = updater.Switch(h.ctx, dir, fmt.Sprintf("c%d", j))
					if err == nil {
						branch, detached = fmt.Sprintf("c%d", j), false
					}
				}
				moves++
				if wantErr != nil {
					// The commit cannot be read in this view: the move fails
					// like the clone, and a later move must still be right.
					require.Error(t, err, label)
					require.Equal(t, clientErrorCode(t, wantErr), clientErrorCode(t, err), label)
					failed++
					continue
				}
				require.NoError(t, err, label)
				at = j
				got := cloneStateOf(t, dir)
				require.Equal(t, want.stored, got.stored, "%s: stored tree", label)
				require.Equal(t, want.working, got.working, "%s: working copy", label)
			}

			require.Empty(t, repo.errs, "%s: every delta must apply", view)
			require.Equal(t, counting.requests, counting.based, "%s: every request after the clone names a base", view)
			deltas += repo.applied
		}
	}

	deltaUpdateMoves.Add(int64(moves))
	t.Logf("seed %d: %d views, %d moves (%d refused like the manifest), %d deltas applied, %d deleted files restored by forced moves, %d unclonable commits left out",
		seed, views, moves, failed, deltas, restored, skipped)
	require.Greater(t, views, len(deltaFilters)*len(deltaUpdateSparseSets)/2, "most views must be readable for some commit")
	require.Greater(t, deltas, moves/2, "most moves must go through a delta")
	require.Positive(t, restored)
}

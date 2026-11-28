package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

// treeCompareSeeds are the fixed seeds of the comparison property test; run
// one alone with -run 'TreeCompareProperty/seed=N'.
var treeCompareSeeds = []int64{1, 2, 3, 5, 8, 13}

// TestEndToEnd_TreeCompareProperty checks, over random push histories (file
// and folder swaps, moved and copied subtrees, mode and binary flips, an empty
// tree), every read-rule set of the delta property test and every commit pair
// (the empty tree included), that the diffs the server computes by walking
// only the directories whose stored hashes differ (commit diff, compare,
// merge request diff, binary changes) are exactly the diffs of the two full
// manifests the same caller can read, contents and rename detection included.
// Everything runs against the real SQLite repositories, whose reads resolve
// trees by hash.
func TestEndToEnd_TreeCompareProperty(t *testing.T) {
	t.Parallel()
	seeds := treeCompareSeeds
	if testing.Short() {
		seeds = seeds[:2]
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			runTreeCompareProperty(t, seed)
		})
	}
}

func runTreeCompareProperty(t *testing.T, seed int64) {
	h := newDeltaHarness(t)
	gen := &deltaGen{rng: rand.New(rand.NewSource(seed))}
	ctx := h.ctx

	const commitCount = 8
	model := deltaModel{}
	var commits []string
	models := map[string]deltaModel{}
	for len(commits) < commitCount {
		next := model.clone()
		switch {
		case len(commits) == 0:
			for i := 0; i < 14; i++ {
				next.set(gen.filePath(), gen.file())
			}
		case len(commits) == 4 && seed%2 == 1:
			next = deltaModel{}
		default:
			gen.mutate(next)
		}
		base := ""
		if len(commits) > 0 {
			base = commits[len(commits)-1]
		}
		id := deltaPush(t, ctx, h.admin, model, next, base)
		if len(commits) > 0 && id == commits[len(commits)-1] {
			continue
		}
		commits = append(commits, id)
		models[id] = next
		model = next
	}

	admin, err := h.uc.User.GetByEmail(ctx, e2eSuperAdminEmail)
	require.NoError(t, err)
	adminCtx := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: admin.ID, IsSuperAdmin: true, IsAdmin: true})
	userCtx := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: snow.ID(deltaUserID)})
	_, project, err := h.uc.Common.ResolveBySlug(adminCtx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	branch := h.uc.Branch

	// A side branch off the third commit, two changes ahead, with a merge
	// request into main (which moved on meanwhile).
	sideBase := commits[2]
	_, err = h.admin.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "side", "", sideBase, "")
	require.NoError(t, err)
	sideModel := models[sideBase]
	sideHead := sideBase
	for i := 0; i < 2; i++ {
		next := sideModel.clone()
		gen.mutate(next)
		sideHead = deltaPushTo(t, ctx, h.admin, "side", sideModel, next, sideHead)
		sideModel = next
	}
	mr, err := h.uc.MergeRequest.Create(adminCtx, project.ID, serverusecase.CreateMergeRequestInput{Title: "side", SourceBranch: "side", TargetBranch: "main"})
	require.NoError(t, err)

	loader := func(callCtx context.Context) func(serverDomain.Hash) ([]byte, error) {
		return func(hash serverDomain.Hash) ([]byte, error) {
			return branch.ReadEntry(callCtx, project.ID, diff.Entry{ChunkHashes: []serverDomain.Hash{hash}})
		}
	}
	full := func(callCtx context.Context, id *snow.ID) map[string]diff.Entry {
		if id == nil {
			return map[string]diff.Entry{}
		}
		_, root, err := branch.GetCommit(callCtx, project.ID, *id)
		require.NoError(t, err)
		return diff.FromTree(root)
	}

	ids := []*snow.ID{nil}
	for _, c := range append(append([]string(nil), commits...), sideHead) {
		id := mustParseBase36(t, c)
		ids = append(ids, &id)
	}
	cases, changed := 0, 0
	for _, filter := range deltaFilters {
		callCtx := userCtx
		if filter.admin {
			callCtx = adminCtx
		} else {
			h.setFilter(filter)
		}
		manifests := map[*snow.ID]map[string]diff.Entry{}
		for _, id := range ids {
			manifests[id] = full(callCtx, id)
		}
		load := loader(callCtx)
		for _, from := range ids {
			for _, to := range ids[1:] {
				label := fmt.Sprintf("seed %d, filter %q, %v -> %v", seed, filter.name, from, *to)
				want := diff.TreeDiffEntries(manifests[from], manifests[to], load)
				got, err := branch.TreeDiffBetween(callCtx, project.ID, from, *to)
				require.NoError(t, err, label)
				require.Equal(t, want, got, label)
				if from != nil {
					compared, base, err := branch.CommitDiff(callCtx, project.ID, *to, from)
					require.NoError(t, err, label)
					require.Equal(t, from, base, label)
					require.Equal(t, want, compared, label)
				}
				cases++
				if len(want) > 0 {
					changed++
				}
			}
		}

		// The merge request diff spans the merge base and the source head,
		// seen through the caller's filter.
		baseID := mustParseBase36(t, sideBase)
		headID := mustParseBase36(t, sideHead)
		want := diff.TreeDiffEntries(manifests[findID(ids, baseID)], manifests[findID(ids, headID)], load)
		got, err := h.uc.MergeRequest.Diff(callCtx, project.ID, mr.Number)
		require.NoError(t, err, filter.name)
		require.Equal(t, want, got, "seed %d, filter %q: merge request diff", seed, filter.name)
	}

	// Binary changes (the lock gate) compare the unfiltered trees.
	adminFull := map[*snow.ID]map[string]diff.Entry{}
	for _, id := range ids {
		adminFull[id] = full(adminCtx, id)
	}
	for _, from := range ids {
		for _, to := range ids[1:] {
			var want []string
			for _, change := range diff.Compare(adminFull[from], adminFull[to]) {
				if change.Old.IsBinary || change.New.IsBinary {
					want = append(want, change.Path)
				}
			}
			got, err := branch.BinaryChangesBetween(adminCtx, project.ID, from, to)
			require.NoError(t, err)
			sort.Strings(got)
			if want == nil {
				want = []string{}
			}
			require.Equal(t, want, got, "seed %d: binary changes %v -> %v", seed, from, *to)
		}
	}
	t.Logf("seed %d: %d comparisons, %d with changes", seed, cases, changed)
	require.Greater(t, changed, cases/2)
}

func findID(ids []*snow.ID, id snow.ID) *snow.ID {
	for _, candidate := range ids {
		if candidate != nil && *candidate == id {
			return candidate
		}
	}
	return nil
}

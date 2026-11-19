package gitimport

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func plan(t *testing.T, f *fixture, opts Options) *Plan {
	t.Helper()
	p, err := NewPlanner(f.source()).Plan(opts)
	require.NoError(t, err)
	return p
}

func TestPlanLinearHistory(t *testing.T) {
	for _, bare := range []bool{false, true} {
		f := newFixture(t, bare)
		a := f.commit(map[string]entry{"a": reg("1")}, "a")
		b := f.commit(map[string]entry{"a": reg("2")}, "b", a)
		c := f.commit(map[string]entry{"a": reg("3")}, "c", b)
		f.branch("main", c)
		f.head("main")

		p := plan(t, f, Options{})
		require.Equal(t, []plumbing.Hash{a, b, c}, hashes(p.Commits))
		assert.Empty(t, p.Commits[0].ParentOrigins)
		assert.Equal(t, []string{f.origin(a)}, p.Commits[1].ParentOrigins)
		assert.Equal(t, []plumbing.Hash{b}, p.Commits[2].Parents)
		assert.Equal(t, "main", p.DefaultBranch)
		assert.Equal(t, DefaultSourceKey, p.SourceKey)
		require.Len(t, p.Refs, 1)
		assert.Equal(t, "main", p.Refs[0].Target)
		assert.Equal(t, RefBranch, p.Refs[0].Kind)
		assert.Equal(t, f.origin(c), p.Refs[0].Origin)
		for _, pc := range p.Commits {
			o, err := domain.ParseOrigin(pc.Origin)
			require.NoError(t, err)
			assert.Equal(t, "git", o.System)
			assert.Equal(t, pc.Hash.String(), o.NativeID)
		}
		assert.Empty(t, p.Warnings)
	}
}

func TestPlanBranchesMergeAndRefNames(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"a": reg("1")}, "a")
	c := f.commit(map[string]entry{"a": reg("1"), "c": reg("c")}, "c", a)
	b := f.commit(map[string]entry{"a": reg("2")}, "b", a)
	m := f.commit(map[string]entry{"a": reg("2"), "c": reg("c")}, "merge", b, c)
	f.branch("main", m)
	f.branch("feature/x", c)
	f.branch("feature-x", b)
	f.branch("release/1", b)
	f.head("main")

	p := plan(t, f, Options{})
	require.Equal(t, []plumbing.Hash{a, c, b, m}, hashes(p.Commits))
	assert.Equal(t, []string{f.origin(b), f.origin(c)}, p.Commits[3].ParentOrigins)

	names := map[string]plumbing.Hash{}
	for _, r := range p.Branches() {
		names[r.Target] = r.Commit
	}
	assert.Equal(t, map[string]plumbing.Hash{"main": m, "feature-x": b, "release-1": b}, names)
	collisions := warningsOf(p.Warnings, WarnRefCollision)
	require.Len(t, collisions, 1)
	assert.Equal(t, "refs/heads/feature/x", collisions[0].Ref)
	renamed := warningsOf(p.Warnings, WarnRefRenamed)
	require.Len(t, renamed, 1)
	assert.Equal(t, "refs/heads/release/1", renamed[0].Ref)
}

func TestPlanOctopus(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"a": reg("a")}, "a")
	b := f.commit(map[string]entry{"b": reg("b")}, "b")
	c := f.commit(map[string]entry{"c": reg("c")}, "c")
	m := f.commit(map[string]entry{"a": reg("a"), "b": reg("b"), "c": reg("c")}, "octopus", a, b, c)
	f.branch("main", m)

	p := plan(t, f, Options{})
	assert.Equal(t, []plumbing.Hash{a, b, m}, hashes(p.Commits))
	assert.Equal(t, []plumbing.Hash{a, b}, p.Commits[2].Parents)
	oct := warningsOf(p.Warnings, WarnOctopus)
	require.Len(t, oct, 1)
	assert.Equal(t, m.String(), oct[0].Commit)
	assert.Contains(t, oct[0].Message, c.String())
	assert.Equal(t, "main", p.DefaultBranch)
}

func TestPlanOrphanRootsAndTies(t *testing.T) {
	f := newFixture(t, false)
	when := f.clock.Add(time.Hour)
	r1 := f.commitAt(when, map[string]entry{"x": reg("1")}, "root one")
	r2 := f.commitAt(when, map[string]entry{"y": reg("2")}, "root two")
	f.branch("one", r1)
	f.branch("two", r2)

	p := plan(t, f, Options{})
	require.Len(t, p.Commits, 2)
	first, second := r1, r2
	if r2.String() < r1.String() {
		first, second = r2, r1
	}
	assert.Equal(t, []plumbing.Hash{first, second}, hashes(p.Commits))
	for _, pc := range p.Commits {
		assert.Empty(t, pc.Parents)
	}
	assert.Equal(t, "one", p.DefaultBranch)
}

func linear(f *fixture, n int) []plumbing.Hash {
	var out []plumbing.Hash
	for i := range n {
		var parents []plumbing.Hash
		if i > 0 {
			parents = append(parents, out[i-1])
		}
		out = append(out, f.commit(map[string]entry{"f": reg(string(rune('a' + i)))}, "c", parents...))
	}
	f.branch("master", out[n-1])
	return out
}

func TestPlanDepthAndSince(t *testing.T) {
	f := newFixture(t, false)
	cs := linear(f, 5)

	p := plan(t, f, Options{Depth: 2})
	assert.Equal(t, cs[3:], hashes(p.Commits))
	assert.Empty(t, p.Commits[0].Parents)
	assert.Equal(t, "master", p.DefaultBranch)

	first, err := f.source().Commit(cs[2])
	require.NoError(t, err)
	p = plan(t, f, Options{Since: first.Committer.When})
	assert.Equal(t, cs[2:], hashes(p.Commits))
	assert.Empty(t, p.Commits[0].Parents)

	p = plan(t, f, Options{Since: f.clock.Add(time.Hour)})
	assert.Empty(t, p.Commits)
	assert.Empty(t, p.Refs)
	require.Len(t, warningsOf(p.Warnings, WarnRefOutsideWindow), 1)
	assert.Equal(t, "", p.DefaultBranch)
}

func TestPlanSnapshot(t *testing.T) {
	f := newFixture(t, false)
	cs := linear(f, 3)
	other := f.commit(map[string]entry{"o": reg("o")}, "other", cs[0])
	f.branch("other", other)
	f.ref("refs/tags/v1", cs[2])

	p := plan(t, f, Options{Snapshot: true})
	assert.Equal(t, []plumbing.Hash{cs[2], other}, hashes(p.Commits))
	for _, pc := range p.Commits {
		assert.Empty(t, pc.Parents)
	}
	assert.Len(t, p.Refs, 3)

	p = plan(t, f, Options{Snapshot: true, Since: f.clock})
	assert.Equal(t, []plumbing.Hash{other}, hashes(p.Commits))

	p = plan(t, f, Options{Snapshot: true, Known: map[string]bool{f.origin(other): true}})
	assert.Equal(t, []plumbing.Hash{cs[2]}, hashes(p.Commits))
	assert.Len(t, p.Refs, 3)
}

func TestPlanTagsAndPatterns(t *testing.T) {
	f := newFixture(t, true)
	cs := linear(f, 3)
	f.branch("dev", cs[1])
	f.ref("refs/tags/light", cs[0])
	f.annotated("v1.0", "release one\n", cs[1], plumbing.CommitObject)
	inner := f.annotated("inner", "inner tag\n", cs[2], plumbing.CommitObject)
	f.annotated("outer", "outer tag\n", inner, plumbing.TagObject)
	commit, err := f.source().Commit(cs[0])
	require.NoError(t, err)
	f.annotated("tree-tag", "a tree\n", commit.TreeHash, plumbing.TreeObject)
	f.ref("refs/remotes/origin/main", cs[2])

	p := plan(t, f, Options{})
	tags := map[string]PlannedRef{}
	for _, r := range p.Tags() {
		tags[r.Target] = r
	}
	require.Len(t, tags, 4)
	assert.Equal(t, cs[0], tags["light"].Commit)
	assert.False(t, tags["light"].Annotated)
	assert.Equal(t, cs[1], tags["v1.0"].Commit)
	assert.Equal(t, "release one\n", tags["v1.0"].Message)
	assert.True(t, tags["v1.0"].Annotated)
	assert.Equal(t, cs[2], tags["outer"].Commit)
	assert.Equal(t, "outer tag\n", tags["outer"].Message)
	skipped := warningsOf(p.Warnings, WarnRefSkipped)
	require.Len(t, skipped, 1)
	assert.Equal(t, "refs/tags/tree-tag", skipped[0].Ref)
	assert.Len(t, p.Branches(), 2)

	p = plan(t, f, Options{Refs: []string{"refs/heads/dev", "v*"}})
	require.Len(t, p.Refs, 2)
	assert.Equal(t, "dev", p.Refs[0].Target)
	assert.Equal(t, "v1.0", p.Refs[1].Target)
	assert.Equal(t, cs[:2], hashes(p.Commits))
	assert.Equal(t, "dev", p.DefaultBranch)

	p = plan(t, f, Options{Refs: []string{"refs/remotes/*"}})
	assert.Empty(t, p.Refs)
	assert.Empty(t, p.Commits)
}

func TestPlanKnownPrunes(t *testing.T) {
	f := newFixture(t, false)
	cs := linear(f, 4)
	side := f.commit(map[string]entry{"s": reg("s")}, "side", cs[1])
	m := f.commit(map[string]entry{"m": reg("m")}, "merge", cs[3], side)
	f.branch("master", m)

	closed := map[string]bool{f.origin(cs[0]): true, f.origin(cs[1]): true, f.origin(cs[2]): true}
	p := plan(t, f, Options{Known: closed})
	assert.Equal(t, []plumbing.Hash{cs[3], side, m}, hashes(p.Commits))
	assert.Equal(t, []string{f.origin(cs[2])}, p.Commits[0].ParentOrigins)
	assert.Equal(t, []string{f.origin(cs[1])}, p.Commits[1].ParentOrigins)
	assert.Equal(t, []string{f.origin(cs[3]), f.origin(side)}, p.Commits[2].ParentOrigins)

	f.ref("refs/tags/old", cs[0])
	p = plan(t, f, Options{Known: map[string]bool{f.origin(cs[2]): true}})
	assert.Equal(t, []plumbing.Hash{cs[0], cs[1], cs[3], side, m}, hashes(p.Commits))
	assert.Empty(t, p.Commits[0].ParentOrigins)
	assert.Equal(t, []string{f.origin(cs[0])}, p.Commits[1].ParentOrigins)
	assert.Equal(t, []string{f.origin(cs[2])}, p.Commits[2].ParentOrigins)
	assert.Equal(t, []string{f.origin(cs[1])}, p.Commits[3].ParentOrigins)
	assert.Empty(t, warningsOf(p.Warnings, WarnRefOutsideWindow))
	assert.Len(t, p.Refs, 2)

	p = plan(t, f, Options{Known: map[string]bool{f.origin(m): true}})
	assert.Equal(t, []plumbing.Hash{cs[0]}, hashes(p.Commits))
	require.Len(t, p.Refs, 2)
	assert.Equal(t, f.origin(m), p.Refs[0].Origin)
	assert.Equal(t, f.origin(cs[0]), p.Refs[1].Origin)
}

func TestPlanKnownPrunesEveryForkAncestor(t *testing.T) {
	f := newFixture(t, false)
	cs := linear(f, 6)
	feature := f.commit(map[string]entry{"x": reg("x")}, "feature", cs[3])
	f.branch("feature", feature)

	known := map[string]bool{}
	for _, h := range cs {
		known[f.origin(h)] = true
	}
	p := plan(t, f, Options{Known: known})
	assert.Equal(t, []plumbing.Hash{feature}, hashes(p.Commits))
	assert.Equal(t, []string{f.origin(cs[3])}, p.Commits[0].ParentOrigins)
}

func TestPlanKnownStopsAtImportedHistory(t *testing.T) {
	f := newFixture(t, false)
	cs := linear(f, 4)
	for _, h := range cs[:2] {
		s := h.String()
		require.NoError(t, os.Remove(filepath.Join(f.dir, ".git", "objects", s[:2], s[2:])))
	}

	closed := map[string]bool{f.origin(cs[0]): true, f.origin(cs[1]): true, f.origin(cs[2]): true}
	for _, known := range []map[string]bool{closed, {f.origin(cs[2]): true}} {
		p := plan(t, f, Options{Known: known})
		assert.Equal(t, []plumbing.Hash{cs[3]}, hashes(p.Commits))
		assert.Equal(t, []string{f.origin(cs[2])}, p.Commits[0].ParentOrigins)
	}
}

func TestPlanDefaultBranch(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"a": reg("a")}, "a")
	f.branch("zeta", a)
	f.branch("alpha", a)
	f.branch("feature/y", a)
	require.NoError(t, f.repo.Storer.SetReference(plumbing.NewHashReference(plumbing.HEAD, a)))

	assert.Equal(t, "alpha", plan(t, f, Options{}).DefaultBranch)
	assert.Equal(t, "feature-y", plan(t, f, Options{DefaultBranch: "feature/y"}).DefaultBranch)
	assert.Equal(t, "zeta", plan(t, f, Options{DefaultBranch: "zeta"}).DefaultBranch)
	_, err := NewPlanner(f.source()).Plan(Options{DefaultBranch: "nope"})
	assert.ErrorContains(t, err, "not among")

	f.branch("main", a)
	f.head("gone")
	assert.Equal(t, "main", plan(t, f, Options{}).DefaultBranch)
}

func TestPlanOptionErrors(t *testing.T) {
	f := newFixture(t, false)
	linear(f, 1)
	pl := NewPlanner(f.source())
	for _, opts := range []Options{
		{SourceKey: "a:b"},
		{SourceKey: "has space"},
		{Depth: -1},
		{Refs: []string{" "}},
	} {
		_, err := pl.Plan(opts)
		assert.Error(t, err, "%+v", opts)
	}
	p, err := pl.Plan(Options{SourceKey: "game"})
	require.NoError(t, err)
	assert.Equal(t, "git:game:"+p.Commits[0].Hash.String(), p.Commits[0].Origin)

	_, err = Open(t.TempDir())
	assert.Error(t, err)
}

func TestPlanMissingCommit(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"a": reg("a")}, "a", plumbing.NewHash("1111111111111111111111111111111111111111"))
	f.branch("main", a)
	_, err := NewPlanner(f.source()).Plan(Options{})
	assert.ErrorContains(t, err, "read commit")

	f.branch("broken", plumbing.NewHash("2222222222222222222222222222222222222222"))
	_, ws, err := f.source().Refs()
	require.NoError(t, err)
	require.Len(t, warningsOf(ws, WarnRefSkipped), 1)
}

func TestSanitizeRefName(t *testing.T) {
	cases := map[string]string{
		"main":       "main",
		"feature/x":  "feature-x",
		"a b\tc":     "a-b-c",
		"..":         "--",
		"x\x7fy":     "x-y",
		"release/v1": "release-v1",
	}
	for in, want := range cases {
		got := SanitizeRefName(in)
		assert.Equal(t, want, got, in)
		assert.NoError(t, domain.ValidateBranchName(got))
	}
}

func TestNewSourceAndShort(t *testing.T) {
	f := newFixture(t, false)
	src := NewSource(f.repo)
	assert.Same(t, f.repo, src.Repository())
	head, err := src.HeadBranch()
	require.NoError(t, err)
	assert.Equal(t, plumbing.ReferenceName("refs/heads/master"), head)
	assert.Equal(t, "x", Ref{Name: "refs/tags/x"}.Short())
}

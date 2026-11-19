package gitimport

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

func converter(t *testing.T, f *fixture, opts ConverterOptions) *Converter {
	t.Helper()
	c, err := NewConverter(f.source(), opts)
	require.NoError(t, err)
	return c
}

func convertAll(t *testing.T, c *Converter, p *Plan) []*Event {
	t.Helper()
	var out []*Event
	for _, pc := range p.Commits {
		ev, err := c.Convert(pc)
		require.NoError(t, err)
		out = append(out, ev)
	}
	return out
}

func filesByPath(ev *Event) map[string]File {
	out := map[string]File{}
	for _, f := range ev.Files {
		out[f.Path] = f
	}
	return out
}

func paths(ev *Event) []string {
	var out []string
	for _, f := range ev.Files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

func TestConvertDeltasAndRemovals(t *testing.T) {
	f := newFixture(t, false)
	bin := "PNG\x00\x01\x02binary"
	a := f.commit(map[string]entry{"a.txt": reg("one\n"), "dir/b.txt": reg("bee\n"), "img/c.png": reg(bin)}, "root\n")
	b := f.commit(map[string]entry{"a.txt": reg("two\n"), "img/c.png": reg(bin), "d/e/f.txt": reg("new\n")}, "second\n", a)
	f.branch("main", b)

	p := plan(t, f, Options{})
	c := converter(t, f, ConverterOptions{})
	evs := convertAll(t, c, p)

	root := evs[0]
	assert.Equal(t, f.origin(a), root.Origin)
	assert.Empty(t, root.Parents)
	assert.Equal(t, "root\n", root.Message)
	assert.Equal(t, "Ada Author", root.Author.Name)
	assert.Equal(t, "ada@example.com", root.Author.Email)
	assert.Equal(t, root.Author.When, root.CreatedAt())
	assert.True(t, root.Author.When.Before(root.CommitterWhen))
	assert.Equal(t, []string{"a.txt", "dir/b.txt", "img/c.png"}, paths(root))
	assert.Empty(t, root.Removed)

	files := filesByPath(root)
	png := files["img/c.png"]
	assert.True(t, png.IsBinary)
	assert.Equal(t, chunker.EncodingRaw, png.Encoding)
	assert.Equal(t, int64(len(bin)), png.SizeBytes)
	assert.Equal(t, ModeReadWrite, png.Mode)
	txt := files["a.txt"]
	assert.False(t, txt.IsBinary)
	assert.Equal(t, chunker.EncodingZstd, txt.Encoding)
	assert.Equal(t, int64(4), txt.SizeBytes)
	require.Len(t, txt.ChunkHashes, 1)
	require.Len(t, txt.ChunkSizes, 1)
	assert.Equal(t, chunker.FileHash(txt.ChunkHashes), txt.FileHash)
	assert.NotEmpty(t, txt.BlobSHA)

	second := evs[1]
	assert.Equal(t, []string{f.origin(a)}, second.Parents)
	assert.Equal(t, []string{"a.txt", "d/e/f.txt"}, paths(second))
	assert.Equal(t, []string{"dir/b.txt"}, second.Removed)
	assert.Empty(t, second.Warnings)
}

func TestConvertExecBitAndMemo(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"run.sh": reg("#!/bin/sh\necho hi\n")}, "a")
	b := f.commit(map[string]entry{"run.sh": exe("#!/bin/sh\necho hi\n")}, "b", a)
	c := f.commit(map[string]entry{"run.sh": exe("#!/bin/sh\necho hi\n"), "copy.sh": reg("#!/bin/sh\necho hi\n")}, "c", b)
	f.branch("main", c)

	var sunk []domain.Hash
	conv := converter(t, f, ConverterOptions{Sink: func(ch chunker.EncodedChunk) error {
		sunk = append(sunk, ch.Hash)
		require.Equal(t, int64(len(ch.Data)), ch.SizeBytes)
		return nil
	}})
	evs := convertAll(t, conv, plan(t, f, Options{}))

	require.Len(t, evs[1].Files, 1)
	assert.Equal(t, ModeExecutable, evs[1].Files[0].Mode)
	assert.Equal(t, evs[0].Files[0].FileHash, evs[1].Files[0].FileHash)
	assert.Equal(t, []string{"copy.sh"}, paths(evs[2]))
	assert.Equal(t, ModeReadWrite, evs[2].Files[0].Mode)
	assert.Equal(t, Stats{BlobsRead: 1, BlobHits: 2}, conv.Stats())
	assert.Len(t, sunk, 1)
}

func TestConvertSymlinks(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"real.txt": reg("data\n"), "ln": link("real.txt")}, "a")
	b := f.commit(map[string]entry{"real.txt": link("ln")}, "b", a)
	f.branch("main", b)
	p := plan(t, f, Options{})

	skip := convertAll(t, converter(t, f, ConverterOptions{Symlinks: SymlinksSkip}), p)
	assert.Equal(t, []string{"real.txt"}, paths(skip[0]))
	require.Len(t, skip[0].Warnings, 1)
	assert.Equal(t, WarnSymlink, skip[0].Warnings[0].Kind)
	assert.Equal(t, "ln", skip[0].Warnings[0].Path)
	assert.Empty(t, skip[1].Files)
	assert.Equal(t, []string{"real.txt"}, skip[1].Removed)
	require.Len(t, skip[1].Warnings, 1)
	assert.Equal(t, "real.txt", skip[1].Warnings[0].Path)

	asFile := convertAll(t, converter(t, f, ConverterOptions{Symlinks: SymlinksFile}), p)
	files := filesByPath(asFile[0])
	require.Contains(t, files, "ln")
	assert.Equal(t, int64(len("real.txt")), files["ln"].SizeBytes)
	assert.Contains(t, asFile[0].Warnings[0].Message, "stored as a file")
	assert.Equal(t, []string{"real.txt"}, paths(asFile[1]))
	assert.Equal(t, []string{"ln"}, asFile[1].Removed)

	_, err := NewConverter(f.source(), ConverterOptions{Symlinks: "follow"})
	assert.ErrorContains(t, err, "symlink policy")
}

func TestConvertSubmodule(t *testing.T) {
	f := newFixture(t, false)
	modules := "[submodule \"lib\"]\n\tpath = vendor/lib\n\turl = https://example.com/lib.git\n"
	a := f.commit(map[string]entry{
		".gitmodules": reg(modules),
		"vendor/lib":  gitlink("3333333333333333333333333333333333333333"),
		"other":       gitlink("4444444444444444444444444444444444444444"),
	}, "a")
	f.branch("main", a)

	ev := convertAll(t, converter(t, f, ConverterOptions{}), plan(t, f, Options{}))[0]
	assert.Equal(t, []string{".gitmodules"}, paths(ev))
	subs := warningsOf(ev.Warnings, WarnSubmodule)
	require.Len(t, subs, 2)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Path < subs[j].Path })
	assert.Equal(t, "submodule skipped", subs[0].Message)
	assert.Equal(t, "vendor/lib", subs[1].Path)
	assert.Contains(t, subs[1].Message, "https://example.com/lib.git")
}

func TestConvertInvalidPaths(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{`bad\name.txt`: reg("x"), "ok.txt": reg("y")}, "a")
	f.branch("main", a)

	_, err := converter(t, f, ConverterOptions{}).Convert(plan(t, f, Options{}).Commits[0])
	var invalid *InvalidPathsError
	require.True(t, errors.As(err, &invalid))
	assert.Equal(t, []string{`bad\name.txt`}, invalid.Paths)
	assert.Equal(t, a.String(), invalid.Commit)
	assert.Contains(t, err.Error(), `bad\name.txt`)
}

func TestConvertSnapshotAndMerge(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"a": reg("a"), "b": reg("b")}, "a")
	side := f.commit(map[string]entry{"a": reg("a"), "b": reg("b"), "s": reg("s")}, "side", a)
	main := f.commit(map[string]entry{"a": reg("A"), "b": reg("b")}, "main", a)
	m := f.commit(map[string]entry{"a": reg("A"), "b": reg("b"), "s": reg("s")}, "merge", main, side)
	f.branch("main", m)

	evs := convertAll(t, converter(t, f, ConverterOptions{}), plan(t, f, Options{}))
	merge := evs[len(evs)-1]
	assert.Equal(t, []string{f.origin(main), f.origin(side)}, merge.Parents)
	assert.Equal(t, []string{"s"}, paths(merge))

	snap := convertAll(t, converter(t, f, ConverterOptions{}), plan(t, f, Options{Snapshot: true}))
	require.Len(t, snap, 1)
	assert.Equal(t, []string{"a", "b", "s"}, paths(snap[0]))
	assert.Empty(t, snap[0].Parents)

	empty := f.commit(map[string]entry{"a": reg("A"), "b": reg("b"), "s": reg("s")}, "empty", m)
	f.branch("main", empty)
	evs = convertAll(t, converter(t, f, ConverterOptions{}), plan(t, f, Options{Known: map[string]bool{f.origin(m): true}}))
	require.Len(t, evs, 1)
	assert.Empty(t, evs[0].Files)
	assert.Empty(t, evs[0].Removed)
}

func TestConvertEncodingParity(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var large strings.Builder
	for large.Len() < chunker.TextSingleBlobRawMax+(1<<20) {
		fmt.Fprintf(&large, "line %d %x\n", large.Len(), rng.Int63())
	}
	random := make([]byte, 3<<20)
	rng.Read(random)
	random[0] = 0
	contents := map[string]string{
		"small.txt":      "hello\nworld\n",
		"empty.txt":      "",
		"large.txt":      large.String(),
		"tex/albedo.png": string(random),
		"blob.bin":       string(random[:200<<10]),
	}
	f := newFixture(t, false)
	files := map[string]entry{}
	for p, c := range contents {
		files[p] = reg(c)
	}
	f.branch("main", f.commit(files, "a"))

	stored := map[domain.Hash]int64{}
	conv := converter(t, f, ConverterOptions{Sink: func(ch chunker.EncodedChunk) error {
		stored[ch.Hash] = ch.SizeBytes
		return nil
	}})
	ev := convertAll(t, conv, plan(t, f, Options{}))[0]
	got := filesByPath(ev)
	for p, c := range contents {
		isBinary := chunker.IsBinary([]byte(c))
		enc, chunks, err := chunker.EncodeBytes([]byte(c), p, isBinary, "")
		require.NoError(t, err)
		var want []domain.Hash
		for _, ch := range chunks {
			want = append(want, ch.Hash)
			assert.Equal(t, ch.SizeBytes, stored[ch.Hash], p)
		}
		assert.Equal(t, enc, got[p].Encoding, p)
		assert.Equal(t, isBinary, got[p].IsBinary, p)
		assert.Equal(t, want, got[p].ChunkHashes, p)
		assert.Equal(t, chunker.FileHash(want), got[p].FileHash, p)
		assert.Equal(t, int64(len(c)), got[p].SizeBytes, p)
	}
	assert.Greater(t, len(got["large.txt"].ChunkHashes), 1)
}

func TestConvertBinaryProfileKeyedByPath(t *testing.T) {
	f := newFixture(t, false)
	data := strings.Repeat("\x00binary", 1000)
	a := f.commit(map[string]entry{"x.png": reg(data), "x.bin": reg(data), "y.bin": reg(data)}, "a")
	f.branch("main", a)
	cache := NewMemoryBlobCache()
	conv := converter(t, f, ConverterOptions{Cache: cache})
	convertAll(t, conv, plan(t, f, Options{}))
	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, Stats{BlobsRead: 2, BlobHits: 1}, conv.Stats())
}

type failingCache struct {
	getErr, putErr error
}

func (c failingCache) Get(BlobKey) (*Blob, bool, error) { return nil, false, c.getErr }
func (c failingCache) Put(BlobKey, *Blob) error         { return c.putErr }

func TestConvertErrors(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"a": reg("a")}, "a")
	f.branch("main", a)
	pc := plan(t, f, Options{}).Commits[0]
	boom := errors.New("boom")

	_, err := converter(t, f, ConverterOptions{Cache: failingCache{getErr: boom}}).Convert(pc)
	assert.ErrorIs(t, err, boom)
	_, err = converter(t, f, ConverterOptions{Cache: failingCache{putErr: boom}}).Convert(pc)
	assert.ErrorIs(t, err, boom)
	_, err = converter(t, f, ConverterOptions{Sink: func(chunker.EncodedChunk) error { return boom }}).Convert(pc)
	assert.ErrorIs(t, err, boom)

	conv := converter(t, f, ConverterOptions{})
	_, err = conv.Convert(&PlannedCommit{Hash: plumbing.NewHash("5555555555555555555555555555555555555555")})
	assert.ErrorContains(t, err, "read commit")
	_, err = conv.Convert(&PlannedCommit{Hash: a, Parents: []plumbing.Hash{plumbing.NewHash("6666666666666666666666666666666666666666")}})
	assert.ErrorContains(t, err, "read parent")
	_, err = conv.DryRun(&Plan{Commits: []*PlannedCommit{{Hash: plumbing.NewHash("5555555555555555555555555555555555555555")}}})
	assert.ErrorContains(t, err, "read commit")

	r := &blobReadSeeker{}
	_, err = r.Seek(3, 0)
	assert.ErrorIs(t, err, errBlobSeek)
	assert.NoError(t, r.Close())

	_, err = ParseSymlinkPolicy("")
	assert.NoError(t, err)
}

func TestDryRunReport(t *testing.T) {
	f := newFixture(t, false)
	a := f.commit(map[string]entry{"a.txt": reg("aaaa"), "ln": link("a.txt"), "sub": gitlink("3333333333333333333333333333333333333333")}, "a")
	b := f.commit(map[string]entry{"a.txt": reg("aaaa"), "ln": link("a.txt"), "sub": gitlink("3333333333333333333333333333333333333333")}, "empty", a)
	c := f.commit(map[string]entry{"a.txt": reg("bb"), "copy.txt": reg("aaaa"), `x\y`: reg("z")}, "c", b)
	o := f.commit(map[string]entry{"o": reg("o")}, "o")
	octo := f.commit(map[string]entry{"a.txt": reg("bb")}, "octopus", c, o, a)
	f.branch("main", octo)
	f.ref("refs/tags/v1", a)

	p := plan(t, f, Options{})
	conv := converter(t, f, ConverterOptions{})
	r, err := conv.DryRun(p)
	require.NoError(t, err)
	assert.Equal(t, 5, r.Commits)
	assert.Equal(t, 1, r.Branches)
	assert.Equal(t, 1, r.Tags)
	assert.Equal(t, 3, r.Blobs)
	assert.Equal(t, int64(4+2+1), r.Bytes)
	assert.Equal(t, []string{b.String()}, r.EmptyCommits)
	assert.Equal(t, []string{octo.String()}, r.Octopus)
	assert.Equal(t, []PathIssue{{Commit: c.String(), Path: `x\y`}}, r.InvalidPaths)
	assert.Equal(t, []PathIssue{{Commit: a.String(), Path: "ln"}}, r.Symlinks)
	require.Len(t, r.Submodules, 1)
	assert.Equal(t, "sub", r.Submodules[0].Path)
	assert.Equal(t, Stats{}, conv.Stats())
	assert.NotEmpty(t, r.Warnings)
	assert.Equal(t, 1, r.Removed)
	assert.Equal(t, 4, r.Files)
}

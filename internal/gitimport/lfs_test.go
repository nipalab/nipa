package gitimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/gitimport/lfs"
)

type memLFS struct {
	objects map[string][]byte
	local   map[string]bool
	opens   map[string]int
	err     error
}

type memObject struct{ *bytes.Reader }

func (memObject) Close() error { return nil }

func newMemLFS(contents ...string) *memLFS {
	m := &memLFS{objects: map[string][]byte{}, local: map[string]bool{}, opens: map[string]int{}}
	for _, c := range contents {
		m.objects[pointerOf(c).OID] = []byte(c)
		m.local[pointerOf(c).OID] = true
	}
	return m
}

func (m *memLFS) Has(p lfs.Pointer) bool { return m.local[p.OID] }

func (m *memLFS) Open(_ context.Context, p lfs.Pointer) (lfs.Object, error) {
	m.opens[p.OID]++
	if m.err != nil {
		return nil, m.err
	}
	data, ok := m.objects[p.OID]
	if !ok {
		return nil, &lfs.MissingError{OID: p.OID, Mode: lfs.ModeLocal}
	}
	return memObject{bytes.NewReader(data)}, nil
}

func pointerOf(content string) lfs.Pointer {
	sum := sha256.Sum256([]byte(content))
	return lfs.Pointer{OID: hex.EncodeToString(sum[:]), Size: int64(len(content))}
}

type recordingCache struct {
	*MemoryBlobCache
	records map[string]string
}

func (r *recordingCache) RecordLFS(oid, sha string) error {
	r.records[oid] = sha
	return nil
}

func TestConvertResolvesLFSPointers(t *testing.T) {
	f := newFixture(t, false)
	texture := "PNG\x00" + strings.Repeat("texel", 100)
	doc := "plain lfs text\n"
	ptr := pointerOf(texture).String()
	variant := strings.Replace(ptr, "\noid", "\next-0-x sha256:"+pointerOf("x").OID+"\noid", 1)
	a := f.commit(map[string]entry{"art/hero.png": reg(ptr), "docs/guide.md": reg(pointerOf(doc).String()), "a.txt": reg("small\n")}, "root\n")
	b := f.commit(map[string]entry{"art/hero.png": reg(ptr), "docs/guide.md": reg(pointerOf(doc).String()), "a.txt": reg("small\n"), "art/copy.png": reg(variant)}, "copy\n", a)
	f.branch("main", b)

	store := newMemLFS(texture, doc)
	cache := &recordingCache{MemoryBlobCache: NewMemoryBlobCache(), records: map[string]string{}}
	c := converter(t, f, ConverterOptions{LFS: store, Cache: cache})
	evs := convertAll(t, c, plan(t, f, Options{}))

	hero := filesByPath(evs[0])["art/hero.png"]
	_, chunks, err := chunker.EncodeBytes([]byte(texture), "art/hero.png", true, "")
	require.NoError(t, err)
	assert.True(t, hero.IsBinary)
	assert.Equal(t, int64(len(texture)), hero.SizeBytes)
	assert.Equal(t, chunks[0].Hash, hero.ChunkHashes[0])

	guide := filesByPath(evs[0])["docs/guide.md"]
	assert.False(t, guide.IsBinary)
	assert.Equal(t, chunker.EncodingZstd, guide.Encoding)
	assert.Equal(t, int64(len(doc)), guide.SizeBytes)

	cp := filesByPath(evs[1])["art/copy.png"]
	assert.Equal(t, hero.FileHash, cp.FileHash)
	assert.Equal(t, 1, store.opens[pointerOf(texture).OID], "the same object under two pointer blobs converts once")
	stats := c.Stats()
	assert.Equal(t, int64(2), stats.LFSRead)
	assert.Equal(t, int64(1), stats.LFSHits)
	assert.Equal(t, int64(1), stats.BlobsRead)
	assert.Len(t, cache.records, 2)
}

func TestConvertLFSOffKeepsPointer(t *testing.T) {
	f := newFixture(t, false)
	ptr := pointerOf("content").String()
	f.branch("main", f.commit(map[string]entry{"a.bin": reg(ptr)}, "root\n"))
	c := converter(t, f, ConverterOptions{})
	ev := convertAll(t, c, plan(t, f, Options{}))[0]
	assert.Equal(t, int64(len(ptr)), ev.Files[0].SizeBytes)
	assert.Zero(t, c.Stats().LFSRead)
}

func TestConvertLFSErrors(t *testing.T) {
	f := newFixture(t, false)
	f.branch("main", f.commit(map[string]entry{"a.bin": reg(pointerOf("content").String())}, "root\n"))
	p := plan(t, f, Options{})

	store := newMemLFS()
	_, err := converter(t, f, ConverterOptions{LFS: store}).Convert(p.Commits[0])
	var missing *lfs.MissingError
	require.ErrorAs(t, err, &missing)

	store.objects[pointerOf("content").OID] = []byte("CONTENT")
	_, err = converter(t, f, ConverterOptions{LFS: store}).Convert(p.Commits[0])
	require.ErrorIs(t, err, lfs.ErrCorrupt)

	store.err = errors.New("network down")
	_, err = converter(t, f, ConverterOptions{LFS: store}).Convert(p.Commits[0])
	require.ErrorContains(t, err, "network down")

	store = newMemLFS("content")
	_, err = converter(t, f, ConverterOptions{LFS: store, Cache: failingCache{getErr: errors.New("cache down")}}).Convert(p.Commits[0])
	require.ErrorContains(t, err, "cache down")
	_, err = converter(t, f, ConverterOptions{LFS: store, Cache: failingCache{putErr: errors.New("put down")}}).Convert(p.Commits[0])
	require.ErrorContains(t, err, "put down")
}

func TestConvertLargeTextLFSObject(t *testing.T) {
	f := newFixture(t, false)
	big := strings.Repeat("line of text that compresses well\n", (9<<20)/34)
	f.branch("main", f.commit(map[string]entry{"data.csv": reg(pointerOf(big).String())}, "root\n"))
	c := converter(t, f, ConverterOptions{LFS: newMemLFS(big)})
	ev := convertAll(t, c, plan(t, f, Options{}))[0]
	assert.Equal(t, int64(len(big)), ev.Files[0].SizeBytes)
	assert.False(t, ev.Files[0].IsBinary)
}

func TestDryRunCountsLFS(t *testing.T) {
	f := newFixture(t, false)
	local, remote := "local object", "remote object!"
	f.branch("main", f.commit(map[string]entry{
		"a.png": reg(pointerOf(local).String()),
		"b.png": reg(pointerOf(remote).String()),
		"c.png": reg(strings.Replace(pointerOf(remote).String(), "\noid", "\next-0-x sha256:"+pointerOf("x").OID+"\noid", 1)),
		"d.txt": reg("text\n"),
	}, "root\n"))
	p := plan(t, f, Options{})

	store := newMemLFS(local)
	r, err := converter(t, f, ConverterOptions{LFS: store}).DryRun(p)
	require.NoError(t, err)
	assert.Equal(t, 2, r.LFSObjects)
	assert.Equal(t, int64(len(local)+len(remote)), r.LFSBytes)
	assert.Equal(t, int64(len(local)+len(remote)+len("text\n")), r.Bytes)
	require.Len(t, r.LFSMissing, 1)
	assert.Equal(t, pointerOf(remote).OID, r.LFSMissing[0].OID)
	assert.Equal(t, int64(len(remote)), r.LFSMissing[0].Size)
	assert.Equal(t, "b.png", r.LFSMissing[0].Path)

	r, err = converter(t, f, ConverterOptions{}).DryRun(p)
	require.NoError(t, err)
	assert.Equal(t, 2, r.LFSObjects)
	assert.Empty(t, r.LFSMissing)
	assert.Equal(t, 4, r.Blobs)
	assert.Greater(t, r.Bytes, int64(300))
}

func TestDryRunSkipsUploadedBlobs(t *testing.T) {
	f := newFixture(t, false)
	obj := "lfs object"
	f.branch("main", f.commit(map[string]entry{"a.png": reg(pointerOf(obj).String()), "d.txt": reg("text\n")}, "root\n"))
	p := plan(t, f, Options{})

	cache := NewMemoryBlobCache()
	_, err := converter(t, f, ConverterOptions{LFS: newMemLFS(obj), Cache: cache}).Convert(p.Commits[0])
	require.NoError(t, err)
	r, err := converter(t, f, ConverterOptions{LFS: newMemLFS(), Cache: cache}).DryRun(p)
	require.NoError(t, err)
	assert.Zero(t, r.Bytes)
	assert.Empty(t, r.LFSMissing, "an uploaded object is not needed again")
	assert.Equal(t, 1, r.LFSObjects)

	boom := errors.New("cache down")
	_, err = converter(t, f, ConverterOptions{Cache: failingCache{getErr: boom}}).DryRun(p)
	assert.ErrorIs(t, err, boom)

	g := newFixture(t, false)
	g.branch("main", g.commit(map[string]entry{"a.png": reg(pointerOf(obj).String())}, "root\n"))
	_, err = converter(t, g, ConverterOptions{LFS: newMemLFS(obj), Cache: failingCache{getErr: boom}}).DryRun(plan(t, g, Options{}))
	assert.ErrorIs(t, err, boom)
}

func TestSourceClose(t *testing.T) {
	f := newFixture(t, false)
	require.NoError(t, f.source().Close())
	repo, err := git.Init(memory.NewStorage(), nil)
	require.NoError(t, err)
	require.NoError(t, NewSource(repo).Close())
}

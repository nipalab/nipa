package lfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeObject(t *testing.T, gitDir, content string) Pointer {
	t.Helper()
	p := Pointer{OID: oidOf(content), Size: int64(len(content))}
	s := NewLocalStore(gitDir)
	require.NoError(t, os.MkdirAll(filepath.Dir(s.Path(p.OID)), 0o755))
	require.NoError(t, os.WriteFile(s.Path(p.OID), []byte(content), 0o644))
	return p
}

func TestLocalStore(t *testing.T) {
	dir := t.TempDir()
	p := writeObject(t, dir, "local content")
	s := NewLocalStore(dir)
	assert.Equal(t, filepath.Join(dir, "lfs", "objects", p.OID[:2], p.OID[2:4], p.OID), s.Path(p.OID))
	assert.True(t, s.Has(p))
	assert.False(t, s.Has(Pointer{OID: p.OID, Size: 3}))
	assert.False(t, s.Has(Pointer{OID: "zz", Size: 3}))
	assert.False(t, s.Has(Pointer{OID: oidOf("other"), Size: 5}))

	obj, err := s.Open(p)
	require.NoError(t, err)
	data, err := io.ReadAll(obj)
	require.NoError(t, err)
	require.NoError(t, obj.Close())
	assert.Equal(t, "local content", string(data))

	_, err = s.Open(Pointer{OID: oidOf("other"), Size: 5})
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestVerifier(t *testing.T) {
	content := "verify me"
	p := Pointer{OID: oidOf(content), Size: int64(len(content))}

	v := NewVerifier(bytes.NewReader([]byte(content)), p)
	buf := make([]byte, 3)
	_, err := v.Read(buf)
	require.NoError(t, err)
	_, err = v.Seek(0, io.SeekStart)
	require.NoError(t, err)
	_, err = io.ReadAll(v)
	require.NoError(t, err)
	require.NoError(t, v.Verify())

	v = NewVerifier(bytes.NewReader([]byte(content)), p)
	_, err = io.ReadFull(v, make([]byte, len(content)))
	require.NoError(t, err)
	require.NoError(t, v.Verify(), "verify drains to EOF")

	v = NewVerifier(bytes.NewReader([]byte(content+"!")), p)
	_, err = io.ReadFull(v, make([]byte, len(content)))
	require.NoError(t, err)
	require.ErrorIs(t, v.Verify(), ErrCorrupt)

	v = NewVerifier(bytes.NewReader([]byte("verify mE")), p)
	_, _ = io.ReadAll(v)
	err = v.Verify()
	require.ErrorIs(t, err, ErrCorrupt)
	assert.Contains(t, err.Error(), "hashes to")

	v = NewVerifier(bytes.NewReader([]byte("short")), p)
	_, _ = io.ReadAll(v)
	require.ErrorContains(t, v.Verify(), "has 5 bytes")

	v = NewVerifier(bytes.NewReader([]byte(content)), p)
	_, err = v.Seek(2, io.SeekStart)
	require.ErrorContains(t, err, "rewinds")
	_, err = v.Seek(-1, io.SeekStart)
	require.Error(t, err)

	v = NewVerifier(failingReader{}, p)
	_, _ = v.Read(buf)
	require.Error(t, v.Verify())
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error)       { return 0, errors.New("boom") }
func (failingReader) Seek(int64, int) (int64, error) { return 0, nil }

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": ModeAuto, "auto": ModeAuto, "local": ModeLocal, "off": ModeOff} {
		got, err := ParseMode(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := ParseMode("remote")
	require.ErrorContains(t, err, "invalid lfs mode")
}

type fakeFetcher struct {
	calls int
	obj   Object
	err   error
}

func (f *fakeFetcher) Fetch(context.Context, Pointer) (Object, error) {
	f.calls++
	return f.obj, f.err
}

type nopObject struct{ *bytes.Reader }

func (nopObject) Close() error { return nil }

func TestResolver(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	local := writeObject(t, dir, "in the store")
	remote := Pointer{OID: oidOf("remote"), Size: 6}
	fetcher := &fakeFetcher{obj: nopObject{bytes.NewReader([]byte("remote"))}}

	r := &Resolver{Mode: ModeAuto, Local: []*LocalStore{NewLocalStore(t.TempDir()), NewLocalStore(dir)}, Remote: fetcher}
	assert.True(t, r.Has(local))
	assert.False(t, r.Has(remote))
	assert.True(t, r.CanFetch())
	obj, err := r.Open(ctx, local)
	require.NoError(t, err)
	require.NoError(t, obj.Close())
	assert.Zero(t, fetcher.calls)
	obj, err = r.Open(ctx, remote)
	require.NoError(t, err)
	require.NoError(t, obj.Close())
	assert.Equal(t, 1, fetcher.calls)

	r.Mode = ModeLocal
	_, err = r.Open(ctx, remote)
	var missing *MissingError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "lfs object "+remote.OID+" is not in the local lfs store", err.Error())

	r = &Resolver{Mode: ModeAuto}
	_, err = r.Open(ctx, remote)
	require.ErrorContains(t, err, "no lfs server is configured")
	assert.Contains(t, (&MissingError{OID: "x", Mode: ModeAuto, Remote: true}).Error(), "is missing")
}

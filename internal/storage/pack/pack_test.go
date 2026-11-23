package pack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
)

func testEntries(contents ...string) []Entry {
	entries := make([]Entry, 0, len(contents))
	for _, content := range contents {
		entries = append(entries, Entry{Hash: chunker.Sum([]byte(content)), Data: []byte(content)})
	}
	return entries
}

func TestBuildAndReadIndexRoundTrip(t *testing.T) {
	entries := testEntries("alpha", "bravo charlie", "d")
	p, err := Build(entries)
	require.NoError(t, err)
	encoded := p.Bytes()
	require.Equal(t, p.Size(), int64(len(encoded)))
	require.Equal(t, Magic, string(encoded[:4]))
	require.Equal(t, Magic, string(encoded[len(encoded)-4:]))

	index, err := ReadIndex(bytes.NewReader(encoded), int64(len(encoded)))
	require.NoError(t, err)
	require.Equal(t, p.Index(), index)
	for i, entry := range index {
		assert.Equal(t, entries[i].Hash, entry.Hash)
		got := encoded[entry.Offset : entry.Offset+entry.Length]
		assert.Equal(t, entries[i].Data, got)
		assert.Equal(t, entry.Hash, chunker.Sum(got))
	}
	assert.Equal(t, int64(HeaderSize), index[0].Offset)
}

func TestReaderReadsAndSeeks(t *testing.T) {
	p, err := Build(testEntries("one", "two", "three"))
	require.NoError(t, err)
	r := p.Reader()
	all, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, p.Bytes(), all)

	// Small reads cross segment boundaries.
	_, err = r.Seek(0, io.SeekStart)
	require.NoError(t, err)
	var out []byte
	buf := make([]byte, 2)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	require.Equal(t, all, out)

	pos, err := r.Seek(-4, io.SeekEnd)
	require.NoError(t, err)
	require.Equal(t, p.Size()-4, pos)
	tail, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, Magic, string(tail))

	pos, err = r.Seek(-2, io.SeekCurrent)
	require.NoError(t, err)
	require.Equal(t, p.Size()-2, pos)
	_, err = r.Seek(-1, io.SeekStart)
	require.Error(t, err)
	_, err = r.Seek(0, 7)
	require.Error(t, err)
}

func TestBuildRejectsEmptyInput(t *testing.T) {
	_, err := Build(nil)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = Build([]Entry{{Data: nil}})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestReadIndexRejectsCorruption(t *testing.T) {
	p, err := Build(testEntries("alpha", "bravo"))
	require.NoError(t, err)
	good := p.Bytes()
	size := len(good)
	indexOffset := int(binary.BigEndian.Uint64(good[size-TrailerSize:]))

	mutate := func(fn func(b []byte) []byte) []byte {
		return fn(append([]byte(nil), good...))
	}
	cases := map[string][]byte{
		"too short":        good[:8],
		"truncated":        good[:size-1],
		"bad head magic":   mutate(func(b []byte) []byte { b[0] = 'X'; return b }),
		"bad tail magic":   mutate(func(b []byte) []byte { b[size-1] = 'X'; return b }),
		"index offset low": mutate(func(b []byte) []byte { binary.BigEndian.PutUint64(b[size-TrailerSize:], 1); return b }),
		"index offset past end": mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint64(b[size-TrailerSize:], uint64(size))
			return b
		}),
		"partial entry": mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint64(b[size-TrailerSize:], uint64(indexOffset+1))
			return b
		}),
		"entry past data": mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[indexOffset+hashSize+8:], uint32(size))
			return b
		}),
		"entry inside header": mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint64(b[indexOffset+hashSize:], 0)
			return b
		}),
		"empty entry": mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[indexOffset+hashSize+8:], 0)
			return b
		}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadIndex(bytes.NewReader(data), int64(len(data)))
			require.Error(t, err)
		})
	}
}

func TestReadIndexReportsReadErrors(t *testing.T) {
	p, err := Build(testEntries("alpha"))
	require.NoError(t, err)
	// A size larger than the data makes the trailer read fail.
	_, err = ReadIndex(bytes.NewReader(p.Bytes()), p.Size()+100)
	require.Error(t, err)
	_, err = ReadIndex(failingReaderAt{}, 100)
	require.Error(t, err)
}

type failingReaderAt struct{}

func (failingReaderAt) ReadAt([]byte, int64) (int, error) { return 0, errors.New("boom") }

func FuzzReadIndex(f *testing.F) {
	p, err := Build(testEntries("alpha", "bravo", "charlie"))
	require.NoError(f, err)
	f.Add(p.Bytes())
	f.Add([]byte(Magic + Magic))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		entries, err := ReadIndex(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		indexOffset := int64(binary.BigEndian.Uint64(data[len(data)-TrailerSize:]))
		for _, entry := range entries {
			if entry.Offset < int64(HeaderSize) || entry.Length <= 0 || entry.Offset+entry.Length > indexOffset {
				t.Fatalf("accepted entry outside the data region: %+v", entry)
			}
		}
	})
}

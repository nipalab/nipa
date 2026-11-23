package chunkbundle

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

func entry(content string) Entry {
	data := []byte(content)
	return Entry{Hash: chunker.Sum(data), Data: data}
}

func encode(t testing.TB, entries ...Entry) []byte {
	t.Helper()
	enc := NewEncoder(entries)
	body, err := io.ReadAll(enc)
	require.NoError(t, err)
	require.Equal(t, enc.Size(), int64(len(body)))
	return body
}

func limitsFor(body []byte) Limits {
	return Limits{Size: int64(len(body)), MaxCount: 1000}
}

func TestRoundTrip(t *testing.T) {
	entries := []Entry{entry("alpha"), entry("beta beta"), entry(string(bytes.Repeat([]byte{7}, 70_000)))}
	body := encode(t, entries...)
	require.Equal(t, EncodedSize(5, 9, 70_000), int64(len(body)))

	got, err := DecodeAll(bytes.NewReader(body), limitsFor(body))
	require.NoError(t, err)
	require.Equal(t, entries, got)

	// A reader that hands out one byte at a time decodes the same.
	got, err = DecodeAll(iotest.OneByteReader(bytes.NewReader(body)), limitsFor(body))
	require.NoError(t, err)
	require.Equal(t, entries, got)
}

func TestEncoderSmallReads(t *testing.T) {
	entries := []Entry{entry("one"), entry("two")}
	want := encode(t, entries...)
	got, err := io.ReadAll(iotest.OneByteReader(NewEncoder(entries)))
	require.NoError(t, err)
	require.Equal(t, want, got)

	n, err := NewEncoder(nil).Read(make([]byte, 8))
	require.Zero(t, n)
	require.ErrorIs(t, err, io.EOF)
}

func TestEncoderLayout(t *testing.T) {
	e := entry("xyz")
	body := encode(t, e)
	require.Equal(t, e.Hash[:], body[:32])
	require.Equal(t, uint32(3), binary.BigEndian.Uint32(body[32:36]))
	require.Equal(t, []byte("xyz"), body[36:])
}

func TestDecodeAllDropsDuplicates(t *testing.T) {
	a, b := entry("a"), entry("b")
	body := encode(t, a, b, a)
	got, err := DecodeAll(bytes.NewReader(body), limitsFor(body))
	require.NoError(t, err)
	require.Equal(t, []Entry{a, b}, got)
}

func header(hash domain.Hash, length uint32) []byte {
	out := make([]byte, HeaderSize)
	copy(out, hash[:])
	binary.BigEndian.PutUint32(out[32:], length)
	return out
}

func TestDecodeRejections(t *testing.T) {
	good := encode(t, entry("hello"), entry("world"))
	tampered := bytes.Clone(good)
	tampered[len(tampered)-1] ^= 1
	zero := header(chunker.Sum(nil), 0)
	big := append(header(domain.Hash{1}, 11), bytes.Repeat([]byte{1}, 11)...)
	past := append(header(chunker.Sum([]byte("abc")), 9), []byte("abc")...)

	cases := map[string]struct {
		body   []byte
		limits Limits
	}{
		"hash mismatch":       {tampered, limitsFor(tampered)},
		"truncated content":   {good[:len(good)-1], limitsFor(good)},
		"truncated header":    {good[:51], Limits{Size: 51, MaxCount: 10}},
		"short read":          {good[:20], limitsFor(good)},
		"trailing bytes":      {append(bytes.Clone(good), 0), limitsFor(good)},
		"zero length":         {append(zero, 0), Limits{Size: int64(len(zero)) + 1, MaxCount: 10}},
		"entry over max":      {big, Limits{Size: int64(len(big)), MaxCount: 10, MaxEntry: 10}},
		"length past the end": {past, Limits{Size: int64(len(past)), MaxCount: 10}},
		"too many entries":    {good, Limits{Size: int64(len(good)), MaxCount: 1}},
		"empty body":          {nil, Limits{Size: 0, MaxCount: 10}},
		"no entry allowed":    {good, Limits{Size: int64(len(good))}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeAll(bytes.NewReader(tc.body), tc.limits)
			require.ErrorIs(t, err, ErrInvalid)
			require.Nil(t, got)
		})
	}
}

func TestDecodeReaderError(t *testing.T) {
	boom := errors.New("boom")
	body := encode(t, entry("hello"))
	_, err := DecodeAll(io.MultiReader(bytes.NewReader(body[:10]), iotest.ErrReader(boom)), limitsFor(body))
	require.ErrorIs(t, err, boom)
	require.NotErrorIs(t, err, ErrInvalid)

	_, err = DecodeAll(io.MultiReader(bytes.NewReader(body), iotest.ErrReader(boom)), limitsFor(body))
	require.ErrorIs(t, err, boom)
}

func TestDecoderNextAfterEnd(t *testing.T) {
	body := encode(t, entry("once"))
	d, err := NewDecoder(bytes.NewReader(body), limitsFor(body))
	require.NoError(t, err)
	_, err = d.Next()
	require.NoError(t, err)
	_, err = d.Next()
	require.ErrorIs(t, err, io.EOF)
	_, err = d.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestDecodeDefaultMaxEntry(t *testing.T) {
	body := header(domain.Hash{1}, uint32(chunker.MaxChunkSize()+1))
	_, err := DecodeAll(bytes.NewReader(body), Limits{Size: chunker.MaxChunkSize() + 100, MaxCount: 1})
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorContains(t, err, "out of range")
}

func FuzzDecode(f *testing.F) {
	f.Add(encode(f, entry("hello"), entry("world")), 4)
	f.Add(encode(f, entry("a"), entry("a")), 1)
	f.Add([]byte{}, 1)
	f.Add(header(domain.Hash{}, 1), 1)
	f.Fuzz(func(t *testing.T, body []byte, maxCount int) {
		limits := Limits{Size: int64(len(body)), MaxCount: maxCount % 64, MaxEntry: 1 << 16}
		entries, err := DecodeAll(bytes.NewReader(body), limits)
		if err != nil {
			require.ErrorIs(t, err, ErrInvalid)
			require.Nil(t, entries)
			return
		}
		seen := map[domain.Hash]bool{}
		var total int64
		for _, e := range entries {
			require.Equal(t, chunker.Sum(e.Data), e.Hash)
			require.NotEmpty(t, e.Data)
			require.LessOrEqual(t, int64(len(e.Data)), limits.MaxEntry)
			require.False(t, seen[e.Hash])
			seen[e.Hash] = true
			total += int64(HeaderSize + len(e.Data))
		}
		require.LessOrEqual(t, len(entries), limits.MaxCount)
		require.LessOrEqual(t, total, int64(len(body)))
	})
}

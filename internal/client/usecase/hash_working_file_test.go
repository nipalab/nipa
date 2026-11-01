package usecase

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashWorkingFile_StreamingMatchesInMemory(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"big.txt": bytes.Repeat([]byte("line of text\n"), (inlineContentMax/13)+1024),
		"big.bin": append([]byte{0}, bytes.Repeat([]byte{7, 1, 2, 3}, inlineContentMax/4+1024)...),
	}
	for name, data := range cases {
		abs := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(abs, data, 0o644))

		want, wantChunks, wantEnc, err := chunkFile(name, data, "")
		require.NoError(t, err)

		got, err := hashWorkingFile(abs, name, int64(len(data)), "")
		require.NoError(t, err)
		require.Nil(t, got.inline)
		require.Equal(t, want, got.hash)
		require.Equal(t, wantChunks, got.chunks)
		require.Equal(t, wantEnc, got.encoding)
		require.Equal(t, int64(len(data)), got.size)
	}
}

func TestHashWorkingFile_InlineKeepsOnlyText(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "a.txt")
	bin := filepath.Join(dir, "a.bin")
	require.NoError(t, os.WriteFile(text, []byte("hello\n"), 0o644))
	require.NoError(t, os.WriteFile(bin, []byte{0, 1, 2}, 0o644))

	got, err := hashWorkingFile(text, "a.txt", 6, "")
	require.NoError(t, err)
	require.Equal(t, []byte("hello\n"), got.inline)

	got, err = hashWorkingFile(bin, "a.bin", 3, "")
	require.NoError(t, err)
	require.Nil(t, got.inline)
	require.True(t, got.isBinary)
}

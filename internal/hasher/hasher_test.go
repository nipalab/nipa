package hasher

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasherHashAndCompare(t *testing.T) {
	h := NewHasher(1)
	defer h.Close()

	hash, err := h.Hash("nipa")
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	require.NotEqual(t, "nipa", hash)
	fmt.Println(hash)

	require.True(t, h.Compare(hash, "nipa"))
	require.False(t, h.Compare(hash, "wrongpassword"))
}

func TestHasherMultipleWorkers(t *testing.T) {
	h := NewHasher(4)
	defer h.Close()

	passwords := []string{"one", "two", "three", "four", "five"}
	hashes := make([]string, len(passwords))
	for i, p := range passwords {
		hash, err := h.Hash(p)
		require.NoError(t, err)
		hashes[i] = hash
	}

	for i, p := range passwords {
		require.True(t, h.Compare(hashes[i], p))
	}
}

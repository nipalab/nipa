package storage

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/snow"
)

func TestChunkKey_RoundTrip(t *testing.T) {
	hash := chunker.Sum([]byte("key"))
	project := snow.ID(1234567890123)
	key := ChunkKey(project, hash)
	require.Equal(t, "p/"+project.Base36()+"/"+hash.String()[:2]+"/"+hash.String()[2:], key)
	require.Equal(t, "p/"+project.Base36(), ProjectPrefix(project))

	gotProject, gotHash, ok := ParseChunkKey(key)
	require.True(t, ok)
	require.Equal(t, project, gotProject)
	require.Equal(t, hash, gotHash)
}

func TestParseChunkKey_RejectsOtherShapes(t *testing.T) {
	hex := chunker.Sum([]byte("key")).String()
	for _, key := range []string{
		hex[:2] + "/" + hex[2:],                      // old global layout
		"p/abc/" + hex[:2],                           // too short
		"x/abc/" + hex[:2] + "/" + hex[2:],           // wrong root
		"p/abc/" + hex[:3] + "/" + hex[3:],           // wrong shard width
		"p/abc/" + hex[:2] + "/nothex",               // not a hash
		"p/ABC/" + hex[:2] + "/" + hex[2:],           // not canonical base36
		"p/!!/" + hex[:2] + "/" + hex[2:],            // not base36
		"p/abc/" + hex[:2] + "/" + hex[2:] + "/more", // too deep
	} {
		_, _, ok := ParseChunkKey(key)
		require.False(t, ok, key)
	}
}

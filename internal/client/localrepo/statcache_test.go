package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func statEntry(seed byte, size int64) domain.StatEntry {
	return domain.StatEntry{
		SizeBytes: size,
		MtimeNS:   1700000000000000000,
		Mode:      0o644,
		Hash:      serverDomain.Hash{seed},
		CachedAt:  1700000000000000001,
	}
}

func TestStatCache_RoundTrip(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{
		"assets/logo.png": statEntry(0xaa, 10),
		"readme.md":       statEntry(0xbb, 20),
	}))

	got, err := lr.LoadStatCache()
	require.NoError(t, err)
	require.Equal(t, map[string]domain.StatEntry{
		"assets/logo.png": statEntry(0xaa, 10),
		"readme.md":       statEntry(0xbb, 20),
	}, got)
	require.Equal(t, 2, countRows(t, lr.db, "stat_cache"))
}

func TestStatCache_UpsertOverwrites(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{"a.txt": statEntry(0x01, 1)}))
	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{"a.txt": statEntry(0x02, 2)}))

	got, err := lr.LoadStatCache()
	require.NoError(t, err)
	require.Equal(t, statEntry(0x02, 2), got["a.txt"])
	require.Equal(t, 1, countRows(t, lr.db, "stat_cache"))
}

func TestStatCache_Empty(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	got, err := lr.LoadStatCache()
	require.NoError(t, err)
	require.Empty(t, got)

	require.NoError(t, lr.SaveStatEntries(nil))
	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{}))
	require.Equal(t, 0, countRows(t, lr.db, "stat_cache"))
}

func TestStatCache_SweepKeepsTrackedFiles(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))
	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{
		"assets/logo.png": statEntry(0xaa, 10),
		"notes.txt":       statEntry(0xbb, 20),
	}))

	require.NoError(t, lr.SweepStatCache())

	got, err := lr.LoadStatCache()
	require.NoError(t, err)
	require.Contains(t, got, "assets/logo.png", "paths tracked by the snapshot survive the sweep")
	require.NotContains(t, got, "notes.txt")
}

func TestSaveTree_SweepsStatCache(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))
	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{
		"assets/logo.png": statEntry(0xaa, 10),
		"notes.txt":       statEntry(0xbb, 20),
	}))

	require.NoError(t, lr.SaveTree(treeFixture()))

	got, err := lr.LoadStatCache()
	require.NoError(t, err)
	require.Contains(t, got, "assets/logo.png")
	require.NotContains(t, got, "notes.txt")

	require.NoError(t, lr.SaveTree(nil))
	got, err = lr.LoadStatCache()
	require.NoError(t, err)
	require.Empty(t, got, "clearing the snapshot clears every stat row")
}

func TestStatCache_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	_, err := lr.LoadStatCache()
	require.Error(t, err)
	require.Error(t, lr.SaveStatEntries(map[string]domain.StatEntry{"a.txt": statEntry(0x01, 1)}))
	require.Error(t, lr.SweepStatCache())
}

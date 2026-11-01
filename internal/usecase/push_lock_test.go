package usecase

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func TestMarkLockableBinaries(t *testing.T) {
	files := []*domain.PushFile{
		{Path: "art/hero.PSD"},
		{Path: "art/readme.txt"},
		{Path: "blob.bin", IsBinary: true},
		nil,
	}

	markLockableBinaries(files)

	require.True(t, files[0].IsBinary, "known binary extension overrides a text claim")
	require.False(t, files[1].IsBinary)
	require.True(t, files[2].IsBinary, "the client flag can still add binaries")
}

func TestPlanPushLocks_HostileTextClaimStillNeedsLock(t *testing.T) {
	files := []*domain.PushFile{{Path: "art/hero.psd"}}
	markLockableBinaries(files)

	plan := planPushLocks(files, nil, map[string]bool{"art/hero.psd": false})

	require.Equal(t, []string{"art/hero.psd"}, plan.required)
}

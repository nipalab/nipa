package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

func TestRevertState_RoundTrip(t *testing.T) {
	lr := newTestLocalRepo(t)

	state := &domain.RevertState{
		Targets: []domain.CommitRef{
			{ID: "3", Hash: "aaa", Subject: "third"},
			{ID: "2", Hash: "bbb", Subject: "second"},
		},
		CurrentTreeHash:    "tree1",
		OriginalTreeHash:   "tree0",
		OriginalCommitID:   "4",
		OriginalCommitHash: "ccc",
		Mainline:           1,
		NoCommit:           true,
		Message:            "Revert \"third\"",
		Conflicts:          []string{"a.txt"},
	}
	require.NoError(t, lr.SaveRevertState(state))

	got, err := lr.LoadRevertState()
	require.NoError(t, err)
	require.Equal(t, state, got)
}

func TestRevertState_LoadMissing(t *testing.T) {
	lr := newTestLocalRepo(t)

	got, err := lr.LoadRevertState()
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestRevertState_Clear(t *testing.T) {
	lr := newTestLocalRepo(t)

	require.NoError(t, lr.SaveRevertState(&domain.RevertState{OriginalTreeHash: "tree0"}))
	require.NoError(t, lr.ClearRevertState())

	got, err := lr.LoadRevertState()
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, lr.ClearRevertState())
}

func TestRevertState_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()

	_, err := lr.LoadRevertState()
	require.Error(t, err)
	require.Error(t, lr.SaveRevertState(&domain.RevertState{}))
	require.Error(t, lr.ClearRevertState())
}

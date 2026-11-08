package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLock_SharedHoldersCoexist(t *testing.T) {
	lr := newTestLocalRepo(t)
	other := NewLocalRepo()
	require.NoError(t, other.Init(lr.target))
	t.Cleanup(func() { _ = other.Close() })

	first, err := lr.LockShared()
	require.NoError(t, err)
	second, err := other.LockShared()
	require.NoError(t, err)

	_, acquired, err := lr.TryLockExclusive()
	require.NoError(t, err)
	require.False(t, acquired, "exclusive must fail fast while shared holders exist")

	first()
	_, acquired, err = other.TryLockExclusive()
	require.NoError(t, err)
	require.False(t, acquired)

	second()
	second()
	release, acquired, err := lr.TryLockExclusive()
	require.NoError(t, err)
	require.True(t, acquired)

	_, acquired, err = other.TryLockExclusive()
	require.NoError(t, err)
	require.False(t, acquired, "exclusive holders exclude each other")
	release()

	release, acquired, err = other.TryLockExclusive()
	require.NoError(t, err)
	require.True(t, acquired)
	release()
}

func TestLock_SharedWaitsForExclusive(t *testing.T) {
	lr := newTestLocalRepo(t)
	release, acquired, err := lr.TryLockExclusive()
	require.NoError(t, err)
	require.True(t, acquired)

	got := make(chan func(), 1)
	go func() {
		unlock, err := lr.LockShared()
		if err == nil {
			got <- unlock
		}
	}()
	select {
	case <-got:
		t.Fatal("shared lock granted while exclusive is held")
	default:
	}
	release()
	unlock := <-got
	unlock()
}

func TestLock_NotInitialized(t *testing.T) {
	_, err := NewLocalRepo().LockShared()
	require.Error(t, err)
	_, _, err = NewLocalRepo().TryLockExclusive()
	require.Error(t, err)
}

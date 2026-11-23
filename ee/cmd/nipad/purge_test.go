package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/storage"
)

func TestPurgeRetention(t *testing.T) {
	require.Zero(t, purgeRetention(&config.Config{}))
	require.Zero(t, purgeRetention(&config.Config{StoragePurgeRetentionDays: -1}))
	require.Equal(t, 30*24*time.Hour, purgeRetention(&config.Config{StoragePurgeRetentionDays: 30}))
}

func TestNewPurgeJob(t *testing.T) {
	job, err := newPurgeJob(&config.Config{}, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, job, "retention 0 disables the purge")

	job, err = newPurgeJob(&config.Config{StoragePurgeRetentionDays: 7}, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, job)

	ledger := storageusage.NewLedger(nil, storageusage.Config{})
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	job, err = newPurgeJob(&config.Config{StoragePurgeRetentionDays: 7, StoragePurgeBatchSize: 5}, nil, ledger, nil, store)
	require.NoError(t, err)
	require.NotNil(t, job)
}

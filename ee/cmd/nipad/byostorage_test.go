package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
)

func TestBuildBYOStorage(t *testing.T) {
	setup, err := buildBYOStorage(&config.Config{}, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, setup, "no key wires nothing")
	require.False(t, setup.enabled())
	require.Nil(t, setup.runOptions(serverapp.Usecases{}))
	deps := serverapp.UsecaseDeps{}
	setup.wire(&deps)
	require.Nil(t, deps.ChunkStores)
	require.Nil(t, deps.ProjectStorage)

	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	_, err = buildBYOStorage(&config.Config{BYOStorageEncryptionKey: key}, nil, nil, nil, nil)
	require.ErrorContains(t, err, "SAAS_MODE")

	cfg := &config.Config{BYOStorageEncryptionKey: key, SaaSMode: true, BYOStorageAllowPrivate: true}
	_, err = buildBYOStorage(cfg, nil, nil, nil, nil)
	require.ErrorContains(t, err, "storage ledger")

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	setup, err = buildBYOStorage(cfg, nil, node, nil, storageusage.NewLedger(nil, storageusage.Config{}))
	require.NoError(t, err)
	require.True(t, setup.enabled())
	setup.wire(&deps)
	require.Same(t, setup.resolver, deps.ChunkStores)
	require.Same(t, setup.service, deps.ProjectStorage)
	require.Len(t, setup.runOptions(serverapp.Usecases{}), 1)
}

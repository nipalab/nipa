package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/grpc"
)

func TestUploadOptions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvUploadWorkers, "")
	t.Setenv(config.EnvUploadWindowMiB, "")
	opts, err := uploadOptions()
	require.NoError(t, err)
	require.Empty(t, opts, "nothing pinned keeps the client defaults")

	t.Setenv(config.EnvUploadWorkers, "8")
	t.Setenv(config.EnvUploadWindowMiB, "1000")
	opts, err = uploadOptions()
	require.NoError(t, err)
	require.Len(t, opts, 2)
	client := grpc.NewClient(grpc.NewTransport(), nil, opts...)
	require.Equal(t, config.MaxUploadWindowMiB<<20, client.UploadWindowBytes(), "the window is clamped")

	t.Setenv(config.EnvUploadWindowMiB, "lots")
	_, err = uploadOptions()
	require.ErrorContains(t, err, config.EnvUploadWindowMiB)

	t.Setenv(config.EnvUploadWorkers, "none")
	_, err = uploadOptions()
	require.ErrorContains(t, err, config.EnvUploadWorkers)
}

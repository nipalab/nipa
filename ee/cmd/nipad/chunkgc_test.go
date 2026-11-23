package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/chunkgc"
	"github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

func TestChunkGCSetup(t *testing.T) {
	mode, grace, err := chunkGCSetup(&config.Config{ChunkPresignTTLSeconds: 3600})
	require.NoError(t, err)
	require.Equal(t, chunkgc.ModeDryRun, mode)
	require.Equal(t, chunkgc.DefaultGrace, grace)

	mode, grace, err = chunkGCSetup(&config.Config{ChunkGCMode: "on", ChunkGCGraceHours: 48, ChunkPresignTTLSeconds: 3600})
	require.NoError(t, err)
	require.Equal(t, chunkgc.ModeOn, mode)
	require.Equal(t, 48*time.Hour, grace)

	_, _, err = chunkGCSetup(&config.Config{ChunkGCMode: "maybe"})
	require.Error(t, err)
	_, _, err = chunkGCSetup(&config.Config{ChunkGCGraceHours: 1, ChunkPresignTTLSeconds: 3600})
	require.ErrorContains(t, err, "twice")

	// A disabled collector never sweeps, so the grace/TTL rule does not apply.
	mode, _, err = chunkGCSetup(&config.Config{ChunkGCMode: "off", ChunkPresignTTLSeconds: 86400 * 7})
	require.NoError(t, err)
	require.Equal(t, chunkgc.ModeOff, mode)
}

type gcObjects struct{ storage.ChunkStore }

func (gcObjects) DeleteChunk(context.Context, snow.ID, domain.Hash) error { return nil }
func (gcObjects) ListChunks(context.Context, func(s3.StoredChunk) error) error {
	return nil
}
func (gcObjects) DeletePack(context.Context, string) error { return nil }

func TestChunkGCWiring(t *testing.T) {
	require.Nil(t, newChunkGCGuard(chunkgc.ModeOff, nil))
	require.NotNil(t, newChunkGCGuard(chunkgc.ModeDryRun, nil))

	job, err := newChunkGCJob(&config.Config{}, chunkgc.ModeOff, time.Hour, nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, job)

	_, err = newChunkGCJob(&config.Config{}, chunkgc.ModeOn, time.Hour, nil, storage.ChunkStore(nil), nil)
	require.Error(t, err, "a store that cannot list and delete runs no collector")

	job, err = newChunkGCJob(&config.Config{ChunkGCOrphanScan: true}, chunkgc.ModeOn, time.Hour, nil, gcObjects{}, nil)
	require.NoError(t, err)
	require.NotNil(t, job)
}

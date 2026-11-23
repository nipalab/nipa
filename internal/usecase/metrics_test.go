package usecase

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

func scrapeUsecaseMetrics(t *testing.T, metrics *obs.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	return rr.Body.String()
}

func TestPushMetrics(t *testing.T) {
	uc, perm, repo, pushRepo, ctx := newPushFixture(t)
	metrics := obs.NewMetrics()
	uc = uc.WithMetrics(metrics)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).Times(2)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main"}, nil)
	chA, fhA := chunkAndFileHash(t, "aaa")
	fileA := &domain.PushFile{Path: "a.txt", Mode: 0o644, SizeBytes: 3, FileHash: fhA, ChunkHashes: []domain.Hash{chA}}
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil)

	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{fileA}, nil, "", "")
	require.NoError(t, err)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "gone").
		Return(nil, domain.NewErrorNotFound("branch not found"))
	_, err = uc.Push(ctx, snow.ID(1), "gone", "", "msg", nil, nil, "", "")
	require.Error(t, err)

	body := scrapeUsecaseMetrics(t, metrics)
	require.Contains(t, body, `nipa_push_total{result="ok"} 1`)
	require.Contains(t, body, `nipa_push_total{result="error"} 1`)
}

func TestChunkMetrics(t *testing.T) {
	uc, _, _ := newChunkFixture(t)
	metrics := obs.NewMetrics()
	uc = uc.WithMetrics(metrics)

	ctx := context.Background()
	data := []byte("hello chunk")
	hash := chunker.Sum(data)

	body := bundleOf(t, data)
	_, err := uc.StoreBundle(ctx, snow.ID(1), bytes.NewReader(body), int64(len(body)), 1)
	require.NoError(t, err)

	got, err := uc.Download(ctx, snow.ID(1), hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	missing := chunker.Sum([]byte("missing"))
	missingHashes, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash, missing}, nil)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{missing}, missingHashes)

	_, err = uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	require.NoError(t, err)

	tampered := bytes.Clone(body)
	tampered[len(tampered)-1] ^= 1
	_, err = uc.StoreBundle(ctx, snow.ID(1), bytes.NewReader(tampered), int64(len(tampered)), 1)
	require.Error(t, err)

	scraped := scrapeUsecaseMetrics(t, metrics)
	require.Contains(t, scraped, `nipa_chunk_ops_total{op="put",result="stored"} 1`)
	require.Contains(t, scraped, `nipa_chunk_ops_total{op="get",result="ok"} 1`)
	require.Contains(t, scraped, `nipa_chunk_ops_total{op="confirm",result="partial"} 1`)
	require.Contains(t, scraped, `nipa_chunk_ops_total{op="presign_upload",result="ok"} 1`)
	require.Contains(t, scraped, `nipa_chunk_verify_failures_total{stage="store"} 1`)
	require.Contains(t, scraped, `nipa_chunk_bytes_total{op="put"} 11`)
	require.Contains(t, scraped, `nipa_chunk_bytes_total{op="get"} 11`)
	require.Contains(t, scraped, `nipa_chunk_bytes_total{op="confirm"} 11`)
}

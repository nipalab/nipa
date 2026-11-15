package grpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
)

const readOnlyBody = `{"error":"server is in maintenance (read-only): storage move","maintenance":{"scope":"server","mode":"read_only","message":"storage move","ends_at":"2026-11-15T14:00:00Z"}}`

func TestUploadChunks_DoesNotRetryMaintenance(t *testing.T) {
	h := testChunkHash(0x31)
	srv := newChunkTestServer()
	srv.failPuts[h.String()] = 100
	srv.failPutStatus = http.StatusServiceUnavailable
	srv.maintenanceBody = readOnlyBody
	c := newDownloadClient(t, srv, fastRetry(5))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h, Data: []byte("x")},
	})
	require.True(t, domain.IsMaintenance(err), "got %v", err)
	require.Equal(t, 1, srv.putAttemptsFor(h.String()))
	require.Contains(t, err.Error(), "server is in maintenance (read-only): storage move")
	_, ok := maintenance.FromGRPC(err)
	require.True(t, ok, "the refusal carries the gRPC form for the daemon")
}

func TestDownloadChunks_DoesNotRetryMaintenance(t *testing.T) {
	h := testChunkHash(0x32)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("payload"))
	srv.failGets[h.String()] = 100
	srv.failGetStatus = http.StatusServiceUnavailable
	srv.maintenanceBody = "not json"
	c := newDownloadClient(t, srv, fastRetry(5))

	err := c.DownloadChunks(context.Background(), downloadScope(), []serverDomain.Hash{h}, newDeliveries().sink)
	require.True(t, domain.IsMaintenance(err), "got %v", err)
	require.Equal(t, 1, srv.attemptsFor(h.String()))
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Equal(t, "read_only", de.Maintenance.Mode, "an unreadable body falls back to the header")
	require.Equal(t, "server", de.Maintenance.Scope)
}

func TestHTTPMaintenanceError(t *testing.T) {
	res := func(code int, header, body string) *http.Response {
		r := &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
		if header != "" {
			r.Header.Set(maintenanceHeader, header)
		}
		return r
	}
	_, ok := httpMaintenanceError(res(http.StatusServiceUnavailable, "", readOnlyBody))
	require.False(t, ok, "a 503 without the header is a plain outage")
	_, ok = httpMaintenanceError(res(http.StatusForbidden, "server;full", readOnlyBody))
	require.False(t, ok)

	refused, ok := httpMaintenanceError(res(http.StatusServiceUnavailable, "server;read_only", readOnlyBody))
	require.True(t, ok)
	require.Equal(t, 503, refused.Code)
	require.Equal(t, "storage move", refused.Maintenance.Message)
	require.NotNil(t, refused.Maintenance.EndsAt)

	require.False(t, httpRetryHint(res(http.StatusServiceUnavailable, "project;full", "")).retryable)
	require.True(t, httpRetryHint(res(http.StatusServiceUnavailable, "", "")).retryable)

	_, limited := rateLimitWait(maintenance.NewError(maintenance.Window{Mode: maintenance.ReadOnly}, maintenance.ScopeServer).GRPCError(time.Now()))
	require.False(t, limited, "unary calls only repeat rate limit refusals")
}

func TestGetMaintenanceStatus(t *testing.T) {
	ends := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	srv := newChunkTestServer()
	srv.maintStatus = &pb.GetMaintenanceStatusResponse{
		Server:  &pb.MaintenanceWindow{Mode: "read_only", Active: true, Message: "move", StartsAt: timestamppb.New(ends.Add(-time.Hour)), EndsAt: timestamppb.New(ends)},
		Project: &pb.MaintenanceWindow{Mode: "full", Org: "org", Project: "org/proj"},
		Org:     &pb.MaintenanceWindow{},
		Source:  "db",
	}
	c := newDownloadClient(t, srv, fastRetry(1))

	st, err := c.GetMaintenanceStatus(context.Background(), "org", "proj")
	require.NoError(t, err)
	require.Equal(t, []string{"Bearer tok"}, srv.maintAuth, "the stored token is sent so the project window is filled")
	require.Equal(t, "db", st.Source)
	require.Nil(t, st.Org, "an empty window is no window")
	require.Equal(t, &domain.MaintenanceWindow{Mode: "read_only", Active: true, Message: "move", StartsAt: ends.Add(-time.Hour), EndsAt: &ends}, st.Server)
	require.Equal(t, "org/proj", st.Project.Project)
	require.True(t, st.Project.StartsAt.IsZero())
	require.Nil(t, st.Project.EndsAt)

	c.session = &stubSession{getTokenErr: errors.New("no token")}
	_, err = c.GetMaintenanceStatus(context.Background(), "", "")
	require.NoError(t, err)
	require.Empty(t, srv.maintAuth, "without a stored token the public call goes out anonymous")
}

func TestGetMaintenanceStatus_NotConnected(t *testing.T) {
	_, err := NewTransport().GetMaintenanceStatus(context.Background(), "", "")
	require.Error(t, err)
}

package obs

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestID_GeneratesAndEchoes(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil))

	id := rr.Header().Get(HeaderRequestID)
	require.Len(t, id, 32)
	require.Equal(t, id, seen)
	_, err := hex.DecodeString(id)
	require.NoError(t, err)
}

func TestRequestID_HonorsIncoming(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil)
	req.Header.Set(HeaderRequestID, "proxy-trace-42")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, "proxy-trace-42", rr.Header().Get(HeaderRequestID))
	require.Equal(t, "proxy-trace-42", seen)
}

func TestRequestID_RejectsInvalidIncoming(t *testing.T) {
	for i, value := range []string{strings.Repeat("a", 200), "bad\nid"} {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(HeaderRequestID, value)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			got := rr.Header().Get(HeaderRequestID)
			require.NotEmpty(t, got)
			require.NotEqual(t, value, got)
		})
	}
}

func TestLog_CarriesRequestID(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Log(r.Context()).Info("inside")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderRequestID, "req-7")
	h.ServeHTTP(httptest.NewRecorder(), req)

	require.Contains(t, buf.String(), "request_id=req-7")
	require.Contains(t, buf.String(), "inside")
}

func TestLog_OutsideRequest(t *testing.T) {
	require.NotNil(t, Log(context.Background()))
	require.Equal(t, slog.Default(), Log(context.Background()))
	var nilCtx context.Context
	require.NotNil(t, Log(nilCtx))
}

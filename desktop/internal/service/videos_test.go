package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

func mediaFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "preview", "testdata", "media", name))
	require.NoError(t, err)
	return string(data)
}

func TestService_VideoDiffServesBothSides(t *testing.T) {
	mp4 := mediaFixture(t, "clip.mp4")
	svc, _ := newModels(t, map[string]string{"cut/old.webm": mediaFixture(t, "clip.webm"), "cut/intro.mp4": mp4, "cut/raw.avi": "RIFF"})
	out, err := svc.VideoDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "cut/old.webm"}, &ImageRef{Source: "working", Path: "cut/intro.mp4"})
	require.NoError(t, err)
	require.Equal(t, "webm", out.Old.Stats.Container)
	require.Equal(t, "VP9", out.Old.Stats.VideoCodec)
	require.Equal(t, "H.264", out.New.Stats.VideoCodec)
	require.Equal(t, 320, out.New.Stats.Width)
	require.EqualValues(t, len(mp4), out.New.Size)
	require.True(t, strings.HasPrefix(out.New.URL, MediaPrefix+out.New.Media+"/intro.mp4"))

	handler := MediaMiddleware(svc)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	req := httptest.NewRequest(http.MethodGet, out.New.URL, nil)
	req.Header.Set("Range", "bytes=4-11")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusPartialContent, rec.Code)
	require.Equal(t, "video/mp4", rec.Header().Get("Content-Type"))
	body, _ := io.ReadAll(rec.Body)
	require.Equal(t, mp4[4:12], string(body))

	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/index.html", http.StatusTeapot},
		{http.MethodGet, MediaPrefix + "nope/x.mp4", http.StatusNotFound},
		{http.MethodPost, out.New.URL, http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, tc.code, rec.Code, tc.path)
	}

	raw, err := svc.VideoDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "cut/raw.avi"})
	require.NoError(t, err)
	require.Nil(t, raw.New.Stats)
	require.Contains(t, raw.New.StatsError, "unsupported")
	require.NotEmpty(t, raw.New.URL)
}

func TestService_VideoDiffLimitsAndErrors(t *testing.T) {
	svc, d := newModels(t, map[string]string{"big.mp4": "x"})
	d.sizes["big.mp4"] = maxVideoBytes + 1
	out, err := svc.VideoDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "big.mp4"})
	require.NoError(t, err)
	require.True(t, out.New.TooLarge)
	require.Empty(t, out.New.URL)

	_, err = svc.VideoDiff(ctx, "/r", nil, nil)
	require.Error(t, err)
	_, err = svc.VideoDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "gone.mp4"})
	require.ErrorIs(t, err, errBlobMissing)

	d.failures["bad.mp4"] = &daemonpb.OpFailure{Code: 500, Message: "boom"}
	_, err = svc.VideoDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "bad.mp4"})
	require.EqualError(t, err, "boom")

	d.err = status.Error(codes.Internal, "no stream")
	_, err = svc.VideoDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "big.mp4"})
	require.Error(t, err)
}

func TestService_MediaStoreEvictsAndOpens(t *testing.T) {
	svc, _ := newModels(t, map[string]string{"a.mp4": "data"})
	var opened []string
	svc.OpenFunc = func(p string) error {
		opened = append(opened, p)
		return nil
	}
	var first *VideoSide
	for i := 0; i <= maxMediaFiles; i++ {
		out, err := svc.VideoDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "a.mp4"})
		require.NoError(t, err)
		if first == nil {
			first = out.New
		}
	}
	_, ok := svc.media.lookup(first.Media)
	require.False(t, ok)
	require.Error(t, svc.OpenMedia(first.Media))

	last := svc.media.order[len(svc.media.order)-1]
	require.NoError(t, svc.OpenMedia(last))
	require.Len(t, opened, 1)
	svc.OpenFunc = nil
	require.Error(t, svc.OpenMedia(last))

	dir := svc.media.dir
	require.NoError(t, svc.ServiceShutdown())
	_, err := os.Stat(dir)
	require.True(t, os.IsNotExist(err))
	require.NoError(t, svc.media.close())
}

package version

import (
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubBuildInfo(t *testing.T, bi *debug.BuildInfo, ok bool) {
	t.Helper()
	prevVersion, prevRevision, prevRead := Version, Revision, readBuildInfo
	t.Cleanup(func() {
		Version, Revision, readBuildInfo = prevVersion, prevRevision, prevRead
	})
	Version, Revision = "", ""
	readBuildInfo = func() (*debug.BuildInfo, bool) { return bi, ok }
}

func TestResolveLdflagsWin(t *testing.T) {
	stubBuildInfo(t, &debug.BuildInfo{
		Main:     debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "buildinfo-sha"}},
	}, true)
	Version, Revision = "v1.2.3", "abc1234"

	info := Resolve()

	assert.Equal(t, "v1.2.3", info.Version)
	assert.Equal(t, "abc1234", info.Revision)
	assert.Equal(t, runtime.Version(), info.GoVersion)
}

func TestResolveFallsBackToModuleVersion(t *testing.T) {
	stubBuildInfo(t, &debug.BuildInfo{
		Main:     debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "buildinfo-sha"}},
	}, true)

	info := Resolve()

	assert.Equal(t, "v1.2.3", info.Version)
	assert.Equal(t, "buildinfo-sha", info.Revision)
}

func TestResolveFallsBackToVCSRevision(t *testing.T) {
	stubBuildInfo(t, &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
		},
	}, true)

	info := Resolve()

	assert.Equal(t, "dev-0123456789ab", info.Version)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", info.Revision)
}

func TestResolveMarksDirty(t *testing.T) {
	stubBuildInfo(t, &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.modified", Value: "true"},
		},
	}, true)

	assert.Equal(t, "dev-abc123-dirty", Resolve().Version)
}

func TestResolveWithoutBuildInfo(t *testing.T) {
	stubBuildInfo(t, nil, false)

	info := Resolve()

	assert.Equal(t, "dev", info.Version)
	assert.Empty(t, info.Revision)
}

func TestHandlerServesJSON(t *testing.T) {
	stubBuildInfo(t, nil, false)
	Version = "v1.2.3"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/version", nil)
	Handler().ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var info Info
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.Equal(t, "v1.2.3", info.Version)
	assert.Equal(t, runtime.Version(), info.GoVersion)
}

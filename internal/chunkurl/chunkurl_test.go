package chunkurl

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

const (
	testKey     = "signing-key"
	testOrg     = "acme"
	testProject = "game"
	testHash    = "ab12cd34ef56"
)

func parsePath(t *testing.T, path string) (string, string, string, url.Values) {
	t.Helper()
	u, err := url.Parse(path)
	require.NoError(t, err)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	require.Len(t, parts, 5)
	require.Equal(t, "api", parts[0])
	require.Equal(t, "chunks", parts[1])
	return parts[2], parts[3], parts[4], u.Query()
}

func TestUploadPathRoundTrip(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	path := UploadPath(testKey, testOrg, testProject, testHash, 10, exp)

	org, project, hash, query := parsePath(t, path)

	params, err := ParseParams(query)
	require.NoError(t, err)
	require.Equal(t, OpUpload, params.Op)
	require.Equal(t, int64(10), params.Size)
	require.Equal(t, exp, params.Exp)
	require.NoError(t, Verify(testKey, org, project, hash, params, time.Now()))
}

func TestDownloadPathRoundTrip(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	path := DownloadPath(testKey, testOrg, testProject, testHash, exp)

	org, project, hash, query := parsePath(t, path)

	params, err := ParseParams(query)
	require.NoError(t, err)
	require.Equal(t, OpDownload, params.Op)
	require.Equal(t, int64(0), params.Size)
	require.Equal(t, exp, params.Exp)
	require.NoError(t, Verify(testKey, org, project, hash, params, time.Now()))
}

func TestVerifyTampered(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	path := UploadPath(testKey, testOrg, testProject, testHash, 10, exp)
	_, _, _, query := parsePath(t, path)
	params, err := ParseParams(query)
	require.NoError(t, err)

	tests := []struct {
		name    string
		key     string
		org     string
		project string
		hash    string
		mutate  func(p *Params)
	}{
		{name: "wrong key", key: "other-key", org: testOrg, project: testProject, hash: testHash},
		{name: "wrong org", key: testKey, org: "other", project: testProject, hash: testHash},
		{name: "wrong project", key: testKey, org: testOrg, project: "other", hash: testHash},
		{name: "wrong hash", key: testKey, org: testOrg, project: testProject, hash: "ffff"},
		{name: "wrong op", key: testKey, org: testOrg, project: testProject, hash: testHash, mutate: func(p *Params) { p.Op = OpDownload }},
		{name: "wrong size", key: testKey, org: testOrg, project: testProject, hash: testHash, mutate: func(p *Params) { p.Size = 11 }},
		{name: "wrong expiry", key: testKey, org: testOrg, project: testProject, hash: testHash, mutate: func(p *Params) { p.Exp = p.Exp + 1 }},
		{name: "wrong sig", key: testKey, org: testOrg, project: testProject, hash: testHash, mutate: func(p *Params) { p.Sig = strings.Repeat("0", 64) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := params
			if tt.mutate != nil {
				tt.mutate(&p)
			}
			err := Verify(tt.key, tt.org, tt.project, tt.hash, p, time.Now())
			require.Error(t, err)
			require.True(t, domain.IsErrorNoPermission(err))
		})
	}
}

func TestVerifyExpired(t *testing.T) {
	exp := time.Now().Add(-time.Second).Unix()
	path := DownloadPath(testKey, testOrg, testProject, testHash, exp)
	_, _, _, query := parsePath(t, path)
	params, err := ParseParams(query)
	require.NoError(t, err)

	err = Verify(testKey, testOrg, testProject, testHash, params, time.Now())
	require.Error(t, err)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestParseParamsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "missing op", query: "exp=1&sig=abc"},
		{name: "unknown op", query: "op=delete&exp=1&sig=abc"},
		{name: "missing expiry", query: "op=get&sig=abc"},
		{name: "bad expiry", query: "op=get&exp=abc&sig=abc"},
		{name: "bad size", query: "op=put&exp=1&size=abc&sig=abc"},
		{name: "negative size", query: "op=put&exp=1&size=-1&sig=abc"},
		{name: "missing sig", query: "op=get&exp=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := url.ParseQuery(tt.query)
			require.NoError(t, err)
			_, err = ParseParams(query)
			require.Error(t, err)
		})
	}
}

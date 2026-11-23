package chunkurl

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func parseBundlePath(t *testing.T, path string) (string, string, BundleParams) {
	t.Helper()
	u, err := url.Parse(path)
	require.NoError(t, err)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	require.Equal(t, []string{"api", "chunk-bundles"}, parts[:2])
	require.Len(t, parts, 4)
	params, err := ParseBundleParams(u.Query())
	require.NoError(t, err)
	return parts[2], parts[3], params
}

func TestBundlePathRoundTrip(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	org, project, params := parseBundlePath(t, BundlePath(testKey, testOrg, testProject, 64<<20, 1000, exp))
	require.Equal(t, testOrg, org)
	require.Equal(t, testProject, project)
	require.Equal(t, int64(64<<20), params.MaxSize)
	require.Equal(t, 1000, params.MaxCount)
	require.Equal(t, exp, params.Exp)
	require.NoError(t, VerifyBundle(testKey, org, project, params, time.Now()))
}

func TestBundlePathEscapes(t *testing.T) {
	path := BundlePath(testKey, "a b", "c/d", 10, 1, 1)
	require.True(t, strings.HasPrefix(path, "/api/chunk-bundles/a%20b/c%2Fd?"), path)
}

func TestVerifyBundleRejections(t *testing.T) {
	now := time.Now()
	exp := now.Add(time.Hour).Unix()
	_, _, params := parseBundlePath(t, BundlePath(testKey, testOrg, testProject, 100, 10, exp))

	tests := []struct {
		name    string
		key     string
		org     string
		project string
		mutate  func(p *BundleParams)
		now     time.Time
	}{
		{name: "wrong key", key: "other-key", org: testOrg, project: testProject},
		{name: "wrong org", key: testKey, org: "other", project: testProject},
		{name: "wrong project", key: testKey, org: testOrg, project: "other"},
		{name: "wrong size", key: testKey, org: testOrg, project: testProject, mutate: func(p *BundleParams) { p.MaxSize++ }},
		{name: "wrong count", key: testKey, org: testOrg, project: testProject, mutate: func(p *BundleParams) { p.MaxCount++ }},
		{name: "wrong expiry", key: testKey, org: testOrg, project: testProject, mutate: func(p *BundleParams) { p.Exp++ }},
		{name: "wrong sig", key: testKey, org: testOrg, project: testProject, mutate: func(p *BundleParams) { p.Sig = strings.Repeat("0", 64) }},
		{name: "expired", key: testKey, org: testOrg, project: testProject, now: now.Add(2 * time.Hour)},
		{
			name: "chunk url signature", key: testKey, org: testOrg, project: testProject,
			mutate: func(p *BundleParams) { p.Sig = Sign(testKey, testOrg, testProject, "", "get", p.MaxSize, p.Exp) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := params
			if tt.mutate != nil {
				tt.mutate(&p)
			}
			at := tt.now
			if at.IsZero() {
				at = now
			}
			err := VerifyBundle(tt.key, tt.org, tt.project, p, at)
			require.True(t, domain.IsErrorNoPermission(err), "%v", err)
		})
	}
}

func TestBundleSignatureNotAChunkSignature(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	sig := SignBundle(testKey, testOrg, testProject, 0, 1, exp)
	err := Verify(testKey, testOrg, testProject, testHash, Params{Op: OpDownload, Exp: exp, Sig: sig}, time.Now())
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestParseBundleParamsInvalid(t *testing.T) {
	for name, query := range map[string]string{
		"missing size":  "count=1&exp=1&sig=a",
		"zero size":     "size=0&count=1&exp=1&sig=a",
		"bad count":     "size=1&count=x&exp=1&sig=a",
		"zero count":    "size=1&count=0&exp=1&sig=a",
		"bad expiry":    "size=1&count=1&exp=x&sig=a",
		"missing sig":   "size=1&count=1&exp=1",
		"negative size": "size=-5&count=1&exp=1&sig=a",
	} {
		t.Run(name, func(t *testing.T) {
			values, err := url.ParseQuery(query)
			require.NoError(t, err)
			_, err = ParseBundleParams(values)
			require.Error(t, err)
		})
	}
}

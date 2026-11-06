package usecase

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func marketingMatcherPaths(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/caddy/Caddyfile")
	require.NoError(t, err)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 2 && fields[0] == "@marketing" && fields[1] == "path" {
			return fields[2:]
		}
	}
	t.Fatal("deploy/caddy/Caddyfile has no @marketing path matcher")
	return nil
}

func TestMarketingRoutesAreReservedOrgSlugs(t *testing.T) {
	paths := marketingMatcherPaths(t)
	require.NotEmpty(t, paths)
	for _, p := range paths {
		segment := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0]
		if segment == "" || strings.Contains(segment, ".") || strings.HasPrefix(segment, "_") {
			continue
		}
		require.True(t, isReservedOrgSlug(segment), "marketing path %q needs %q in reservedOrgSlugs (and web/src/slug.ts)", p, segment)
	}
}

func TestMarketingMatcherDoesNotShadowAPIPaths(t *testing.T) {
	for _, p := range marketingMatcherPaths(t) {
		for _, api := range []string{"/auth", "/api", "/docs", "/metrics", "/debug"} {
			require.False(t, p == api || strings.HasPrefix(p, api+"/"), "marketing path %q shadows the app path %q", p, api)
		}
	}
}

func TestReservedSlugHelpers(t *testing.T) {
	require.True(t, isReservedOrgSlug("support"))
	require.False(t, isReservedOrgSlug("acme"))
	require.True(t, isReservedProjectSlug("new"))
	require.False(t, isReservedProjectSlug("game"))
}

package domain

import (
	"strings"
	"testing"
)

// FuzzParseNipaUrl checks that parsing never panics and that a parsed URL is
// internally consistent.
func FuzzParseNipaUrl(f *testing.F) {
	f.Add("http://localhost:6745/acme/game")
	f.Add("https://nipalab.com/org/project/assets/textures")
	f.Add("ftp://host/org/project")
	f.Add("http://host/onlyorg")
	f.Add("http://host//project")
	f.Add("http://[::1]:80/a/b/c/d")
	f.Add("")

	f.Fuzz(func(t *testing.T, raw string) {
		parsed, err := ParseNipaUrl(raw)
		if err != nil {
			return
		}
		if parsed.Url != raw {
			t.Fatalf("Url = %q, want the input %q", parsed.Url, raw)
		}
		if !strings.HasPrefix(parsed.Origin, "http://") && !strings.HasPrefix(parsed.Origin, "https://") {
			t.Fatalf("Origin %q has no http(s) scheme", parsed.Origin)
		}
		if !strings.HasSuffix(parsed.Origin, "://"+parsed.Host) {
			t.Fatalf("Origin %q does not end with host %q", parsed.Origin, parsed.Host)
		}
		if strings.Contains(parsed.Org, "/") || strings.Contains(parsed.Project, "/") {
			t.Fatalf("org %q or project %q contains a separator", parsed.Org, parsed.Project)
		}
	})
}

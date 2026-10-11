package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Version and Revision are injected at build time through
// -ldflags "-X github.com/nipalab/nipa/internal/version.Version=...".
var (
	Version  = ""
	Revision = ""
)

// Info describes a nipa build.
type Info struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	GoVersion string `json:"go_version"`
}

// String renders the info as a single line, e.g.
// "v0.1.0 (revision abc1234, go1.25.1)".
func (i Info) String() string {
	var parts []string
	if i.Revision != "" {
		parts = append(parts, "revision "+i.Revision)
	}
	if i.GoVersion != "" {
		parts = append(parts, i.GoVersion)
	}
	if len(parts) == 0 {
		return i.Version
	}
	return i.Version + " (" + strings.Join(parts, ", ") + ")"
}

// Flag reports whether the program arguments request the build version
// (-version, --version or -v).
func Flag(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-version", "--version", "-v":
			return true
		}
	}
	return false
}

var readBuildInfo = debug.ReadBuildInfo

// Resolve reports the version of the running binary. Builds made by the
// Makefile carry an explicit Version; other builds fall back to the module
// version or VCS revision recorded by the Go toolchain.
func Resolve() Info {
	info := Info{Version: Version, Revision: Revision, GoVersion: runtime.Version()}
	if info.Version != "" && info.Revision != "" {
		return info
	}
	bi, ok := readBuildInfo()
	if !ok {
		if info.Version == "" {
			info.Version = "dev"
		}
		return info
	}
	if info.Version == "" {
		info.Version = resolveVersion(bi)
	}
	if info.Revision == "" {
		info.Revision = buildSetting(bi, "vcs.revision")
	}
	return info
}

func resolveVersion(bi *debug.BuildInfo) string {
	if v := bi.Main.Version; isRelease(v) {
		return v
	}
	if rev := buildSetting(bi, "vcs.revision"); rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if buildSetting(bi, "vcs.modified") == "true" {
			return "dev-" + rev + "-dirty"
		}
		return "dev-" + rev
	}
	return "dev"
}

func isRelease(v string) bool {
	return strings.HasPrefix(v, "v") && len(v) > 1
}

func buildSetting(bi *debug.BuildInfo, key string) string {
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

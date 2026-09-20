package domain

import (
	"fmt"
	"strings"
)

// NormalizePathPrefix canonicalizes a directory prefix: leading/trailing
// slashes are trimmed, empty and "." segments dropped, ".." rejected.
// The empty string means the whole project.
func NormalizePathPrefix(prefix string) (string, error) {
	p := strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return "", nil
	}
	segments := make([]string, 0, len(p))
	for _, segment := range strings.Split(p, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("invalid path prefix %q: must not contain ..", prefix)
		default:
			segments = append(segments, segment)
		}
	}
	return strings.Join(segments, "/"), nil
}

// PrefixCovers reports whether path is the prefix itself or lives below it.
// An empty prefix covers every path in the project.
func PrefixCovers(prefix, path string) bool {
	if prefix == "" {
		return true
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return false
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

// PrefixCanDescend reports whether any path below dir can match prefix.
// It lets a manifest walk prune directories that cannot contain a grant.
func PrefixCanDescend(prefix, dir string) bool {
	if prefix == "" {
		return true
	}
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return true
	}
	if PrefixCovers(prefix, dir) {
		return true
	}
	return strings.HasPrefix(prefix, dir+"/")
}

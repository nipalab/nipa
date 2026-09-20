package domain

import (
	"fmt"
	"strings"
)

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

type PrefixSet []string

func NewPrefixSet(prefixes []string) (PrefixSet, error) {
	if len(prefixes) == 0 {
		return nil, nil
	}
	set := make(PrefixSet, 0, len(prefixes))
	for _, prefix := range prefixes {
		normalized, err := NormalizePathPrefix(prefix)
		if err != nil {
			return nil, err
		}
		set = append(set, normalized)
	}
	return set, nil
}

func (s PrefixSet) Empty() bool {
	return len(s) == 0
}

func (s PrefixSet) Covers(path string) bool {
	if len(s) == 0 {
		return true
	}
	for _, prefix := range s {
		if PrefixCovers(prefix, path) {
			return true
		}
	}
	return false
}

func (s PrefixSet) CanDescend(dir string) bool {
	if len(s) == 0 {
		return true
	}
	for _, prefix := range s {
		if PrefixCanDescend(prefix, dir) {
			return true
		}
	}
	return false
}

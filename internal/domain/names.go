package domain

import (
	"fmt"
	"strings"
)

func ValidateRepoPath(p string) error {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return NewErrorUser("file path must not be empty")
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return NewErrorUser(fmt.Sprintf("invalid file path %q", p))
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return NewErrorUser(fmt.Sprintf("invalid file path %q", p))
		}
	}
	return nil
}

func ValidateBranchName(name string) error {
	return validateRefName("branch", name)
}

func ValidateTagName(name string) error {
	return validateRefName("tag", name)
}

func validateRefName(kind, name string) error {
	if name == "" {
		return NewErrorUser(kind + " name must not be empty")
	}
	if name == "." || name == ".." {
		return NewErrorUser(fmt.Sprintf("invalid %s name %q", kind, name))
	}
	for _, r := range name {
		if InvalidRefNameRune(r) {
			return NewErrorUser(fmt.Sprintf("invalid %s name %q", kind, name))
		}
	}
	return nil
}

func InvalidRefNameRune(r rune) bool {
	return r == '/' || r <= 0x20 || r == 0x7f
}

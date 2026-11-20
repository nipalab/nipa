package domain

import (
	"fmt"
	"runtime"
	"strings"
)

// MetadataDirName is the client's per-clone metadata directory. No tracked
// path may contain it as a segment, or a sync would overwrite clone metadata.
const MetadataDirName = ".nipa"

// ValidateRepoPath checks a pushed file path: slash separated, every segment
// a valid name (see ValidatePathSegment), none of them the metadata directory.
func ValidateRepoPath(p string) error {
	return validateRepoPath(p, true)
}

// ValidateRemovedRepoPath checks a path a push removes. It allows a metadata
// directory segment so a push can still delete such a path stored before
// ValidateRepoPath rejected it; removing never writes it to a working copy.
func ValidateRemovedRepoPath(p string) error {
	return validateRepoPath(p, false)
}

func validateRepoPath(p string, rejectMetadata bool) error {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return NewErrorUser("file path must not be empty")
	}
	for _, seg := range strings.Split(p, "/") {
		if checkSegment(seg) != "" {
			return NewErrorUser(fmt.Sprintf("invalid file path %q", p))
		}
		if rejectMetadata && IsMetadataSegment(seg) {
			return NewErrorUser(fmt.Sprintf("invalid file path %q: %s is reserved for clone metadata", p, MetadataDirName))
		}
	}
	return nil
}

// ValidatePathSegment checks one file or directory name taken from a tree: it
// must be a single, non-special path segment and not the metadata directory.
// These rules are unsafe to break on every operating system; the Windows-only
// ones are in ValidatePortablePathSegment.
func ValidatePathSegment(name string) error {
	if reason := checkSegment(name); reason != "" {
		return NewErrorUser(fmt.Sprintf("invalid name %q: %s", name, reason))
	}
	if IsMetadataSegment(name) {
		return NewErrorUser(fmt.Sprintf("invalid name %q: %s is reserved for clone metadata", name, MetadataDirName))
	}
	return nil
}

// ValidatePortablePathSegment is ValidatePathSegment plus the names Windows
// cannot store as written: a ':' (an NTFS alternate data stream), a trailing
// dot or space (silently stripped), and the reserved device names such as CON
// or LPT1, with or without an extension.
func ValidatePortablePathSegment(name string) error {
	if err := ValidatePathSegment(name); err != nil {
		return err
	}
	reason := ""
	switch {
	case strings.ContainsRune(name, ':'):
		reason = "contains ':'"
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		reason = "ends with a dot or space"
	case isWindowsDeviceName(name):
		reason = "is a reserved Windows device name"
	}
	if reason != "" {
		return NewErrorUser(fmt.Sprintf("invalid name %q: %s", name, reason))
	}
	return nil
}

// ValidateLocalPathSegment checks a name that is about to become a file or
// directory on this machine, adding the Windows rules when running there.
func ValidateLocalPathSegment(name string) error {
	return validateSegmentFor(runtime.GOOS, name)
}

func validateSegmentFor(goos, name string) error {
	if goos == "windows" {
		return ValidatePortablePathSegment(name)
	}
	return ValidatePathSegment(name)
}

// ValidateLocalPath checks every segment of a slash separated repository path
// with ValidateLocalPathSegment.
func ValidateLocalPath(p string) error {
	if p == "" {
		return NewErrorUser("file path must not be empty")
	}
	for _, seg := range strings.Split(p, "/") {
		if err := ValidateLocalPathSegment(seg); err != nil {
			return NewErrorUser(fmt.Sprintf("invalid file path %q: %s", p, err.Error()))
		}
	}
	return nil
}

// IsMetadataSegment reports whether a name resolves to the metadata directory
// on some filesystem: compared case-insensitively, ignoring trailing dots and
// spaces (Windows strips them) and the code points HFS+ ignores, and matching
// the NIPA~1 short name NTFS gives it.
func IsMetadataSegment(name string) bool {
	n := strings.ToLower(strings.TrimRight(stripHFSIgnorable(name), ". "))
	return n == MetadataDirName || n == "nipa~1"
}

func checkSegment(name string) string {
	switch {
	case name == "":
		return "empty name"
	case name == "." || name == "..":
		return "relative path segment"
	case strings.ContainsAny(name, "/\\"):
		return "contains a path separator"
	case strings.ContainsRune(name, 0):
		return "contains a NUL byte"
	}
	return ""
}

// stripHFSIgnorable drops the zero-width code points HFS+ ignores when it
// compares names, so ".n\u200cipa" cannot alias the metadata directory.
func stripHFSIgnorable(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x200c && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x206a && r <= 0x206f, r == 0xfeff:
			return -1
		}
		return r
	}, name)
}

var windowsDeviceNames = func() map[string]struct{} {
	names := map[string]struct{}{"CON": {}, "PRN": {}, "AUX": {}, "NUL": {}, "CONIN$": {}, "CONOUT$": {}}
	for _, prefix := range []string{"COM", "LPT"} {
		for _, digit := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "\u00b9", "\u00b2", "\u00b3"} {
			names[prefix+digit] = struct{}{}
		}
	}
	return names
}()

// isWindowsDeviceName reports whether Windows maps the name to a device: the
// part before the first dot, without trailing spaces, is a reserved name.
func isWindowsDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	_, ok := windowsDeviceNames[strings.ToUpper(strings.TrimRight(base, " "))]
	return ok
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

package domain

import "io/fs"

// LinkKind names a mode that redirects a path elsewhere, or returns "" for
// anything else. Go reports Windows junctions and other name-surrogate
// reparse points as ModeIrregular without ModeDir (or as ModeSymlink with
// GODEBUG=winsymlink=0); a folder that is some other reparse point (a
// OneDrive placeholder, say) keeps ModeDir and is a real folder.
func LinkKind(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "symbolic link"
	case mode&fs.ModeIrregular != 0 && !mode.IsDir():
		return "junction or reparse point"
	}
	return ""
}

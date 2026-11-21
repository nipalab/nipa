package usecase

import (
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

// materializeTempPrefix names the temp files working files are assembled in,
// next to their targets, before they are renamed into place.
const materializeTempPrefix = ".nipa-materialize-"

// sweepMargin keeps a sweep away from temp files as young as the write that
// triggers it: another process may have just started writing them, and coarse
// file timestamps can make a new file look slightly older than it is.
const sweepMargin = 5 * time.Second

// staleTempAge is how old a temp file must be for nipa gc to remove it
// without knowing whether a write is in progress.
const staleTempAge = time.Hour

type workingWriteMarker interface {
	BeginWorkingWrite() (end func(), interrupted bool, err error)
	MarkInterruptedWrite() error
}

// beginWorkingWrite marks a working-copy write on repositories that track
// them and returns the function that ends it. When an earlier write was
// killed before it could clean up, the temp files it left in the working
// copy are swept first; a clone that was never interrupted pays nothing. The
// write also shares one folder guard (guardWrites) until it ends.
func beginWorkingWrite(lr any, root string) func() {
	endGuard := guardWrites(root)
	end := beginWriteMarker(lr, root)
	return func() {
		end()
		endGuard()
	}
}

func beginWriteMarker(lr any, root string) func() {
	marker, ok := lr.(workingWriteMarker)
	if !ok {
		return func() {}
	}
	started := time.Now()
	end, interrupted, err := marker.BeginWorkingWrite()
	if err != nil {
		slog.Warn("could not mark the working-copy write", "error", err)
		return func() {}
	}
	if interrupted {
		removed, young := sweepMaterializeTemps(root, started.Add(-sweepMargin))
		slog.Debug("swept temp files of an interrupted write", "removed", removed, "left", young)
		if young > 0 {
			if err := marker.MarkInterruptedWrite(); err != nil {
				slog.Warn("could not keep the interrupted-write marker", "error", err)
			}
		}
	}
	return end
}

// sweepMaterializeTemps removes the materialize temp files under root last
// modified before the cutoff and reports how many it removed and how many
// younger ones it left. It walks and removes inside the work tree, never
// through a link, and never descends into the .nipa directory.
func sweepMaterializeTemps(root string, before time.Time) (removed, young int) {
	wt := acquireWorkTree(root)
	defer wt.release()
	r, err := wt.open(false)
	if err != nil {
		return 0, 0
	}
	_ = fs.WalkDir(r.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == nipaDir && path != "." {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !isMaterializeTemp(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !info.ModTime().Before(before) {
			young++
			return nil
		}
		if wt.remove(filepath.FromSlash(path)) == nil {
			removed++
		}
		return nil
	})
	return removed, young
}

// isMaterializeTemp reports whether name is one os.CreateTemp made for
// materializeTempPrefix: the prefix followed only by the random decimal
// suffix, so a user file that merely starts with the prefix is left alone.
func isMaterializeTemp(name string) bool {
	suffix, ok := strings.CutPrefix(name, materializeTempPrefix)
	if !ok || suffix == "" {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

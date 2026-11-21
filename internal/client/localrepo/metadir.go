package localrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/client/domain"
)

// metadataDirs are the folders of .nipa the client writes and prunes in,
// after .nipa itself.
var metadataDirs = []string{ObjectsDir, WritesDir, ShelvesDir}

// checkMetadataDirs refuses a clone whose .nipa folder, or a folder in it the
// client writes and prunes in, is a symbolic link or junction: the metadata,
// the object cache and its pruning would act on wherever the link points.
// Missing folders are fine; Init creates them as real ones. It costs one
// Lstat per folder, whatever the cache holds.
func checkMetadataDirs(target string) error {
	for i, rel := range append([]string{ConfigDir}, metadataDirs...) {
		if i > 0 {
			rel = filepath.Join(ConfigDir, rel)
		}
		info, err := os.Lstat(filepath.Join(target, rel))
		if errors.Is(err, fs.ErrNotExist) {
			if i == 0 {
				return nil
			}
			continue
		}
		if err != nil {
			return err
		}
		if kind := domain.LinkKind(info.Mode()); kind != "" {
			return linkedMetadataError(rel, kind)
		}
	}
	return nil
}

func linkedMetadataError(rel, kind string) error {
	p := filepath.ToSlash(rel)
	return domain.NewUserError(fmt.Sprintf("refusing to use %q: it is a %s", p, kind)).
		WithHint("Nipa keeps its metadata and object cache in real folders inside the working copy and never writes or deletes through a link there", fmt.Sprintf("replace %q with a real folder (move the linked content back into it, or delete it and clone again)", p))
}

// openRealDir opens the metadata folder rel of target as an os.Root, so
// nothing done through it leaves the folder, and refuses it when it is a
// symbolic link or junction. The folder opened is compared with the one found
// at rel afterwards, so a link swapped in while it was opened is refused too.
func openRealDir(target, rel string) (*os.Root, error) {
	dir := filepath.Join(target, rel)
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	var found fs.FileInfo
	if err == nil {
		found, err = os.Lstat(dir)
	}
	if err == nil {
		if kind := domain.LinkKind(found.Mode()); kind != "" {
			err = linkedMetadataError(rel, kind)
		} else if !os.SameFile(opened, found) {
			err = linkedMetadataError(rel, "folder that changed while it was opened")
		}
	}
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

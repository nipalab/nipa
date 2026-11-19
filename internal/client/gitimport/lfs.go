package gitimport

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/gitimport"
	"github.com/nipalab/nipa/internal/gitimport/lfs"
)

const maxListedLFS = 10

func (im *Importer) lfsResolver(src *gitimport.Source, remote *remoteSource, mode lfs.Mode, gitDir string) (*lfs.Resolver, error) {
	if mode == lfs.ModeOff {
		return nil, nil
	}
	remoteURL := ""
	if remote != nil {
		remoteURL = remote.raw
	}
	settings := lfs.Discover(src.Repository(), remoteURL)
	r := &lfs.Resolver{Mode: mode, Local: append([]*lfs.LocalStore{lfs.NewLocalStore(gitDir)}, settings.Stores...)}
	if settings.Endpoint == "" || mode != lfs.ModeAuto {
		return r, nil
	}
	client, err := lfs.NewClient(settings.Endpoint, lfs.ClientOptions{
		HTTP:        im.http,
		Credentials: im.credentials,
		TempDir:     filepath.Join(gitDir, StateDirName, "tmp"),
		Backoff:     im.lfsBackoff,
	})
	if err != nil {
		return nil, domain.NewUserError(err.Error()).WithHint("the lfs endpoint comes from lfs.url, .lfsconfig or the remote url", "fix the lfs url, or import with --lfs local or --lfs off")
	}
	r.Remote = client
	return r, nil
}

func missingLFSError(missing []gitimport.LFSObject, mode lfs.Mode) error {
	lines := make([]string, 0, maxListedLFS)
	for i, o := range missing {
		if i == maxListedLFS {
			lines = append(lines, fmt.Sprintf("and %d more", len(missing)-maxListedLFS))
			break
		}
		lines = append(lines, fmt.Sprintf("%s (%s)", o.OID, o.Path))
	}
	action := "run git lfs fetch --all in the git repository, set lfs.url, or import the pointer files with --lfs off"
	if mode == lfs.ModeLocal {
		action = "run git lfs fetch --all in the git repository, or import with --lfs auto to download them"
	}
	return domain.NewUserError(fmt.Sprintf("%d lfs object(s) are not in the local lfs store, nothing was imported: %s", len(missing), strings.Join(lines, ", "))).
		WithHint("nothing was imported", action)
}

func withLFSHint(err error) error {
	if err == nil {
		return nil
	}
	var missing *lfs.MissingError
	var server *lfs.ServerError
	switch {
	case errors.As(err, &missing):
		return domain.NewUserError(err.Error()).WithHint("the lfs object is neither local nor on a configured lfs server", "run git lfs fetch --all in the git repository, then rerun the import")
	case errors.As(err, &server) && (server.Status == 401 || server.Status == 403):
		return domain.NewUserError(err.Error()).WithHint("the lfs server refused the credentials", "store credentials for the lfs host in a git credential helper, or run git lfs fetch --all and import with --lfs local")
	case errors.As(err, &server) && server.NotFound():
		return domain.NewUserError(err.Error()).WithHint("the lfs server does not have this object", "run git lfs fetch --all where the object exists, then rerun the import")
	case errors.Is(err, lfs.ErrCorrupt):
		return domain.NewUserError(err.Error()).WithHint("the lfs content does not match its pointer", "run git lfs fsck in the git repository, then rerun the import")
	}
	return err
}

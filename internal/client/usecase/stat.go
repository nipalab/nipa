package usecase

import (
	"os"
	"path/filepath"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// statEntryFor fingerprints a working file whose content hash is already known
// (materialized from chunks, or scanned while pushing), so the next status can
// trust it without rehashing.
func statEntryFor(root, path string, hash serverDomain.Hash) (domain.StatEntry, error) {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return domain.StatEntry{}, err
	}
	return statEntryFromInfo(info, hash), nil
}

func statEntryFromInfo(info os.FileInfo, hash serverDomain.Hash) domain.StatEntry {
	return domain.StatEntry{
		SizeBytes: info.Size(),
		MtimeNS:   info.ModTime().UnixNano(),
		Mode:      serverModeFromPerm(info.Mode()),
		Hash:      hash,
		CachedAt:  time.Now().UnixNano(),
	}
}

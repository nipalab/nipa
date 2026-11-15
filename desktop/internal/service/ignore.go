package service

import (
	"github.com/nipalab/nipa/internal/client/ignore"
)

// GeneratedFolders groups new paths under the folders that look like build
// output or installed dependencies, largest first.
func (s *Service) GeneratedFolders(root string, paths []string) ([]ignore.Generated, error) {
	if err := s.knownRoot(root); err != nil {
		return nil, err
	}
	return ignore.DetectGenerated(root, paths), nil
}

// IgnoreFolders appends patterns to .nipaignore and returns how many were new.
func (s *Service) IgnoreFolders(root string, patterns []string) (int, error) {
	if err := s.knownRoot(root); err != nil {
		return 0, err
	}
	n, err := ignore.AppendRules(root, patterns)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.emit(EventRepoChanged, RepoChanged{Root: root})
	}
	return n, nil
}

// CanImportGitignore reports whether the clone has a root .gitignore but no
// .nipaignore yet.
func (s *Service) CanImportGitignore(root string) (bool, error) {
	if err := s.knownRoot(root); err != nil {
		return false, err
	}
	return ignore.CanImportGitignore(root), nil
}

// ImportGitignore copies the root .gitignore rules into .nipaignore.
func (s *Service) ImportGitignore(root string) (ignore.ImportResult, error) {
	if err := s.knownRoot(root); err != nil {
		return ignore.ImportResult{}, err
	}
	res, err := ignore.ImportGitignore(root)
	if err != nil {
		return ignore.ImportResult{}, err
	}
	s.emit(EventRepoChanged, RepoChanged{Root: root})
	return res, nil
}

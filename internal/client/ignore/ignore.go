// Package ignore matches repo-relative paths against the patterns in the
// repository root .nipaignore file and the per-clone .nipa/ignore file.
package ignore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/pathmatch"
)

// FileName is the versioned ignore rules file expected at the repository root.
const FileName = ".nipaignore"

const localFile = ".nipa/ignore"

// Matcher holds the ordered rules from both ignore files; later rules win, and
// local rules are appended after the versioned ones.
type Matcher struct {
	patterns *pathmatch.Patterns
}

// New loads the root .nipaignore file and the local .nipa/ignore file. Missing
// files are not an error.
func New(root string) (*Matcher, error) {
	var lists []*pathmatch.Patterns
	paths := []string{
		filepath.Join(root, FileName),
		filepath.Join(root, filepath.FromSlash(localFile)),
	}
	for _, path := range paths {
		loaded, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		lists = append(lists, loaded)
	}
	return &Matcher{patterns: pathmatch.Join(lists...)}, nil
}

func loadFile(path string) (*pathmatch.Patterns, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read ignore rules: %w", err)
	}
	return pathmatch.Parse(string(data)), nil
}

// Empty reports whether the matcher has no rules.
func (m *Matcher) Empty() bool {
	return m == nil || m.patterns.Empty()
}

// Ignores reports whether path is ignored. An ignored directory also ignores
// everything below it, and a file cannot be re-included once a parent
// directory is excluded (matching gitignore semantics).
func (m *Matcher) Ignores(path string, isDir bool) bool {
	if m == nil {
		return false
	}
	return m.patterns.Matches(path, isDir)
}

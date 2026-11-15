package ignore

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GitignoreFile is the git ignore file ImportGitignore reads.
const GitignoreFile = ".gitignore"

var utf8BOM = []byte("\xef\xbb\xbf")

// ImportResult describes what ImportGitignore wrote.
type ImportResult struct {
	Added       int      `json:"added"`
	Unsupported []string `json:"unsupported,omitempty"`
}

// AppendRules adds the patterns .nipaignore does not already hold, creating
// the file when missing, and returns how many were written.
func AppendRules(root string, patterns []string) (int, error) {
	return appendRules(root, "", patterns)
}

// ImportGitignore copies the root .gitignore rules that .nipaignore does not
// already hold. Lines using gitignore syntax the matcher does not support
// (character classes, escapes) are copied but reported.
func ImportGitignore(root string) (ImportResult, error) {
	data, err := os.ReadFile(filepath.Join(root, GitignoreFile))
	if err != nil {
		return ImportResult{}, fmt.Errorf("read %s: %w", GitignoreFile, err)
	}
	var patterns []string
	var res ImportResult
	for _, line := range strings.Split(string(bytes.TrimPrefix(data, utf8BOM)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
		if strings.ContainsAny(line, `[\`) {
			res.Unsupported = append(res.Unsupported, line)
		}
	}
	res.Added, err = appendRules(root, "# imported from .gitignore", patterns)
	return res, err
}

// CanImportGitignore reports whether the root has a .gitignore but no
// .nipaignore.
func CanImportGitignore(root string) bool {
	if _, err := os.Stat(filepath.Join(root, FileName)); !os.IsNotExist(err) {
		return false
	}
	info, err := os.Stat(filepath.Join(root, GitignoreFile))
	return err == nil && info.Mode().IsRegular()
}

func appendRules(root, header string, patterns []string) (int, error) {
	file := filepath.Join(root, FileName)
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("read ignore rules: %w", err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(bytes.TrimPrefix(data, utf8BOM)), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || have[p] {
			continue
		}
		have[p] = true
		add = append(add, p)
	}
	if len(add) == 0 {
		return 0, nil
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	if header != "" {
		if len(data) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(header + "\n")
	}
	for _, p := range add {
		b.WriteString(p + "\n")
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		return 0, fmt.Errorf("write ignore rules: %w", err)
	}
	return len(add), nil
}

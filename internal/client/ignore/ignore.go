// Package ignore matches repo-relative paths against the patterns in the
// repository root .nipaignore file and the per-clone .nipa/ignore file.
package ignore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileName is the versioned ignore rules file expected at the repository root.
const FileName = ".nipaignore"

const localFile = ".nipa/ignore"

type rule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// Matcher holds the ordered rules from both ignore files; later rules win, and
// local rules are appended after the versioned ones.
type Matcher struct {
	rules []rule
}

// New loads the root .nipaignore file and the local .nipa/ignore file. Missing
// files are not an error.
func New(root string) (*Matcher, error) {
	var rules []rule
	paths := []string{
		filepath.Join(root, FileName),
		filepath.Join(root, filepath.FromSlash(localFile)),
	}
	for _, path := range paths {
		loaded, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		rules = append(rules, loaded...)
	}
	return &Matcher{rules: rules}, nil
}

func loadFile(path string) ([]rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read ignore rules: %w", err)
	}
	return compile(string(data)), nil
}

// Empty reports whether the matcher has no rules.
func (m *Matcher) Empty() bool {
	return m == nil || len(m.rules) == 0
}

// Ignores reports whether path is ignored. An ignored directory also ignores
// everything below it, and a file cannot be re-included once a parent
// directory is excluded (matching gitignore semantics).
func (m *Matcher) Ignores(path string, isDir bool) bool {
	if m.Empty() || path == "" || path == "." {
		return false
	}
	segments := strings.Split(path, "/")
	for i := 1; i < len(segments); i++ {
		if m.match(strings.Join(segments[:i], "/"), true) {
			return true
		}
	}
	return m.match(path, isDir)
}

func (m *Matcher) match(path string, isDir bool) bool {
	ignored := false
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(path) {
			ignored = !r.negate
		}
	}
	return ignored
}

// compile parses ignore patterns: blank lines and # comments are skipped, a
// leading ! negates, a trailing / restricts to directories, a leading or
// embedded / anchors the pattern to the repository root, and anything else
// matches the basename at any depth. `**` spans path segments, while `*` and
// `?` stay inside one segment.
func compile(contents string) []rule {
	var rules []rule
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negate := strings.HasPrefix(line, "!")
		if negate {
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		rules = append(rules, rule{
			re:      regexp.MustCompile(patternRegexp(line, anchored)),
			negate:  negate,
			dirOnly: dirOnly,
		})
	}
	return rules
}

func patternRegexp(pattern string, anchored bool) string {
	segments := strings.Split(pattern, "/")
	var b strings.Builder
	if anchored {
		b.WriteString("^")
	} else {
		b.WriteString(`(?:^|/)`)
	}
	for i, segment := range segments {
		if segment == "**" {
			switch {
			case i == 0 && len(segments) == 1:
				b.WriteString(`.*`)
			case i == 0:
				b.WriteString(`(?:[^/]+/)*`)
			case i == len(segments)-1:
				b.WriteString(`/.*`)
			default:
				b.WriteString(`/(?:[^/]+/)*`)
			}
			continue
		}
		if i > 0 && segments[i-1] != "**" {
			b.WriteString("/")
		}
		b.WriteString(segmentRegexp(segment))
	}
	b.WriteString("$")
	return b.String()
}

func segmentRegexp(segment string) string {
	var b strings.Builder
	for i := 0; i < len(segment); i++ {
		switch segment[i] {
		case '*':
			b.WriteString(`[^/]*`)
			if i+1 < len(segment) && segment[i+1] == '*' {
				i++
			}
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(segment[i])))
		}
	}
	return b.String()
}

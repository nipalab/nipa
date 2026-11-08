// Package pathmatch compiles the gitignore-subset patterns shared by
// .nipaignore and .nipaassets.yml and matches repo-relative paths against
// them. It is pure and free of client imports.
package pathmatch

import (
	"regexp"
	"strings"
)

type rule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// Patterns is an ordered pattern list; the last matching pattern wins.
type Patterns struct {
	rules []rule
}

// Parse compiles newline-separated patterns. Blank lines and # comments are
// skipped, a leading ! negates, a trailing / restricts to directories, a
// leading or embedded / anchors the pattern to the repository root, and
// anything else matches the basename at any depth. `**` spans path segments,
// while `*` and `?` stay inside one segment.
func Parse(contents string) *Patterns {
	p := &Patterns{}
	for _, line := range strings.Split(contents, "\n") {
		if r, ok := compileLine(line); ok {
			p.rules = append(p.rules, r)
		}
	}
	return p
}

// Compile compiles one pattern per element, with the Parse syntax.
func Compile(lines []string) *Patterns {
	return Parse(strings.Join(lines, "\n"))
}

// Join concatenates pattern lists in order, so later lists win.
func Join(lists ...*Patterns) *Patterns {
	out := &Patterns{}
	for _, l := range lists {
		if l != nil {
			out.rules = append(out.rules, l.rules...)
		}
	}
	return out
}

// Empty reports whether there are no patterns.
func (p *Patterns) Empty() bool {
	return p == nil || len(p.rules) == 0
}

// Matches reports whether path matches. A matching directory also covers
// everything below it, and a file cannot be negated back once a parent
// directory matched (gitignore semantics).
func (p *Patterns) Matches(path string, isDir bool) bool {
	if p.Empty() || path == "" || path == "." {
		return false
	}
	segments := strings.Split(path, "/")
	for i := 1; i < len(segments); i++ {
		if p.match(strings.Join(segments[:i], "/"), true) {
			return true
		}
	}
	return p.match(path, isDir)
}

func (p *Patterns) match(path string, isDir bool) bool {
	matched := false
	for _, r := range p.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(path) {
			matched = !r.negate
		}
	}
	return matched
}

func compileLine(line string) (rule, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return rule{}, false
	}
	negate := strings.HasPrefix(line, "!")
	if negate {
		line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
	}
	dirOnly := strings.HasSuffix(line, "/")
	line = strings.TrimSuffix(line, "/")
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return rule{}, false
	}
	return rule{re: regexp.MustCompile(patternRegexp(line, anchored)), negate: negate, dirOnly: dirOnly}, true
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

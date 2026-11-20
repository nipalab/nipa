package issuetracker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const (
	RelationMentions = "mentions"
	RelationCloses   = "closes"

	// MaxRefs caps the distinct issue references read from one text.
	MaxRefs = 20

	maxKeyLen       = 32
	maxCachedRegexp = 128
)

type Ref struct {
	Key      string
	Relation string
}

var projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

// NormalizeProjectKey uppercases and validates a Jira-style project key
// (`GAME`, `ART_2`).
func NormalizeProjectKey(key string) (string, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if len(key) > maxKeyLen || !projectKeyPattern.MatchString(key) {
		return "", fmt.Errorf("invalid project key %q", key)
	}
	return key, nil
}

// NormalizeProjectKeys normalizes, deduplicates and sorts keys, failing on the
// first invalid one.
func NormalizeProjectKeys(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, raw := range keys {
		key, err := NormalizeProjectKey(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func validKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, raw := range keys {
		if key, err := NormalizeProjectKey(raw); err == nil {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

type patterns struct {
	refs   *regexp.Regexp
	branch *regexp.Regexp
}

var (
	patternMu    sync.Mutex
	patternCache = map[string]*patterns{}
)

func patternsFor(keys []string) *patterns {
	keys = validKeys(keys)
	if len(keys) == 0 {
		return nil
	}
	cacheKey := strings.Join(keys, ",")
	patternMu.Lock()
	defer patternMu.Unlock()
	if p, ok := patternCache[cacheKey]; ok {
		return p
	}
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = regexp.QuoteMeta(key)
	}
	alt := strings.Join(quoted, "|")
	p := &patterns{
		refs: regexp.MustCompile(`(?:(?i:\b(?:fix|fixes|fixed|close|closes|closed|resolve|resolves|resolved)\b):?\s*)?` +
			`\b((?:` + alt + `)-[1-9][0-9]{0,9})\b`),
		branch: regexp.MustCompile(`^(?i:(` + alt + `)-([1-9][0-9]{0,9}))(?:$|[^0-9A-Za-z])`),
	}
	if len(patternCache) >= maxCachedRegexp {
		clear(patternCache)
	}
	patternCache[cacheKey] = p
	return p
}

// Extract returns the issue references in text for the given project keys,
// in order of first appearance, at most MaxRefs. A reference preceded by a
// closing keyword (`Fixes GAME-1`, `resolved: GAME-1`) is `closes`, any other
// `mentions`; a key seen both ways is `closes`.
func Extract(text string, keys []string) []Ref {
	p := patternsFor(keys)
	if p == nil || text == "" {
		return nil
	}
	var refs []Ref
	index := map[string]int{}
	for _, m := range p.refs.FindAllStringSubmatchIndex(text, -1) {
		key := text[m[2]:m[3]]
		relation := RelationMentions
		if m[0] < m[2] {
			relation = RelationCloses
		}
		if i, ok := index[key]; ok {
			if relation == RelationCloses {
				refs[i].Relation = RelationCloses
			}
			continue
		}
		if len(refs) >= MaxRefs {
			continue
		}
		index[key] = len(refs)
		refs = append(refs, Ref{Key: key, Relation: relation})
	}
	return refs
}

// BranchKey reads an issue key leading the last segment of a branch name
// (`GAME-123-new-rig`, `feature/game-123-new-rig`), uppercased.
func BranchKey(name string, keys []string) (string, bool) {
	p := patternsFor(keys)
	if p == nil {
		return "", false
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	m := p.branch.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return strings.ToUpper(m[1]) + "-" + m[2], true
}

package localrepo

import (
	"context"
	"errors"
	"strings"
	"unicode"

	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

// PathHit is one tracked file whose path contains the searched characters in
// order.
type PathHit struct {
	Path      string
	SizeBytes int64
	IsBinary  bool
}

// SearchPaths returns up to max tracked files whose path contains every
// non-space character of query in order, ignoring ASCII case. It is a
// prefilter: callers rank the hits. truncated reports that more rows matched.
func (l *LocalRepo) SearchPaths(query string, max int) ([]PathHit, bool, error) {
	if l.db == nil {
		return nil, false, errors.New("local repo not initialized")
	}
	pattern := SubsequencePattern(query)
	if pattern == "" || max <= 0 {
		return nil, false, nil
	}
	rows, err := sqlcLocalrepo.New(l.db).FileSearchPaths(context.Background(), sqlcLocalrepo.FileSearchPathsParams{Pattern: pattern, MaxRows: int64(max) + 1})
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > max
	if truncated {
		rows = rows[:max]
	}
	out := make([]PathHit, 0, len(rows))
	for _, r := range rows {
		out = append(out, PathHit{Path: stripRoot(r.Path), SizeBytes: r.SizeBytes, IsBinary: r.IsBinary})
	}
	return out, truncated, nil
}

// SubsequencePattern turns query into a LIKE pattern (escape \) matching
// paths that hold its characters in order. SQLite folds case for ASCII only,
// so other characters become single-character wildcards and the caller's
// ranking checks them. Empty when query has no searchable character.
func SubsequencePattern(query string) string {
	var b strings.Builder
	for _, r := range query {
		switch {
		case unicode.IsSpace(r):
			continue
		case r == '%' || r == '_' || r == '\\':
			b.WriteString("%\\")
			b.WriteRune(r)
		case r > unicode.MaxASCII:
			b.WriteString("%_")
		default:
			b.WriteByte('%')
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	b.WriteByte('%')
	return b.String()
}

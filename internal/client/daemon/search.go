package daemon

import (
	"context"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/preview"
)

const (
	defaultSearchLimit = 50
	maxSearchLimit     = 200
	// searchPrefilter caps the rows the LIKE prefilter hands to the ranking,
	// so a one-letter query on a huge clone stays cheap.
	searchPrefilter = 20000
)

// SearchFiles finds tracked files whose path holds the query's characters in
// order, best first: basename prefix, basename substring, basename
// subsequence, then the same on the whole path.
func (s *Server) SearchFiles(ctx context.Context, req *daemonpb.SearchFilesRequest) (*daemonpb.SearchFilesResponse, error) {
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	resp := &daemonpb.SearchFilesResponse{}
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		hits, truncated, err := rp.localRepo.SearchPaths(req.GetQuery(), searchPrefilter)
		if err != nil {
			return err
		}
		resp.Truncated = truncated
		for _, h := range rankPaths(req.GetQuery(), hits, limit) {
			resp.Hits = append(resp.Hits, &daemonpb.SearchFileHit{
				Path: h.Path, Name: path.Base(h.Path), SizeBytes: h.SizeBytes, IsBinary: h.IsBinary,
				Kind: string(preview.Classify(h.Path, h.IsBinary)),
			})
		}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return resp, nil
}

type pathScore struct {
	hit  localrepo.PathHit
	tier int
	span int
}

// rankPaths keeps the hits that really match query (the prefilter ignores
// case only for ASCII) and orders them by tier, match span, path length and
// path.
func rankPaths(query string, hits []localrepo.PathHit, limit int) []localrepo.PathHit {
	q := foldQuery(query)
	if len(q) == 0 {
		return nil
	}
	scored := make([]pathScore, 0, len(hits))
	for _, h := range hits {
		if tier, span, ok := scorePath(q, h.Path); ok {
			scored = append(scored, pathScore{hit: h, tier: tier, span: span})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		a, b := scored[i], scored[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.span != b.span {
			return a.span < b.span
		}
		if len(a.hit.Path) != len(b.hit.Path) {
			return len(a.hit.Path) < len(b.hit.Path)
		}
		return a.hit.Path < b.hit.Path
	})
	out := make([]localrepo.PathHit, 0, min(limit, len(scored)))
	for _, s := range scored {
		if len(out) == limit {
			break
		}
		out = append(out, s.hit)
	}
	return out
}

func foldQuery(query string) []rune {
	var out []rune
	for _, r := range query {
		if !unicode.IsSpace(r) {
			out = append(out, unicode.ToLower(r))
		}
	}
	return out
}

func scorePath(q []rune, p string) (tier, span int, ok bool) {
	lower := []rune(strings.ToLower(p))
	base := []rune(strings.ToLower(path.Base(p)))
	needle := string(q)
	switch {
	case strings.HasPrefix(string(base), needle):
		return 0, len(q), true
	case strings.Contains(string(base), needle):
		return 1, len(q), true
	}
	if span, ok := subsequenceSpan(q, base); ok {
		return 2, span, true
	}
	if strings.Contains(string(lower), needle) {
		return 3, len(q), true
	}
	if span, ok := subsequenceSpan(q, lower); ok {
		return 4, span, true
	}
	return 0, 0, false
}

// subsequenceSpan reports whether q occurs in s in order and the length of
// the shortest window holding it that starts at a greedy match.
func subsequenceSpan(q, s []rune) (int, bool) {
	best := -1
	for start := range s {
		if s[start] != q[0] {
			continue
		}
		i, j := start, 0
		for i < len(s) && j < len(q) {
			if s[i] == q[j] {
				j++
			}
			i++
		}
		if j < len(q) {
			break
		}
		if span := i - start; best < 0 || span < best {
			best = span
		}
	}
	return best, best >= 0
}

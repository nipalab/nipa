package gitimport

import (
	"container/heap"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/nipalab/nipa/internal/domain"
)

const DefaultSourceKey = "default"

var DefaultRefPatterns = []string{"refs/heads/*", "refs/tags/*"}

type WarningKind string

const (
	WarnOctopus          WarningKind = "octopus"
	WarnRefRenamed       WarningKind = "ref_renamed"
	WarnRefCollision     WarningKind = "ref_collision"
	WarnRefSkipped       WarningKind = "ref_skipped"
	WarnRefOutsideWindow WarningKind = "ref_outside_window"
	WarnSymlink          WarningKind = "symlink"
	WarnSubmodule        WarningKind = "submodule"
)

type Warning struct {
	Kind    WarningKind
	Commit  string
	Ref     string
	Path    string
	Message string
}

type Options struct {
	SourceKey     string
	Refs          []string
	DefaultBranch string
	Depth         int
	Since         time.Time
	Snapshot      bool
	Known         map[string]bool
}

type PlannedRef struct {
	Ref
	Target string
	Origin string
}

type PlannedCommit struct {
	Hash          plumbing.Hash
	Origin        string
	Parents       []plumbing.Hash
	ParentOrigins []string
	CommitterTime time.Time
}

type Plan struct {
	SourceKey     string
	Commits       []*PlannedCommit
	Refs          []PlannedRef
	DefaultBranch string
	Warnings      []Warning
}

func (p *Plan) Branches() []PlannedRef {
	return p.refsOf(RefBranch)
}

func (p *Plan) Tags() []PlannedRef {
	return p.refsOf(RefTag)
}

func (p *Plan) refsOf(kind RefKind) []PlannedRef {
	var out []PlannedRef
	for _, r := range p.Refs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func Origin(sourceKey string, h plumbing.Hash) string {
	return "git:" + sourceKey + ":" + h.String()
}

func ValidateSourceKey(key string) error {
	if key == "" || strings.ContainsRune(key, ':') {
		return fmt.Errorf("invalid source key %q", key)
	}
	if _, err := domain.ParseOrigin(Origin(key, plumbing.ZeroHash)); err != nil {
		return fmt.Errorf("invalid source key %q: %w", key, err)
	}
	return nil
}

type Planner struct {
	src *Source
}

func NewPlanner(src *Source) *Planner {
	return &Planner{src: src}
}

type planNode struct {
	hash      plumbing.Hash
	parents   []plumbing.Hash
	committed time.Time
	children  []*planNode
	pending   int
}

func (p *Planner) Plan(opts Options) (*Plan, error) {
	if opts.SourceKey == "" {
		opts.SourceKey = DefaultSourceKey
	}
	if err := ValidateSourceKey(opts.SourceKey); err != nil {
		return nil, err
	}
	if opts.Depth < 0 {
		return nil, fmt.Errorf("depth must not be negative")
	}
	plan := &Plan{SourceKey: opts.SourceKey}
	refs, warnings, err := p.selectRefs(opts.Refs)
	if err != nil {
		return nil, err
	}
	plan.Warnings = append(plan.Warnings, warnings...)
	known := func(h plumbing.Hash) bool { return opts.Known[Origin(opts.SourceKey, h)] }

	var nodes map[plumbing.Hash]*planNode
	if opts.Snapshot {
		nodes, err = p.snapshotNodes(refs, opts, known)
	} else {
		var walkWarnings []Warning
		var frontier []plumbing.Hash
		nodes, frontier, walkWarnings, err = p.walk(refs, opts, known)
		plan.Warnings = append(plan.Warnings, walkWarnings...)
		if err == nil && len(frontier) > 0 {
			err = p.pruneAncestors(nodes, frontier, known)
		}
	}
	if err != nil {
		return nil, err
	}

	for _, r := range refs {
		if _, ok := nodes[r.Commit]; !ok && !known(r.Commit) {
			plan.Warnings = append(plan.Warnings, Warning{Kind: WarnRefOutsideWindow, Ref: r.Name.String(), Commit: r.Commit.String(), Message: "the ref tip is outside the selected window"})
			continue
		}
		plan.Refs = append(plan.Refs, r)
	}
	for i := range plan.Refs {
		plan.Refs[i].Origin = Origin(opts.SourceKey, plan.Refs[i].Commit)
	}

	plan.Commits = order(nodes, opts.SourceKey, known)
	plan.DefaultBranch, err = p.defaultBranch(plan, opts.DefaultBranch)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func (p *Planner) snapshotNodes(refs []PlannedRef, opts Options, known func(plumbing.Hash) bool) (map[plumbing.Hash]*planNode, error) {
	nodes := map[plumbing.Hash]*planNode{}
	for _, r := range refs {
		if _, ok := nodes[r.Commit]; ok || known(r.Commit) {
			continue
		}
		c, err := p.src.Commit(r.Commit)
		if err != nil {
			return nil, fmt.Errorf("read commit %s: %w", r.Commit, err)
		}
		if !opts.Since.IsZero() && c.Committer.When.Before(opts.Since) {
			continue
		}
		nodes[r.Commit] = &planNode{hash: r.Commit, committed: c.Committer.When}
	}
	return nodes, nil
}

type walkItem struct {
	hash plumbing.Hash
	dist int
}

func (p *Planner) walk(refs []PlannedRef, opts Options, known func(plumbing.Hash) bool) (map[plumbing.Hash]*planNode, []plumbing.Hash, []Warning, error) {
	nodes := map[plumbing.Hash]*planNode{}
	seen := map[plumbing.Hash]bool{}
	var frontier []plumbing.Hash
	var warnings []Warning
	var queue []walkItem
	for _, r := range refs {
		if !seen[r.Commit] {
			seen[r.Commit] = true
			queue = append(queue, walkItem{hash: r.Commit})
		}
	}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if known(item.hash) {
			frontier = append(frontier, item.hash)
			continue
		}
		if opts.Depth > 0 && item.dist >= opts.Depth {
			continue
		}
		c, err := p.src.Commit(item.hash)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read commit %s: %w", item.hash, err)
		}
		if !opts.Since.IsZero() && c.Committer.When.Before(opts.Since) {
			continue
		}
		parents := dedupe(c.ParentHashes)
		if len(parents) > 2 {
			dropped := make([]string, 0, len(parents)-2)
			for _, h := range parents[2:] {
				dropped = append(dropped, h.String())
			}
			warnings = append(warnings, Warning{Kind: WarnOctopus, Commit: item.hash.String(), Message: "octopus merge: parents " + strings.Join(dropped, ", ") + " are not kept"})
			parents = parents[:2]
		}
		nodes[item.hash] = &planNode{hash: item.hash, parents: parents, committed: c.Committer.When}
		for _, h := range parents {
			if !seen[h] {
				seen[h] = true
				queue = append(queue, walkItem{hash: h, dist: item.dist + 1})
			}
		}
	}
	return nodes, frontier, warnings, nil
}

func (p *Planner) pruneAncestors(nodes map[plumbing.Hash]*planNode, frontier []plumbing.Hash, known func(plumbing.Hash) bool) error {
	seen := map[plumbing.Hash]bool{}
	queue := append([]plumbing.Hash(nil), frontier...)
	for _, h := range queue {
		seen[h] = true
	}
	for len(queue) > 0 && len(nodes) > 0 {
		h := queue[0]
		queue = queue[1:]
		var parents []plumbing.Hash
		if n, ok := nodes[h]; ok {
			delete(nodes, h)
			parents = n.parents
		} else {
			c, err := p.src.Commit(h)
			if err != nil {
				return fmt.Errorf("read commit %s: %w", h, err)
			}
			parents = c.ParentHashes
		}
		for _, parent := range parents {
			if !seen[parent] && !known(parent) {
				seen[parent] = true
				queue = append(queue, parent)
			}
		}
	}
	return nil
}

func dedupe(hashes []plumbing.Hash) []plumbing.Hash {
	out := make([]plumbing.Hash, 0, len(hashes))
	seen := map[plumbing.Hash]bool{}
	for _, h := range hashes {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func order(nodes map[plumbing.Hash]*planNode, sourceKey string, known func(plumbing.Hash) bool) []*PlannedCommit {
	ready := &nodeHeap{}
	for _, n := range nodes {
		var kept []plumbing.Hash
		for _, h := range n.parents {
			if parent, ok := nodes[h]; ok {
				kept = append(kept, h)
				parent.children = append(parent.children, n)
				n.pending++
			} else if known(h) {
				kept = append(kept, h)
			}
		}
		n.parents = kept
	}
	for _, n := range nodes {
		if n.pending == 0 {
			heap.Push(ready, n)
		}
	}
	out := make([]*PlannedCommit, 0, len(nodes))
	for ready.Len() > 0 {
		n := heap.Pop(ready).(*planNode)
		pc := &PlannedCommit{Hash: n.hash, Origin: Origin(sourceKey, n.hash), Parents: n.parents, CommitterTime: n.committed}
		for _, h := range n.parents {
			pc.ParentOrigins = append(pc.ParentOrigins, Origin(sourceKey, h))
		}
		out = append(out, pc)
		for _, child := range n.children {
			child.pending--
			if child.pending == 0 {
				heap.Push(ready, child)
			}
		}
	}
	return out
}

type nodeHeap []*planNode

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if !h[i].committed.Equal(h[j].committed) {
		return h[i].committed.Before(h[j].committed)
	}
	return h[i].hash.String() < h[j].hash.String()
}
func (h nodeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x any)   { *h = append(*h, x.(*planNode)) }
func (h *nodeHeap) Pop() any {
	old := *h
	n := old[len(old)-1]
	*h = old[:len(old)-1]
	return n
}

func (p *Planner) selectRefs(patterns []string) ([]PlannedRef, []Warning, error) {
	if len(patterns) == 0 {
		patterns = DefaultRefPatterns
	}
	matchers := make([]*regexp.Regexp, 0, len(patterns))
	for _, pat := range patterns {
		m, err := compileRefPattern(pat)
		if err != nil {
			return nil, nil, err
		}
		matchers = append(matchers, m)
	}
	all, warnings, err := p.src.Refs()
	if err != nil {
		return nil, nil, err
	}
	var selected []Ref
	for _, r := range all {
		for _, m := range matchers {
			if m.MatchString(r.Name.String()) {
				selected = append(selected, r)
				break
			}
		}
	}
	refs, nameWarnings := nameRefs(selected)
	return refs, append(warnings, nameWarnings...), nil
}

func compileRefPattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("empty ref pattern")
	}
	glob := func(p string) string {
		q := regexp.QuoteMeta(p)
		q = strings.ReplaceAll(q, `\*`, `.*`)
		return strings.ReplaceAll(q, `\?`, `.`)
	}
	expr := "^" + glob(pattern) + "$"
	if !strings.HasPrefix(pattern, "refs/") {
		expr = "^refs/(heads|tags)/" + glob(pattern) + "$"
	}
	return regexp.Compile(expr)
}

func SanitizeRefName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if domain.InvalidRefNameRune(r) {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if out == "." || out == ".." {
		out = strings.ReplaceAll(out, ".", "-")
	}
	return out
}

func nameRefs(refs []Ref) ([]PlannedRef, []Warning) {
	planned := make([]PlannedRef, 0, len(refs))
	for _, r := range refs {
		planned = append(planned, PlannedRef{Ref: r, Target: SanitizeRefName(r.Short())})
	}
	sort.SliceStable(planned, func(i, j int) bool {
		ei, ej := planned[i].Target == planned[i].Short(), planned[j].Target == planned[j].Short()
		if ei != ej {
			return ei
		}
		return planned[i].Name < planned[j].Name
	})
	taken := map[RefKind]map[string]string{RefBranch: {}, RefTag: {}}
	var out []PlannedRef
	var warnings []Warning
	for _, r := range planned {
		if owner, ok := taken[r.Kind][r.Target]; ok {
			warnings = append(warnings, Warning{Kind: WarnRefCollision, Ref: r.Name.String(), Message: fmt.Sprintf("%s name %q is already used by %s", r.Kind, r.Target, owner)})
			continue
		}
		taken[r.Kind][r.Target] = r.Name.String()
		if r.Target != r.Short() {
			warnings = append(warnings, Warning{Kind: WarnRefRenamed, Ref: r.Name.String(), Message: fmt.Sprintf("%s %q is imported as %q", r.Kind, r.Short(), r.Target)})
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings
}

func (p *Planner) defaultBranch(plan *Plan, override string) (string, error) {
	branches := plan.Branches()
	if override != "" {
		for _, b := range branches {
			if b.Target == override || b.Short() == override {
				return b.Target, nil
			}
		}
		return "", fmt.Errorf("default branch %q is not among the imported branches", override)
	}
	head, err := p.src.HeadBranch()
	if err != nil {
		return "", err
	}
	for _, b := range branches {
		if b.Name == head {
			return b.Target, nil
		}
	}
	for _, name := range []string{"main", "master"} {
		for _, b := range branches {
			if b.Target == name {
				return name, nil
			}
		}
	}
	if len(branches) > 0 {
		return branches[0].Target, nil
	}
	return "", nil
}

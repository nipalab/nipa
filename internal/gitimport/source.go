package gitimport

import (
	"errors"
	"fmt"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type RefKind string

const (
	RefBranch RefKind = "branch"
	RefTag    RefKind = "tag"
)

type Ref struct {
	Name      plumbing.ReferenceName
	Kind      RefKind
	Commit    plumbing.Hash
	Annotated bool
	Message   string
}

func (r Ref) Short() string {
	return r.Name.Short()
}

type Source struct {
	repo *git.Repository
}

func Open(path string) (*Source, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("open git repository %q: %w", path, err)
	}
	return &Source{repo: repo}, nil
}

func NewSource(repo *git.Repository) *Source {
	return &Source{repo: repo}
}

func (s *Source) Repository() *git.Repository {
	return s.repo
}

func (s *Source) Commit(h plumbing.Hash) (*object.Commit, error) {
	return s.repo.CommitObject(h)
}

func (s *Source) Refs() ([]Ref, []Warning, error) {
	iter, err := s.repo.References()
	if err != nil {
		return nil, nil, err
	}
	var refs []Ref
	var warnings []Warning
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() != plumbing.HashReference {
			return nil
		}
		switch {
		case ref.Name().IsBranch():
			if _, err := s.repo.CommitObject(ref.Hash()); err != nil {
				warnings = append(warnings, Warning{Kind: WarnRefSkipped, Ref: ref.Name().String(), Message: "branch does not point at a commit"})
				return nil
			}
			refs = append(refs, Ref{Name: ref.Name(), Kind: RefBranch, Commit: ref.Hash()})
		case ref.Name().IsTag():
			r, err := s.peelTag(ref)
			if err != nil {
				warnings = append(warnings, Warning{Kind: WarnRefSkipped, Ref: ref.Name().String(), Message: err.Error()})
				return nil
			}
			refs = append(refs, r)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, warnings, nil
}

const maxTagChain = 16

func (s *Source) peelTag(ref *plumbing.Reference) (Ref, error) {
	out := Ref{Name: ref.Name(), Kind: RefTag}
	target := ref.Hash()
	for range maxTagChain {
		tag, err := s.repo.TagObject(target)
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			break
		}
		if err != nil {
			return Ref{}, err
		}
		if !out.Annotated {
			out.Annotated = true
			out.Message = tag.Message
		}
		if tag.TargetType != plumbing.CommitObject && tag.TargetType != plumbing.TagObject {
			return Ref{}, fmt.Errorf("tag points at a %s, not a commit", tag.TargetType)
		}
		target = tag.Target
	}
	if _, err := s.repo.CommitObject(target); err != nil {
		return Ref{}, fmt.Errorf("tag does not point at a commit")
	}
	out.Commit = target
	return out, nil
}

func (s *Source) HeadBranch() (plumbing.ReferenceName, error) {
	head, err := s.repo.Storer.Reference(plumbing.HEAD)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if head.Type() == plumbing.SymbolicReference && head.Target().IsBranch() {
		return head.Target(), nil
	}
	return "", nil
}

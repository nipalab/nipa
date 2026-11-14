package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const maxConflictText = 4 << 20

type MergeOutcome struct {
	UpToDate       bool     `json:"upToDate"`
	FastForwarded  bool     `json:"fastForwarded"`
	MergeCommitted bool     `json:"mergeCommitted"`
	Aborted        bool     `json:"aborted"`
	Conflicts      []string `json:"conflicts"`
}

type ConflictRevision struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type ConflictFile struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Binary   bool   `json:"binary"`
	Resolved bool   `json:"resolved"`
	Viewer   string `json:"viewer"`
}

// ConflictSet is the merge or revert waiting in a clone. Op is empty when
// nothing waits.
type ConflictSet struct {
	Op     string           `json:"op"`
	Base   ConflictRevision `json:"base"`
	Ours   ConflictRevision `json:"ours"`
	Theirs ConflictRevision `json:"theirs"`
	Files  []ConflictFile   `json:"files"`
}

// ConflictText is one version of a conflicted text file. Missing means the
// file does not exist in that version.
type ConflictText struct {
	Text    string `json:"text"`
	Missing bool   `json:"missing"`
}

type ConflictTexts struct {
	Base    ConflictText `json:"base"`
	Ours    ConflictText `json:"ours"`
	Theirs  ConflictText `json:"theirs"`
	Working ConflictText `json:"working"`
}

type ResolveInput struct {
	Path    string `json:"path"`
	Choice  string `json:"choice"` // ours, theirs, content, working, delete
	Content string `json:"content"`
}

// Merge merges a branch into the clone's branch. Conflicts stop the merge and
// leave it waiting for ResolveConflict and a push, or AbortMerge.
func (s *Service) Merge(ctx context.Context, root, source string) (OpOutcome, error) {
	if strings.TrimSpace(source) == "" {
		return OpOutcome{}, errors.New("choose a branch to merge")
	}
	return s.runOp(ctx, root, "merge", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Merge(ctx, &daemonpb.MergeOpRequest{Root: root, SourceBranch: source})
	})
}

// AbortMerge drops a waiting merge and restores the branch head.
func (s *Service) AbortMerge(ctx context.Context, root string) (OpOutcome, error) {
	return s.runOp(ctx, root, "merge", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Merge(ctx, &daemonpb.MergeOpRequest{Root: root, Abort: true})
	})
}

// Conflicts describes the waiting merge or revert and its conflicted files.
func (s *Service) Conflicts(ctx context.Context, root string) (*ConflictSet, error) {
	var resp *daemonpb.ConflictsResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		var err error
		resp, err = c.Conflicts(ctx, &daemonpb.ConflictsRequest{Root: root})
		return err
	})
	if err != nil {
		return nil, err
	}
	rev := func(r *daemonpb.ConflictRevision) ConflictRevision {
		return ConflictRevision{ID: r.GetId(), Label: r.GetLabel()}
	}
	out := &ConflictSet{Op: resp.GetOp(), Base: rev(resp.GetBase()), Ours: rev(resp.GetOurs()), Theirs: rev(resp.GetTheirs()), Files: []ConflictFile{}}
	for _, f := range resp.GetFiles() {
		out.Files = append(out.Files, ConflictFile{Path: f.GetPath(), Kind: f.GetKind(), Binary: f.GetBinary(), Resolved: f.GetResolved(), Viewer: f.GetViewer()})
	}
	return out, nil
}

// ConflictTexts reads the base, ours, theirs and working versions of a
// conflicted text file for the three-way editor.
func (s *Service) ConflictTexts(ctx context.Context, root, path string) (*ConflictTexts, error) {
	set, err := s.Conflicts(ctx, root)
	if err != nil {
		return nil, err
	}
	if set.Op == "" {
		return nil, errors.New("no merge or revert is waiting")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	read := func(ref *daemonpb.BlobRef) (ConflictText, error) {
		if ref.GetSource() == "revision" && ref.GetRevision() == "" {
			return ConflictText{Missing: true}, nil
		}
		_, data, err := s.readBlob(ctx, client, root, ref, maxConflictText)
		switch {
		case errors.Is(err, errBlobMissing):
			return ConflictText{Missing: true}, nil
		case errors.Is(err, errBlobTooLarge):
			return ConflictText{}, errors.New(path + " is too large to edit here; open it in your editor")
		case err != nil:
			return ConflictText{}, err
		case !utf8.Valid(data):
			return ConflictText{}, errors.New(path + " is not text; pick a version instead")
		}
		return ConflictText{Text: string(data)}, nil
	}
	revision := func(id string) *daemonpb.BlobRef {
		return &daemonpb.BlobRef{Source: "revision", Revision: id, Path: path}
	}
	out := &ConflictTexts{}
	for _, side := range []struct {
		dst *ConflictText
		ref *daemonpb.BlobRef
	}{
		{&out.Base, revision(set.Base.ID)},
		{&out.Ours, revision(set.Ours.ID)},
		{&out.Theirs, revision(set.Theirs.ID)},
		{&out.Working, &daemonpb.BlobRef{Source: "working", Path: path}},
	} {
		if *side.dst, err = read(side.ref); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ConflictDiff compares the two sides of a conflicted file, ours as the old
// side and theirs as the new, for the asset viewers.
func (s *Service) ConflictDiff(ctx context.Context, root, path string) (*FileDiff, error) {
	set, err := s.Conflicts(ctx, root)
	if err != nil {
		return nil, err
	}
	if set.Op == "" {
		return nil, errors.New("no merge or revert is waiting")
	}
	if set.Ours.ID == "" || set.Theirs.ID == "" {
		return nil, nil
	}
	files, err := s.revisionFiles(ctx, root, set.Ours.ID, set.Theirs.ID, []string{path})
	if err != nil {
		return nil, err
	}
	for i := range files {
		if files[i].Path == path || files[i].OldPath == path {
			files[i].OldRevision, files[i].NewRevision = set.Ours.ID, set.Theirs.ID
			return &files[i], nil
		}
	}
	return nil, nil
}

type SceneMergeConflict struct {
	Section string `json:"section"`
	Key     string `json:"key"`
	Message string `json:"message"`
}

// SceneMerge is a Godot scene or resource merged node by node. Merged is only
// a best effort (ours wins) when Clean is false.
type SceneMerge struct {
	Merged    string               `json:"merged"`
	Clean     bool                 `json:"clean"`
	Conflicts []SceneMergeConflict `json:"conflicts"`
}

// MergeScene merges a conflicted .tscn/.tres by nodes and properties. It only
// reads: a clean result is applied with ResolveConflict.
func (s *Service) MergeScene(ctx context.Context, root, path string) (*SceneMerge, error) {
	var resp *daemonpb.MergeGodotResourceResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		var err error
		resp, err = c.MergeGodotResource(ctx, &daemonpb.MergeGodotResourceRequest{Root: root, Path: path})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := &SceneMerge{Merged: string(resp.GetMerged()), Clean: resp.GetClean(), Conflicts: []SceneMergeConflict{}}
	for _, c := range resp.GetConflicts() {
		out.Conflicts = append(out.Conflicts, SceneMergeConflict{Section: c.GetSection(), Key: c.GetKey(), Message: c.GetMessage()})
	}
	return out, nil
}

// ResolveConflict settles one conflicted file and returns the refreshed
// changes.
func (s *Service) ResolveConflict(ctx context.Context, root string, in ResolveInput) (Changes, error) {
	switch in.Choice {
	case "ours", "theirs", "content", "working", "delete":
	default:
		return Changes{}, errors.New("unknown choice " + in.Choice)
	}
	if strings.TrimSpace(in.Path) == "" {
		return Changes{}, errors.New("choose a file")
	}
	return s.statusCall(ctx, root, func(c daemonpb.NipaDaemonClient) (*daemonpb.StatusResponse, error) {
		return c.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: in.Path, Choice: in.Choice, Content: []byte(in.Content)})
	})
}

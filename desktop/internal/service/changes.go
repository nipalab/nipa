package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/preview"
)

// maxUntrackedText bounds how much of a new, untracked text file is turned
// into a diff; larger files only get metadata, like the daemon's hunk budget.
const maxUntrackedText = 1 << 20

type FileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added, modified, deleted, untracked
	Kind   string `json:"kind"`
}

type Changes struct {
	Branch    string       `json:"branch"`
	Detached  string       `json:"detached,omitempty"` // tag name while HEAD is detached
	Staged    []FileChange `json:"staged"`
	Unstaged  []FileChange `json:"unstaged"`
	Conflicts []string     `json:"conflicts"`
	Resolved  []string     `json:"resolved"`
	// Pending is merge or revert while one waits for continue or abort.
	Pending          string `json:"pending,omitempty"`
	PendingRemaining int    `json:"pendingRemaining,omitempty"`
}

type FileSide struct {
	Path   string `json:"path"`
	Mode   int    `json:"mode"`
	Size   int64  `json:"size"`
	Binary bool   `json:"binary"`
	Hash   string `json:"hash"`
}

type DiffLine struct {
	Kind      string `json:"kind"` // context, added, removed
	Old       int64  `json:"old"`  // 0 when the line only exists on the new side
	New       int64  `json:"new"`  // 0 when the line only exists on the old side
	Text      string `json:"text"`
	NoNewline bool   `json:"noNewline,omitempty"`
}

type DiffHunk struct {
	OldStart int64      `json:"oldStart"`
	OldLines int64      `json:"oldLines"`
	NewStart int64      `json:"newStart"`
	NewLines int64      `json:"newLines"`
	Lines    []DiffLine `json:"lines"`
}

type FileDiff struct {
	Path               string     `json:"path"`
	OldPath            string     `json:"oldPath,omitempty"`
	Status             string     `json:"status"` // added, modified, deleted, renamed
	Kind               string     `json:"kind"`
	Similarity         int        `json:"similarity,omitempty"`
	Old                *FileSide  `json:"old"`
	New                *FileSide  `json:"new"`
	Additions          int64      `json:"additions"`
	Deletions          int64      `json:"deletions"`
	Hunks              []DiffHunk `json:"hunks"`
	HunksOmitted       bool       `json:"hunksOmitted,omitempty"`
	ContentUnavailable bool       `json:"contentUnavailable,omitempty"`
	Untracked          bool       `json:"untracked,omitempty"`
	// OldRevision and NewRevision are set for a commit's changes; the working
	// copy diff leaves them empty.
	OldRevision string `json:"oldRevision,omitempty"`
	NewRevision string `json:"newRevision,omitempty"`
	// OldSource and NewSource name a blob source other than revision for
	// the sides (shelf_base and shelf, with the shelf id as revision).
	OldSource string `json:"oldSource,omitempty"`
	NewSource string `json:"newSource,omitempty"`
}

// Changes returns the working-copy status of a clone, grouped the way the
// Changes screen shows it.
func (s *Service) Changes(ctx context.Context, root string) (Changes, error) {
	return s.statusCall(ctx, root, func(c daemonpb.NipaDaemonClient) (*daemonpb.StatusResponse, error) {
		return c.Status(ctx, &daemonpb.StatusRequest{Root: root})
	})
}

// Stage adds files (including deletions) to the next push.
func (s *Service) Stage(ctx context.Context, root string, paths []string) (Changes, error) {
	return s.statusCall(ctx, root, func(c daemonpb.NipaDaemonClient) (*daemonpb.StatusResponse, error) {
		return c.Stage(ctx, &daemonpb.StageRequest{Root: root, Add: paths})
	})
}

// Unstage drops files from the next push; their edits stay on disk.
func (s *Service) Unstage(ctx context.Context, root string, paths []string) (Changes, error) {
	return s.statusCall(ctx, root, func(c daemonpb.NipaDaemonClient) (*daemonpb.StatusResponse, error) {
		return c.Stage(ctx, &daemonpb.StageRequest{Root: root, Unstage: paths})
	})
}

// Discard restores tracked files to the last synced version.
func (s *Service) Discard(ctx context.Context, root string, paths []string) (Changes, error) {
	return s.statusCall(ctx, root, func(c daemonpb.NipaDaemonClient) (*daemonpb.StatusResponse, error) {
		return c.Restore(ctx, &daemonpb.RestoreRequest{Root: root, Paths: paths})
	})
}

func (s *Service) statusCall(ctx context.Context, root string, call func(daemonpb.NipaDaemonClient) (*daemonpb.StatusResponse, error)) (Changes, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return Changes{}, err
	}
	resp, err := call(client)
	if err != nil {
		s.dropIfDisconnected(err)
		return Changes{}, cleanError(err)
	}
	return toChanges(resp), nil
}

func toChanges(resp *daemonpb.StatusResponse) Changes {
	added := map[string]bool{}
	for _, p := range resp.GetAdded() {
		added[p] = true
	}
	out := Changes{
		Branch:           resp.GetBranch(),
		Staged:           []FileChange{},
		Unstaged:         []FileChange{},
		Conflicts:        append([]string{}, resp.GetConflicts()...),
		Resolved:         append([]string{}, resp.GetResolved()...),
		Pending:          resp.GetPending(),
		PendingRemaining: int(resp.GetPendingRemaining()),
	}
	if head := resp.GetHead(); head != nil {
		out.Detached = head.GetName()
	}
	for _, p := range resp.GetStaged() {
		status := "modified"
		if added[p] {
			status = "added"
		}
		out.Staged = append(out.Staged, change(p, status))
	}
	for _, p := range resp.GetDeleted() {
		out.Staged = append(out.Staged, change(p, "deleted"))
	}
	for _, p := range resp.GetModified() {
		out.Unstaged = append(out.Unstaged, change(p, "modified"))
	}
	for _, p := range resp.GetMissing() {
		out.Unstaged = append(out.Unstaged, change(p, "deleted"))
	}
	for _, p := range resp.GetUntracked() {
		out.Unstaged = append(out.Unstaged, change(p, "untracked"))
	}
	byPath := func(list []FileChange) func(i, j int) bool {
		return func(i, j int) bool { return list[i].Path < list[j].Path }
	}
	sort.Slice(out.Staged, byPath(out.Staged))
	sort.Slice(out.Unstaged, byPath(out.Unstaged))
	return out
}

func change(path, status string) FileChange {
	return FileChange{Path: path, Status: status, Kind: string(preview.Classify(path, false))}
}

// FileDiff diffs one working-copy file against the last synced version. A new
// untracked file is shown as a full addition; nil means the file has no
// change anymore.
func (s *Service) FileDiff(ctx context.Context, root, path string) (*FileDiff, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	stream, err := client.DiffFiles(ctx, &daemonpb.DiffRequest{Root: root, Paths: []string{path}})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	var found *FileDiff
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.dropIfDisconnected(err)
			return nil, cleanError(err)
		}
		if f := ev.GetFailure(); f != nil {
			return nil, errors.New(f.GetMessage())
		}
		if file := ev.GetFile(); file != nil && found == nil && (file.GetPath() == path || file.GetOld().GetPath() == path) {
			found = toFileDiff(file)
		}
	}
	if found != nil {
		return found, nil
	}
	resp, err := client.Status(ctx, &daemonpb.StatusRequest{Root: root})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	if !slices.Contains(resp.GetUntracked(), path) {
		return nil, nil
	}
	return s.untrackedDiff(ctx, client, root, path)
}

func (s *Service) untrackedDiff(ctx context.Context, client daemonpb.NipaDaemonClient, root, path string) (*FileDiff, error) {
	stream, err := client.ReadBlob(ctx, &daemonpb.ReadBlobRequest{Root: root, Blob: &daemonpb.BlobRef{Source: "working", Path: path}})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	var info *daemonpb.BlobInfo
	var content bytes.Buffer
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.dropIfDisconnected(err)
			return nil, cleanError(err)
		}
		switch {
		case ev.GetFailure() != nil:
			if ev.GetFailure().GetCode() == 404 {
				return nil, nil
			}
			return nil, errors.New(ev.GetFailure().GetMessage())
		case ev.GetInfo() != nil:
			info = ev.GetInfo()
			if info.GetIsBinary() || info.GetSizeBytes() > maxUntrackedText {
				return untrackedMeta(info), nil
			}
		default:
			content.Write(ev.GetData())
		}
	}
	if info == nil {
		return nil, nil
	}
	diff := untrackedMeta(info)
	diff.HunksOmitted = false
	if content.Len() == 0 {
		return diff, nil
	}
	text := content.String()
	noNewline := !strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	hunk := DiffHunk{NewStart: 1, NewLines: int64(len(lines)), Lines: make([]DiffLine, len(lines))}
	for i, line := range lines {
		hunk.Lines[i] = DiffLine{Kind: "added", New: int64(i + 1), Text: line}
	}
	hunk.Lines[len(lines)-1].NoNewline = noNewline
	diff.Hunks = []DiffHunk{hunk}
	diff.Additions = int64(len(lines))
	return diff, nil
}

func untrackedMeta(info *daemonpb.BlobInfo) *FileDiff {
	return &FileDiff{
		Path:         info.GetPath(),
		Status:       "added",
		Kind:         info.GetKind(),
		New:          &FileSide{Path: info.GetPath(), Size: info.GetSizeBytes(), Binary: info.GetIsBinary()},
		Hunks:        []DiffHunk{},
		HunksOmitted: !info.GetIsBinary(),
		Untracked:    true,
	}
}

func toFileDiff(f *daemonpb.DiffFile) *FileDiff {
	out := &FileDiff{
		Path:               f.GetPath(),
		Status:             f.GetStatus(),
		Kind:               f.GetKind(),
		Similarity:         int(f.GetSimilarity()),
		Old:                toSide(f.GetOld()),
		New:                toSide(f.GetNew()),
		Additions:          f.GetAdditions(),
		Deletions:          f.GetDeletions(),
		Hunks:              make([]DiffHunk, 0, len(f.GetHunks())),
		HunksOmitted:       f.GetHunksOmitted(),
		ContentUnavailable: f.GetContentUnavailable(),
	}
	if out.Status == "renamed" {
		out.OldPath = f.GetOld().GetPath()
	}
	for _, h := range f.GetHunks() {
		hunk := DiffHunk{
			OldStart: h.GetOldStart(),
			OldLines: h.GetOldLines(),
			NewStart: h.GetNewStart(),
			NewLines: h.GetNewLines(),
			Lines:    make([]DiffLine, 0, len(h.GetLines())),
		}
		for _, l := range h.GetLines() {
			hunk.Lines = append(hunk.Lines, DiffLine{
				Kind:      lineKind(l.GetKind()),
				Old:       l.GetOldLine(),
				New:       l.GetNewLine(),
				Text:      l.GetText(),
				NoNewline: l.GetNoNewline(),
			})
		}
		out.Hunks = append(out.Hunks, hunk)
	}
	return out
}

// lineKind maps the daemon's add/remove/context onto the GUI's names.
func lineKind(kind string) string {
	switch kind {
	case "add":
		return "added"
	case "remove":
		return "removed"
	}
	return "context"
}

func toSide(side *daemonpb.DiffFileSide) *FileSide {
	if side == nil {
		return nil
	}
	return &FileSide{
		Path:   side.GetPath(),
		Mode:   int(side.GetMode()),
		Size:   side.GetSizeBytes(),
		Binary: side.GetIsBinary(),
		Hash:   side.GetHash(),
	}
}

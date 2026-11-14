package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type ShelfEntry struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // modified, added, deleted
	Staged    bool   `json:"staged"`
	Untracked bool   `json:"untracked"`
	Locked    bool   `json:"locked"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary"`
	Kind      string `json:"kind"`
}

type Shelf struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	CreatedAt    string       `json:"createdAt"`
	Branch       string       `json:"branch"`
	Tag          string       `json:"tag,omitempty"`
	BaseCommitID string       `json:"baseCommitId,omitempty"`
	Size         int64        `json:"size"`
	Entries      []ShelfEntry `json:"entries"`
}

// ShelfList is a clone's shelves, newest first. Applying names the shelf
// whose apply waits on conflicts.
type ShelfList struct {
	Shelves  []Shelf `json:"shelves"`
	Applying string  `json:"applying,omitempty"`
}

type ShelveInput struct {
	Name             string   `json:"name"`
	Paths            []string `json:"paths"`
	IncludeUntracked bool     `json:"includeUntracked"`
}

type ShelfApplyOutcome struct {
	ShelfID   string   `json:"shelfId"`
	Name      string   `json:"name"`
	Conflicts []string `json:"conflicts"`
	Dropped   bool     `json:"dropped"`
	Aborted   bool     `json:"aborted"`
}

// SyncPlan lists the local changes an update, switch or tag checkout would
// replace.
type SyncPlan struct {
	Overwritten []string `json:"overwritten"`
}

func (s *Service) Shelves(ctx context.Context, root string) (ShelfList, error) {
	var resp *daemonpb.ShelveListResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		var err error
		resp, err = c.ShelveList(ctx, &daemonpb.ShelveListRequest{Root: root})
		return err
	})
	if err != nil {
		return ShelfList{}, err
	}
	out := ShelfList{Shelves: []Shelf{}, Applying: resp.GetApplying()}
	for _, sh := range resp.GetShelves() {
		out.Shelves = append(out.Shelves, toShelf(sh))
	}
	return out, nil
}

// Shelve parks the given changes (everything when Paths is empty) and puts
// those files back to the synced version.
func (s *Service) Shelve(ctx context.Context, root string, in ShelveInput) (OpOutcome, error) {
	return s.runOp(ctx, root, "shelve", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.ShelveCreate(ctx, &daemonpb.ShelveCreateRequest{Root: root, Name: strings.TrimSpace(in.Name), Paths: in.Paths, IncludeUntracked: in.IncludeUntracked})
	})
}

// ApplyShelf brings a shelf back; conflicts wait in the Conflicts tab.
func (s *Service) ApplyShelf(ctx context.Context, root, id string, keep bool) (OpOutcome, error) {
	if id == "" {
		return OpOutcome{}, errors.New("choose a shelf")
	}
	return s.runOp(ctx, root, "shelve", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.ShelveApply(ctx, &daemonpb.ShelveApplyRequest{Root: root, Id: id, Keep: keep})
	})
}

// AbortShelfApply stops the waiting apply and puts the files back; the
// shelf is kept.
func (s *Service) AbortShelfApply(ctx context.Context, root string) (OpOutcome, error) {
	return s.runOp(ctx, root, "shelve", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.ShelveApply(ctx, &daemonpb.ShelveApplyRequest{Root: root, Abort: true})
	})
}

func (s *Service) DropShelf(ctx context.Context, root, id string) error {
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ShelveDrop(ctx, &daemonpb.ShelveDropRequest{Root: root, Id: id})
		return err
	})
}

// ShelfFiles lists a shelf's changes for the asset viewers: the old side is
// the version the files were shelved from, the new side the shelf.
func (s *Service) ShelfFiles(ctx context.Context, root, id string) ([]FileDiff, error) {
	var resp *daemonpb.ShelveFilesResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		var err error
		resp, err = c.ShelveFiles(ctx, &daemonpb.ShelveFilesRequest{Root: root, Id: id})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := []FileDiff{}
	for _, f := range resp.GetFiles() {
		d := toFileDiff(f)
		d.OldSource, d.NewSource = string(usecase.BlobShelfBase), string(usecase.BlobShelf)
		d.OldRevision, d.NewRevision = id, id
		out = append(out, *d)
	}
	return out, nil
}

// SyncPlan lists the local changes that updating (branch and tag empty),
// switching to branch or checking out tag would replace.
func (s *Service) SyncPlan(ctx context.Context, root, branch, tag string) (SyncPlan, error) {
	var resp *daemonpb.SyncPlanResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		var err error
		resp, err = c.SyncPlan(ctx, &daemonpb.SyncPlanRequest{Root: root, Branch: branch, Tag: tag})
		return err
	})
	if err != nil {
		return SyncPlan{}, err
	}
	return SyncPlan{Overwritten: append([]string{}, resp.GetOverwritten()...)}, nil
}

func toShelf(sh *daemonpb.Shelf) Shelf {
	out := Shelf{
		ID:           sh.GetId(),
		Name:         sh.GetName(),
		CreatedAt:    time.Unix(sh.GetCreatedAt(), 0).UTC().Format(time.RFC3339),
		Branch:       sh.GetBranch(),
		Tag:          sh.GetTag(),
		BaseCommitID: sh.GetBaseCommitId(),
		Size:         sh.GetSizeBytes(),
		Entries:      []ShelfEntry{},
	}
	for _, e := range sh.GetEntries() {
		out.Entries = append(out.Entries, ShelfEntry{
			Path:      e.GetPath(),
			Status:    e.GetStatus(),
			Staged:    e.GetStaged(),
			Untracked: e.GetUntracked(),
			Locked:    e.GetLocked(),
			Size:      e.GetSizeBytes(),
			Binary:    e.GetIsBinary(),
			Kind:      e.GetKind(),
		})
	}
	return out
}

func toShelfApply(r *daemonpb.ShelveApplyResult) *ShelfApplyOutcome {
	return &ShelfApplyOutcome{
		ShelfID:   r.GetShelfId(),
		Name:      r.GetName(),
		Conflicts: append([]string{}, r.GetConflicts()...),
		Dropped:   r.GetDropped(),
		Aborted:   r.GetAborted(),
	}
}

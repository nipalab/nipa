package output

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestNewDiff_ModifiedText(t *testing.T) {
	f := diff.FileDiff{
		Change: diff.Change{
			Path:   "a.txt",
			Status: diff.Modified,
			Old:    diff.Entry{Mode: 2, SizeBytes: 14, Hash: serverDomain.Hash{0x01}},
			New:    diff.Entry{Mode: 2, SizeBytes: 14, Hash: serverDomain.Hash{0x02}},
		},
		Old: []byte("one\ntwo\nthree\n"),
		New: []byte("one\nTWO\nthree\n"),
	}
	out := NewDiff([]diff.FileDiff{f}, diff.Options{Context: 3})
	require.Len(t, out.Changes, 1)
	file := out.Changes[0]
	require.Equal(t, "a.txt", file.Path)
	require.Equal(t, "M", file.Status)
	require.False(t, file.Binary)
	require.NotNil(t, file.Old)
	require.NotNil(t, file.New)
	require.Equal(t, "100644", file.New.Mode)
	require.Equal(t, int64(14), file.New.SizeBytes)
	require.NotEmpty(t, file.New.Hash)
	require.Len(t, file.Hunks, 1)
	hunk := file.Hunks[0]
	require.Equal(t, 1, hunk.OldStart)
	require.Equal(t, 3, hunk.OldLines)
	require.Equal(t, 1, hunk.NewStart)
	require.Equal(t, 3, hunk.NewLines)
	require.Equal(t, []string{"context", "delete", "add", "context"}, diffLineKinds(hunk.Lines))
	require.Equal(t, "two", hunk.Lines[1].Text)
	require.Equal(t, "TWO", hunk.Lines[2].Text)
}

func TestNewDiff_Added(t *testing.T) {
	f := diff.FileDiff{
		Change: diff.Change{Path: "new.txt", Status: diff.Added, New: diff.Entry{Mode: 2}},
		New:    []byte("hello\n"),
	}
	out := NewDiff([]diff.FileDiff{f}, diff.Options{})
	file := out.Changes[0]
	require.Equal(t, "A", file.Status)
	require.Nil(t, file.Old)
	require.NotNil(t, file.New)
	require.Len(t, file.Hunks, 1)
	require.Equal(t, []string{"add"}, diffLineKinds(file.Hunks[0].Lines))
}

func TestNewDiff_Deleted(t *testing.T) {
	f := diff.FileDiff{
		Change: diff.Change{Path: "old.txt", Status: diff.Deleted, Old: diff.Entry{Mode: 2}},
		Old:    []byte("gone\n"),
	}
	out := NewDiff([]diff.FileDiff{f}, diff.Options{})
	file := out.Changes[0]
	require.Equal(t, "D", file.Status)
	require.NotNil(t, file.Old)
	require.Nil(t, file.New)
	require.Equal(t, []string{"delete"}, diffLineKinds(file.Hunks[0].Lines))
}

func TestNewDiff_Renamed(t *testing.T) {
	f := diff.FileDiff{
		Change: diff.Change{
			Path:       "b.txt",
			Status:     diff.Renamed,
			Old:        diff.Entry{Path: "a.txt", Mode: 2, Hash: serverDomain.Hash{0x01}},
			New:        diff.Entry{Path: "b.txt", Mode: 2, Hash: serverDomain.Hash{0x02}},
			Similarity: 90,
		},
		Old: []byte("one\n"),
		New: []byte("two\n"),
	}
	out := NewDiff([]diff.FileDiff{f}, diff.Options{})
	file := out.Changes[0]
	require.Equal(t, "R", file.Status)
	require.Equal(t, "a.txt", file.OldPath)
	require.Equal(t, 90, file.Similarity)
	require.NotEmpty(t, file.Hunks)
}

func TestNewDiff_BinaryAndUnavailable(t *testing.T) {
	binary := diff.FileDiff{Change: diff.Change{
		Path:   "tex.png",
		Status: diff.Modified,
		Old:    diff.Entry{Mode: 2, IsBinary: true},
		New:    diff.Entry{Mode: 2, IsBinary: true},
	}}
	unavailable := diff.FileDiff{
		Change:         diff.Change{Path: "missing.bin", Status: diff.Modified, Old: diff.Entry{Mode: 2}, New: diff.Entry{Mode: 2}},
		OldUnavailable: true,
		NewUnavailable: true,
	}
	out := NewDiff([]diff.FileDiff{binary, unavailable}, diff.Options{})
	require.True(t, out.Changes[0].Binary)
	require.Empty(t, out.Changes[0].Hunks)
	require.True(t, out.Changes[1].ContentUnavailable)
	require.Empty(t, out.Changes[1].Hunks)
}

func TestNewLog(t *testing.T) {
	parent := snow.ID(9001)
	entries := []*serverDomain.CommitLogEntry{
		{
			Commit: serverDomain.Commit{
				ID:        9002,
				Hash:      serverDomain.Hash{0xab},
				Message:   "second commit\nwith body",
				Parent1ID: &parent,
				CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			},
			AuthorName:  "Bob",
			AuthorEmail: "bob@example.com",
		},
		nil,
	}
	out := NewLog(entries)
	require.Len(t, out.Commits, 1)
	commit := out.Commits[0]
	require.Equal(t, "6y2", commit.ID)
	require.NotEmpty(t, commit.Hash)
	require.Equal(t, "second commit\nwith body", commit.Message)
	require.Equal(t, "Bob", commit.AuthorName)
	require.Equal(t, "bob@example.com", commit.AuthorEmail)
	require.Equal(t, "2026-01-02T00:00:00Z", commit.CreatedAt)
	require.Equal(t, []string{"6y1"}, commit.ParentIDs)
}

func TestNewBranches(t *testing.T) {
	commit := snow.ID(9001)
	out := NewBranches("main", []*serverDomain.Branch{
		{Name: "main", IsDefault: true, CommitID: &commit, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Name: "dev", IsProtected: true},
		nil,
	})
	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, out))
	require.Len(t, out.Branches, 2)
	require.True(t, out.Branches[0].Current)
	require.True(t, out.Branches[0].Default)
	require.Equal(t, "6y1", out.Branches[0].CommitID)
	require.Equal(t, "2026-01-01T00:00:00Z", out.Branches[0].UpdatedAt)
	require.False(t, out.Branches[1].Current)
	require.True(t, out.Branches[1].Protected)
	require.Empty(t, out.Branches[1].UpdatedAt)
}

func TestNewLocks(t *testing.T) {
	mrNumber := int64(3)
	out := NewLocks([]*clientDomain.FileLock{
		{
			Path:               "art/tex.png",
			Global:             true,
			HeldBy:             "u1",
			HeldByName:         "bob",
			MergeRequestNumber: &mrNumber,
			AcquiredAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{Path: "audio/loop.wav", Branch: "feature", HeldBy: "u2"},
		nil,
	})
	require.Len(t, out.Locks, 2)
	require.Equal(t, "mainline", out.Locks[0].Scope)
	require.Equal(t, int64(3), *out.Locks[0].MergeRequestNumber)
	require.Equal(t, "2026-01-01T00:00:00Z", out.Locks[0].AcquiredAt)
	require.Equal(t, "branch", out.Locks[1].Scope)
	require.Equal(t, "feature", out.Locks[1].Branch)
	require.Nil(t, out.Locks[1].MergeRequestNumber)
}

func TestNewMergeRequests(t *testing.T) {
	out := NewMergeRequests([]*clientDomain.MergeRequest{
		{
			ID:           "abc",
			Number:       1,
			SourceBranch: "feature",
			TargetBranch: "main",
			Title:        "Add b",
			Status:       clientDomain.MergeRequestOpen,
			CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Review: &clientDomain.MergeRequestReviewState{
				Approvals:            1,
				ChangesRequested:     2,
				DismissedApprovals:   3,
				OutstandingReviewers: []string{"rev"},
			},
		},
		nil,
	})
	require.Len(t, out.MergeRequests, 1)
	mr := out.MergeRequests[0]
	require.Equal(t, "abc", mr.ID)
	require.Equal(t, int64(1), mr.Number)
	require.Equal(t, "feature", mr.SourceBranch)
	require.Equal(t, "open", mr.Status)
	require.Equal(t, "2026-01-01T00:00:00Z", mr.CreatedAt)
	require.Empty(t, mr.UpdatedAt)
	require.NotNil(t, mr.Review)
	require.Equal(t, 1, mr.Review.Approvals)
	require.Equal(t, 2, mr.Review.ChangesRequested)
	require.Equal(t, []string{"rev"}, mr.Review.OutstandingReviewers)
}

func diffLineKinds(lines []DiffLine) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.Kind
	}
	return out
}

package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func (f *importFixture) targets(commits ...*domain.Commit) {
	f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(commits, nil)
}

func (f *importFixture) locked() {
	f.locker.EXPECT().LockProject(gomock.Any(), importProject).Return(nil)
}

func TestImportSetRefs_Refusals(t *testing.T) {
	t.Run("no claim", func(t *testing.T) {
		f := newImportFixture(t)
		_, err := f.uc.SetRefs(context.Background(), importProject, SetRefsInput{})
		require.True(t, domain.IsErrorNoPermission(err))
	})
	t.Run("writer is not enough", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(false)
		_, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{})
		require.True(t, domain.IsErrorNoPermission(err))
	})
	t.Run("read-only store", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(true)
		f.storage.EXPECT().RequireWritable(gomock.Any(), importProject).Return(domain.NewErrorQuotaExceeded("read only"))
		_, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{})
		require.ErrorContains(t, err, "read only")
	})
	t.Run("unknown commit", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		f.targets()
		_, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{Branches: []ImportRef{{Name: "main", Origin: gitOrigin(1)}}})
		require.ErrorContains(t, err, "has not been imported")
	})
	t.Run("duplicate ref", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		_, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{Tags: []ImportRef{{Name: "v1", Origin: gitOrigin(1)}, {Name: "v1", Origin: gitOrigin(2)}}})
		require.ErrorContains(t, err, "listed twice")
	})
	t.Run("lookup error", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, errors.New("db"))
		_, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{Branches: []ImportRef{{Name: "main", Origin: gitOrigin(1)}}})
		require.EqualError(t, err, "db")
	})
	t.Run("lock error", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		f.locker.EXPECT().LockProject(gomock.Any(), importProject).Return(errors.New("lock"))
		_, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{})
		require.EqualError(t, err, "lock")
		require.Empty(t, f.audit.entries)
	})
}

func TestImportSetRefs_CreatesRefsAndDefault(t *testing.T) {
	f := newImportFixture(t)
	f.admin()
	tip, release := storedCommit(5, gitOrigin(1)), storedCommit(6, gitOrigin(2))
	f.targets(tip, release)
	f.locked()

	f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "trunk").Return(nil, domain.NewErrorNotFound("x"))
	f.branches.EXPECT().ListBranches(gomock.Any(), importProject, 1, nil, snow.ID(0)).Return(nil, nil)
	f.branches.EXPECT().CreateBranch(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, b domain.Branch) (*domain.Branch, error) {
		require.Equal(t, snow.ID(5), *b.CommitID)
		require.True(t, b.IsDefault, "the first branch of a project is its default")
		return &b, nil
	})
	f.tags.EXPECT().GetTagByName(gomock.Any(), importProject, "v1").Return(nil, domain.NewErrorNotFound("x"))
	f.tags.EXPECT().CreateTag(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
		require.Equal(t, snow.ID(6), tag.CommitID)
		require.Equal(t, "release notes", tag.Message)
		require.Equal(t, importer, tag.UserID)
		return &tag, nil
	})
	f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "trunk").Return(&domain.Branch{ID: 3, Name: "trunk", CommitID: idPtr(5)}, nil)
	f.branches.EXPECT().SetDefaultBranch(gomock.Any(), importProject, snow.ID(3)).Return(nil)
	f.hooks.EXPECT().EmitPush(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ev PushEvent) error {
		require.Equal(t, "trunk", ev.Branch.Name)
		require.Nil(t, ev.Before)
		require.Equal(t, snow.ID(5), ev.AfterID)
		require.Equal(t, tip.Message, ev.Message)
		require.Empty(t, ev.Files)
		return nil
	})
	f.hooks.EXPECT().EmitTag(gomock.Any(), domain.WebhookEventTagCreated, importProject, gomock.Any(), importer).Return(errors.New("delivery failures only log"))

	results, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{
		Branches: []ImportRef{{Name: "trunk", Origin: gitOrigin(1)}},
		Tags:     []ImportRef{{Name: "v1", Origin: gitOrigin(2), Message: "release notes"}},
		Default:  "trunk",
		Commits:  12,
	})
	require.NoError(t, err)
	require.Equal(t, []ImportRefResult{
		{Kind: ImportRefBranch, Name: "trunk", Status: ImportRefCreated, CommitID: 5},
		{Kind: ImportRefTag, Name: "v1", Status: ImportRefCreated, CommitID: 6},
		{Kind: ImportRefDefault, Name: "trunk", Status: ImportRefUpdated, CommitID: 5},
	}, results)
	require.Equal(t, 1, f.tx.n)
	entry := f.audit.only(t)
	requireActor(t, entry, domain.AuditProjectGitImported)
	require.Equal(t, importProject, *entry.ProjectID)
	require.Equal(t, 12, entry.Metadata["commits"])
	require.Equal(t, []string{"branch:trunk=" + gitOrigin(1), "tag:v1=" + gitOrigin(2)}, entry.Metadata["heads"])
	require.Equal(t, map[string]int{"branch.created": 1, "tag.created": 1, "default.updated": 1}, entry.Metadata["results"])
}

func TestImportSetRefs_ExistingBranches(t *testing.T) {
	old, tip := storedCommit(4, gitOrigin(9)), storedCommit(5, gitOrigin(1))
	cases := []struct {
		name   string
		branch *domain.Branch
		update bool
		setup  func(f *importFixture)
		status string
		event  bool
	}{
		{name: "unchanged", branch: &domain.Branch{ID: 3, Name: "main", CommitID: idPtr(5), IsProtected: true}, status: ImportRefUnchanged},
		{name: "protected", branch: &domain.Branch{ID: 3, Name: "main", CommitID: idPtr(4), IsProtected: true}, update: true, status: ImportRefProtected},
		{name: "exists without update", branch: &domain.Branch{ID: 3, Name: "main", CommitID: idPtr(4)}, status: ImportRefExists},
		{
			name:   "empty branch is set without update",
			branch: &domain.Branch{ID: 3, Name: "main"},
			setup: func(f *importFixture) {
				f.branches.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(3), nil, idPtr(5)).Return(nil)
			},
			status: ImportRefUpdated,
			event:  true,
		},
		{
			name:   "fast-forward",
			branch: &domain.Branch{ID: 3, Name: "main", CommitID: idPtr(4)},
			update: true,
			setup: func(f *importFixture) {
				f.branches.EXPECT().GetCommit(gomock.Any(), snow.ID(5)).Return(&domain.Commit{ID: 5, Parent1ID: idPtr(4)}, nil)
				f.branches.EXPECT().GetCommit(gomock.Any(), snow.ID(4)).Return(old, nil)
				f.branches.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(3), idPtr(4), idPtr(5)).Return(nil)
			},
			status: ImportRefUpdated,
			event:  true,
		},
		{
			name:   "diverged",
			branch: &domain.Branch{ID: 3, Name: "main", CommitID: idPtr(4)},
			update: true,
			setup: func(f *importFixture) {
				f.branches.EXPECT().GetCommit(gomock.Any(), snow.ID(5)).Return(&domain.Commit{ID: 5}, nil)
			},
			status: ImportRefDiverged,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			f.admin()
			f.targets(tip)
			f.locked()
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(tc.branch, nil)
			if tc.setup != nil {
				tc.setup(f)
			}
			hadHead := tc.branch.CommitID != nil
			if tc.event {
				f.hooks.EXPECT().EmitPush(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ev PushEvent) error {
					require.Equal(t, snow.ID(5), *ev.Branch.CommitID)
					if hadHead {
						require.Equal(t, old, ev.Before)
					} else {
						require.Nil(t, ev.Before)
					}
					return nil
				})
			}
			results, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{
				Branches: []ImportRef{{Name: "main", Origin: gitOrigin(1)}},
				Update:   tc.update,
			})
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, tc.status, results[0].Status)
			require.Len(t, f.audit.entries, 1)
		})
	}
}

func TestImportSetRefs_ReportedRefs(t *testing.T) {
	for def, want := range map[string]string{"gone": "default:invalid", "main": "default:unchanged"} {
		t.Run(def, func(t *testing.T) {
			f := newImportFixture(t)
			f.admin()
			f.targets(storedCommit(5, gitOrigin(1)))
			f.locked()
			f.tags.EXPECT().GetTagByName(gomock.Any(), importProject, "same").Return(&domain.Tag{Name: "same", CommitID: 5}, nil)
			f.tags.EXPECT().GetTagByName(gomock.Any(), importProject, "moved").Return(&domain.Tag{Name: "moved", CommitID: 2}, nil)
			if def == "gone" {
				f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "gone").Return(nil, domain.NewErrorNotFound("x"))
			} else {
				f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(&domain.Branch{ID: 1, IsDefault: true}, nil)
			}
			results, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{
				Branches: []ImportRef{{Name: "feature/x", Origin: gitOrigin(1)}},
				Tags:     []ImportRef{{Name: "release/1", Origin: gitOrigin(1)}, {Name: "same", Origin: gitOrigin(1)}, {Name: "moved", Origin: gitOrigin(1)}},
				Default:  def,
			})
			require.NoError(t, err)
			statuses := make([]string, 0, len(results))
			for _, r := range results {
				statuses = append(statuses, r.Kind+":"+r.Status)
			}
			require.Equal(t, []string{"branch:invalid", "tag:invalid", "tag:unchanged", "tag:exists", want}, statuses)
		})
	}
}

func TestImportSetRefs_RepositoryErrors(t *testing.T) {
	ref := SetRefsInput{Branches: []ImportRef{{Name: "main", Origin: gitOrigin(1)}}}
	tagRef := SetRefsInput{Tags: []ImportRef{{Name: "v1", Origin: gitOrigin(1)}}}
	cases := []struct {
		name  string
		in    SetRefsInput
		setup func(f *importFixture)
	}{
		{name: "branch lookup", in: ref, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(nil, errors.New("boom"))
		}},
		{name: "list branches", in: ref, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(nil, domain.NewErrorNotFound("x"))
			f.branches.EXPECT().ListBranches(gomock.Any(), importProject, 1, nil, snow.ID(0)).Return(nil, errors.New("boom"))
		}},
		{name: "create branch", in: ref, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(nil, domain.NewErrorNotFound("x"))
			f.branches.EXPECT().ListBranches(gomock.Any(), importProject, 1, nil, snow.ID(0)).Return([]*domain.Branch{{}}, nil)
			f.branches.EXPECT().CreateBranch(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))
		}},
		{name: "ancestry walk", in: SetRefsInput{Branches: ref.Branches, Update: true}, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(&domain.Branch{ID: 3, CommitID: idPtr(4)}, nil)
			f.branches.EXPECT().GetCommit(gomock.Any(), snow.ID(5)).Return(nil, errors.New("boom"))
		}},
		{name: "before commit", in: SetRefsInput{Branches: ref.Branches, Update: true}, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(&domain.Branch{ID: 3, CommitID: idPtr(4)}, nil)
			f.branches.EXPECT().GetCommit(gomock.Any(), snow.ID(5)).Return(&domain.Commit{ID: 5, Parent1ID: idPtr(4)}, nil)
			f.branches.EXPECT().GetCommit(gomock.Any(), snow.ID(4)).Return(nil, errors.New("boom"))
		}},
		{name: "move branch", in: ref, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(&domain.Branch{ID: 3}, nil)
			f.branches.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(3), nil, idPtr(5)).Return(errors.New("boom"))
		}},
		{name: "tag lookup", in: tagRef, setup: func(f *importFixture) {
			f.tags.EXPECT().GetTagByName(gomock.Any(), importProject, "v1").Return(nil, errors.New("boom"))
		}},
		{name: "create tag", in: tagRef, setup: func(f *importFixture) {
			f.tags.EXPECT().GetTagByName(gomock.Any(), importProject, "v1").Return(nil, domain.NewErrorNotFound("x"))
			f.tags.EXPECT().CreateTag(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))
		}},
		{name: "default lookup", in: SetRefsInput{Default: "main"}, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(nil, errors.New("boom"))
		}},
		{name: "set default", in: SetRefsInput{Default: "main"}, setup: func(f *importFixture) {
			f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(&domain.Branch{ID: 3}, nil)
			f.branches.EXPECT().SetDefaultBranch(gomock.Any(), importProject, snow.ID(3)).Return(errors.New("boom"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			f.admin()
			if len(tc.in.Branches)+len(tc.in.Tags) > 0 {
				f.targets(storedCommit(5, gitOrigin(1)))
			}
			f.locked()
			tc.setup(f)
			_, err := f.uc.SetRefs(importCtx(), importProject, tc.in)
			require.EqualError(t, err, "boom")
			require.Empty(t, f.audit.entries)
		})
	}
}

func TestImportSetRefs_WithoutHooksOrAudit(t *testing.T) {
	f := newImportFixture(t)
	f.uc.hooks, f.uc.audit = nil, nil
	f.admin()
	f.targets(storedCommit(5, gitOrigin(1)))
	f.locked()
	f.branches.EXPECT().GetBranchByName(gomock.Any(), importProject, "main").Return(&domain.Branch{ID: 3}, nil)
	f.branches.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(3), nil, idPtr(5)).Return(nil)
	results, err := f.uc.SetRefs(importCtx(), importProject, SetRefsInput{Branches: []ImportRef{{Name: "main", Origin: gitOrigin(1)}}})
	require.NoError(t, err)
	require.Equal(t, ImportRefUpdated, results[0].Status)
	require.Equal(t, 1, f.tx.n)
}

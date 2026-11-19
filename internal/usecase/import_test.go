package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

const (
	importProject = snow.ID(42)
	importOrg     = snow.ID(4)
	importer      = snow.ID(7)
)

type importFixture struct {
	uc       *Import
	perm     *MockpermissionUsecase
	branches *MockbranchRepository
	push     *MockpushRepository
	tags     *MocktagRepository
	users    *MockimportUsers
	orgs     *MockimportOrgMembers
	projects *MockimportProjectOrgs
	locker   *MockimportLocker
	hooks    *MockimportHooks
	storage  *MockimportStorageGate
	audit    *captureRecorder
	tx       *countingTx
}

type countingTx struct{ n int }

func (c *countingTx) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	c.n++
	return fn(ctx)
}

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	f := &importFixture{
		perm:     NewMockpermissionUsecase(ctrl),
		branches: NewMockbranchRepository(ctrl),
		push:     NewMockpushRepository(ctrl),
		tags:     NewMocktagRepository(ctrl),
		users:    NewMockimportUsers(ctrl),
		orgs:     NewMockimportOrgMembers(ctrl),
		projects: NewMockimportProjectOrgs(ctrl),
		locker:   NewMockimportLocker(ctrl),
		hooks:    NewMockimportHooks(ctrl),
		storage:  NewMockimportStorageGate(ctrl),
		audit:    &captureRecorder{},
		tx:       &countingTx{},
	}
	f.uc = NewImport(f.perm, f.branches, f.push, f.tags, f.users, f.orgs, f.projects, f.locker, f.tx, node).
		WithHooks(f.hooks).WithAudit(f.audit).WithStorageGate(f.storage)
	return f
}

func (f *importFixture) admin() {
	f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(true).AnyTimes()
	f.storage.EXPECT().RequireWritable(gomock.Any(), importProject).Return(nil).AnyTimes()
	f.perm.EXPECT().HasPathAccess(gomock.Any(), importProject, gomock.Any(), domain.PermissionWrite).Return(true).AnyTimes()
}

func importCtx() context.Context { return permissionCtx(importer) }

func gitOrigin(n byte) string {
	return "git:default:" + strings.Repeat(string("0123456789abcdef"[n%16]), 40)
}

func importFile(t *testing.T, path, content string) *domain.PushFile {
	t.Helper()
	encoding, chunks, err := chunker.EncodeBytes([]byte(content), path, false, "")
	require.NoError(t, err)
	hashes := make([]domain.Hash, 0, len(chunks))
	for _, c := range chunks {
		hashes = append(hashes, c.Hash)
	}
	return &domain.PushFile{
		Path:        path,
		Mode:        0o644,
		SizeBytes:   int64(len(content)),
		Encoding:    encoding,
		FileHash:    treehash.FileHash(hashes),
		ChunkHashes: hashes,
	}
}

func storedCommit(id snow.ID, origin string) *domain.Commit {
	return &domain.Commit{ID: id, ProjectID: importProject, Origin: origin, Hash: domain.Hash{byte(id)}, TreeID: int64(id) * 10, Message: "msg " + origin}
}

func echoApply(f *importFixture, capture *ApplyPushRequest) {
	f.push.EXPECT().ApplyImport(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req ApplyPushRequest) (*domain.Commit, error) {
		*capture = req
		return &domain.Commit{ID: req.CommitID, Hash: req.CommitHash, ProjectID: req.ProjectID, Origin: req.Origin}, nil
	})
}

func TestImportLookup(t *testing.T) {
	t.Run("writer is not enough", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(false)
		_, err := f.uc.Lookup(importCtx(), importProject, nil)
		require.True(t, domain.IsErrorNoPermission(err))
	})
	t.Run("read-only store", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(true)
		f.storage.EXPECT().RequireWritable(gomock.Any(), importProject).Return(domain.NewErrorQuotaExceeded("read only"))
		_, err := f.uc.Lookup(importCtx(), importProject, []string{gitOrigin(1)})
		require.ErrorContains(t, err, "read only")
	})
	t.Run("caps the batch", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		_, err := f.uc.Lookup(importCtx(), importProject, make([]string, MaxImportLookup+1))
		require.ErrorContains(t, err, "more than the limit")
	})
	t.Run("empty", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		got, err := f.uc.Lookup(importCtx(), importProject, nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("project scoped", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		other := storedCommit(3, gitOrigin(3))
		other.ProjectID = 99
		f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, []string{gitOrigin(1), gitOrigin(2), gitOrigin(3)}).
			Return([]*domain.Commit{storedCommit(1, gitOrigin(1)), other}, nil)
		got, err := f.uc.Lookup(importCtx(), importProject, []string{gitOrigin(1), gitOrigin(2), gitOrigin(3)})
		require.NoError(t, err)
		require.Equal(t, map[string]ImportedCommit{gitOrigin(1): {Origin: gitOrigin(1), CommitID: 1, CommitHash: domain.Hash{1}}}, got)
	})
	t.Run("repository error", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, errors.New("db"))
		_, err := f.uc.Lookup(importCtx(), importProject, []string{gitOrigin(1)})
		require.Error(t, err)
	})
}

func TestImportEstimateUpload(t *testing.T) {
	org := &domain.Organization{ID: 7}
	project := &domain.Project{ID: importProject}
	t.Run("writer is not enough", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(false)
		_, err := f.uc.EstimateUpload(importCtx(), org, project, 10)
		require.True(t, domain.IsErrorNoPermission(err))
	})
	t.Run("no quota seam", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		checked, err := f.uc.EstimateUpload(importCtx(), org, project, 10)
		require.NoError(t, err)
		require.False(t, checked)
	})
	t.Run("nothing to upload", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		quota := NewMockimportQuota(gomock.NewController(t))
		f.uc.WithQuota(quota)
		checked, err := f.uc.EstimateUpload(importCtx(), org, project, 0)
		require.NoError(t, err)
		require.False(t, checked)
	})
	t.Run("within quota", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		quota := NewMockimportQuota(gomock.NewController(t))
		quota.EXPECT().CheckUploadHeadroom(gomock.Any(), org, project, int64(10)).Return(true, nil)
		checked, err := f.uc.WithQuota(quota).EstimateUpload(importCtx(), org, project, 10)
		require.NoError(t, err)
		require.True(t, checked)
	})
	t.Run("over quota", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		quota := NewMockimportQuota(gomock.NewController(t))
		quota.EXPECT().CheckUploadHeadroom(gomock.Any(), org, project, int64(10)).Return(true, domain.NewErrorQuotaExceeded("storage quota exceeded"))
		checked, err := f.uc.WithQuota(quota).EstimateUpload(importCtx(), org, project, 10)
		require.True(t, checked)
		require.ErrorContains(t, err, "storage quota exceeded")
	})
}

func TestImportCommit_Refusals(t *testing.T) {
	t.Run("no claim", func(t *testing.T) {
		f := newImportFixture(t)
		_, err := f.uc.Commit(context.Background(), importProject, ImportCommit{Origin: gitOrigin(1)})
		require.True(t, domain.IsErrorNoPermission(err))
	})
	t.Run("writer is not enough", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(false)
		_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{Origin: gitOrigin(1)})
		require.True(t, domain.IsErrorNoPermission(err))
	})
	t.Run("read-only store", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(true)
		f.storage.EXPECT().RequireWritable(gomock.Any(), importProject).Return(domain.NewErrorQuotaExceeded("read only"))
		_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{Origin: gitOrigin(1)})
		require.ErrorContains(t, err, "read only")
	})
	cases := map[string]ImportCommit{
		"invalid origin":  {Origin: "git:default:nope"},
		"octopus":         {Origin: gitOrigin(1), ParentOrigins: []string{gitOrigin(2), gitOrigin(3), gitOrigin(4)}},
		"same parent":     {Origin: gitOrigin(1), ParentOrigins: []string{gitOrigin(2), gitOrigin(2)}},
		"self as parent":  {Origin: gitOrigin(1), ParentOrigins: []string{gitOrigin(1)}},
		"unknown parent":  {Origin: gitOrigin(1), ParentOrigins: []string{gitOrigin(2)}},
		"hash mismatch":   {Origin: gitOrigin(1), Files: []*domain.PushFile{{Path: "a", SizeBytes: 1, ChunkHashes: []domain.Hash{{1}}}}},
		"bad path":        {Origin: gitOrigin(1), Files: []*domain.PushFile{{Path: "../a"}}},
		"too many files":  {Origin: gitOrigin(1), Removed: []string{"a", "b", "c"}},
		"removed and set": {Origin: gitOrigin(1), Removed: []string{"a"}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			f := newImportFixture(t)
			f.admin()
			f.uc.WithMaxFiles(2)
			if name == "removed and set" {
				in.Files = []*domain.PushFile{importFile(t, "a", "x")}
			}
			f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, nil).AnyTimes()
			_, err := f.uc.Commit(importCtx(), importProject, in)
			var de *domain.Error
			require.ErrorAs(t, err, &de)
			require.Equal(t, 400, de.Code, err.Error())
		})
	}
	t.Run("lookup error", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, errors.New("db"))
		_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{Origin: gitOrigin(1)})
		require.EqualError(t, err, "db")
	})
	t.Run("path write denied", func(t *testing.T) {
		f := newImportFixture(t)
		f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(true)
		f.storage.EXPECT().RequireWritable(gomock.Any(), importProject).Return(nil)
		f.perm.EXPECT().HasPathAccess(gomock.Any(), importProject, "a", domain.PermissionWrite).Return(false)
		f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, nil)
		_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{Origin: gitOrigin(1), Files: []*domain.PushFile{importFile(t, "a", "x")}})
		require.True(t, domain.IsErrorNoPermission(err))
	})
}

func TestImportCommit_Idempotent(t *testing.T) {
	f := newImportFixture(t)
	f.admin()
	f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, []string{gitOrigin(1), gitOrigin(2)}).
		Return([]*domain.Commit{storedCommit(9, gitOrigin(1)), storedCommit(8, gitOrigin(2))}, nil)
	got, err := f.uc.Commit(importCtx(), importProject, ImportCommit{Origin: gitOrigin(1), ParentOrigins: []string{gitOrigin(2)}})
	require.NoError(t, err)
	require.Equal(t, &ImportedCommit{Origin: gitOrigin(1), CommitID: 9, CommitHash: domain.Hash{9}}, got)
}

func TestImportCommit_Root(t *testing.T) {
	f := newImportFixture(t)
	f.admin()
	f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, []string{gitOrigin(1)}).Return(nil, nil)
	var req ApplyPushRequest
	echoApply(f, &req)
	authored := time.Date(2020, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	file := importFile(t, "dir/a.png", "png bytes")
	got, err := f.uc.Commit(importCtx(), importProject, ImportCommit{
		Origin:     gitOrigin(1),
		Message:    "first",
		AuthorName: strings.Repeat("é", 300),
		AuthoredAt: authored,
		Files:      []*domain.PushFile{file},
	})
	require.NoError(t, err)
	require.Equal(t, req.CommitID, got.CommitID)
	require.Zero(t, req.BranchID, "an import never moves a branch")
	require.Nil(t, req.ParentID)
	require.Equal(t, importer, req.UserID)
	require.Equal(t, gitOrigin(1), req.Origin)
	require.Equal(t, 255, len([]rune(req.AuthorName)))
	require.Equal(t, authored.UTC(), *req.CreatedAt)
	require.True(t, file.IsBinary, "lockable extensions are binary")
	root := req.Nodes[0]
	require.Equal(t, treehash.CommitHashWithOrigin(root.Hash, nil, "first", gitOrigin(1)), req.CommitHash)
	require.NotEqual(t, treehash.CommitHash(root.Hash, nil, "first"), req.CommitHash)
}

func TestImportCommit_MergeOnParentTree(t *testing.T) {
	f := newImportFixture(t)
	f.admin()
	p1, p2 := storedCommit(5, gitOrigin(2)), storedCommit(6, gitOrigin(3))
	f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, []string{gitOrigin(1), gitOrigin(2), gitOrigin(3)}).
		Return([]*domain.Commit{p2, p1}, nil)
	f.branches.EXPECT().GetTreeNode(gomock.Any(), int64(50)).Return(&domain.TreeNode{ID: 50, Name: "root"}, nil)
	f.branches.EXPECT().ListFilesByTree(gomock.Any(), int64(50)).Return([]*domain.File{{Name: "keep.txt", Mode: 0o644, Hash: domain.Hash{7}}}, nil)
	f.branches.EXPECT().ListTreeChildren(gomock.Any(), int64(50)).Return(nil, nil)
	var req ApplyPushRequest
	echoApply(f, &req)
	_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{
		Origin:        gitOrigin(1),
		ParentOrigins: []string{gitOrigin(2), gitOrigin(3)},
		Message:       "merge",
		Files:         []*domain.PushFile{importFile(t, "b.txt", "b")},
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(5), *req.ParentID)
	require.Equal(t, snow.ID(6), *req.ParentID2)
	require.Nil(t, req.CreatedAt)
	require.Len(t, req.Files, 2, "the tree starts from parent 1")
	require.Equal(t, treehash.CommitHashWithOrigin(req.Nodes[0].Hash, []domain.Hash{p1.Hash, p2.Hash}, "merge", gitOrigin(1)), req.CommitHash)
}

func TestImportCommit_ApplyError(t *testing.T) {
	f := newImportFixture(t)
	f.admin()
	f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, nil)
	f.push.EXPECT().ApplyImport(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorUser("missing chunk data"))
	_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{Origin: gitOrigin(1)})
	require.ErrorContains(t, err, "missing chunk data")
}

func TestImportCommit_Authors(t *testing.T) {
	member := &domain.User{ID: 11, EmailVerified: true}
	cases := []struct {
		name   string
		in     ImportCommit
		setup  func(f *importFixture)
		want   snow.ID
		errMsg string
	}{
		{name: "no email", in: ImportCommit{AuthorEmail: "not-an-email"}, want: importer},
		{
			name: "mapped member",
			in:   ImportCommit{AuthorUserID: 11},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(member, nil)
				f.orgs.EXPECT().MemberRole(gomock.Any(), importOrg, snow.ID(11)).Return(domain.OrgRoleMember, nil)
			},
			want: 11,
		},
		{
			name: "mapped non member",
			in:   ImportCommit{AuthorUserID: 11},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(member, nil)
				f.orgs.EXPECT().MemberRole(gomock.Any(), importOrg, snow.ID(11)).Return("", domain.NewErrorNotFound("x"))
			},
			errMsg: "not a member",
		},
		{
			name: "mapped deleted user",
			in:   ImportCommit{AuthorUserID: 11},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(&domain.User{ID: 11, Deleted: true}, nil)
			},
			errMsg: "not a member",
		},
		{
			name: "mapped unknown user",
			in:   ImportCommit{AuthorUserID: 11},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(nil, domain.NewErrorNotFound("x"))
			},
			errMsg: "not a member",
		},
		{
			name: "mapped lookup error",
			in:   ImportCommit{AuthorUserID: 11},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(nil, errors.New("db"))
			},
			errMsg: "db",
		},
		{
			name: "verified member email",
			in:   ImportCommit{AuthorEmail: " Ann@Example.com "},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByEmail(gomock.Any(), "ann@example.com").Return(member, nil)
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(member, nil)
				f.orgs.EXPECT().MemberRole(gomock.Any(), importOrg, snow.ID(11)).Return(domain.OrgRoleOwner, nil)
			},
			want: 11,
		},
		{
			name: "unverified email",
			in:   ImportCommit{AuthorEmail: "ann@example.com"},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByEmail(gomock.Any(), "ann@example.com").Return(&domain.User{ID: 11}, nil)
			},
			want: importer,
		},
		{
			name: "email of a non member",
			in:   ImportCommit{AuthorEmail: "ann@example.com"},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByEmail(gomock.Any(), "ann@example.com").Return(member, nil)
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(member, nil)
				f.orgs.EXPECT().MemberRole(gomock.Any(), importOrg, snow.ID(11)).Return("", domain.NewErrorNotFound("x"))
			},
			want: importer,
		},
		{
			name: "member role error",
			in:   ImportCommit{AuthorEmail: "ann@example.com"},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByEmail(gomock.Any(), "ann@example.com").Return(member, nil)
				f.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(member, nil)
				f.orgs.EXPECT().MemberRole(gomock.Any(), importOrg, snow.ID(11)).Return("", errors.New("db"))
			},
			errMsg: "db",
		},
		{
			name: "unknown email",
			in:   ImportCommit{AuthorEmail: "ann@example.com"},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByEmail(gomock.Any(), "ann@example.com").Return(nil, domain.NewErrorRecordNotFound())
			},
			want: importer,
		},
		{
			name: "email lookup error",
			in:   ImportCommit{AuthorEmail: "ann@example.com"},
			setup: func(f *importFixture) {
				f.users.EXPECT().GetByEmail(gomock.Any(), "ann@example.com").Return(nil, errors.New("db"))
			},
			errMsg: "db",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			f.admin()
			f.projects.EXPECT().ProjectOrgID(gomock.Any(), importProject).Return(importOrg, nil).AnyTimes()
			f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, nil)
			if tc.setup != nil {
				tc.setup(f)
			}
			var req ApplyPushRequest
			if tc.errMsg == "" {
				echoApply(f, &req)
			}
			tc.in.Origin = gitOrigin(1)
			_, err := f.uc.Commit(importCtx(), importProject, tc.in)
			if tc.errMsg != "" {
				require.ErrorContains(t, err, tc.errMsg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, req.UserID)
		})
	}
	t.Run("org lookup error", func(t *testing.T) {
		f := newImportFixture(t)
		f.admin()
		f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, gomock.Any()).Return(nil, nil)
		f.projects.EXPECT().ProjectOrgID(gomock.Any(), importProject).Return(snow.ID(0), errors.New("db"))
		_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{Origin: gitOrigin(1), AuthorEmail: "a@b.c"})
		require.EqualError(t, err, "db")
	})
}

func TestImportMaxFiles(t *testing.T) {
	f := newImportFixture(t)
	require.Equal(t, DefaultPushMaxFiles, f.uc.MaxFiles())
	f.uc.WithMaxFiles(3)
	require.Equal(t, 3, f.uc.MaxFiles())
	require.ErrorContains(t, f.uc.TooManyFiles(4), "limit of 3")
}

func TestImportCheckAccessWithoutStorageGate(t *testing.T) {
	f := newImportFixture(t)
	f.uc.storage = nil
	f.perm.EXPECT().AdminHasProject(gomock.Any(), importProject).Return(true)
	require.NoError(t, f.uc.CheckAccess(importCtx(), importProject))
}

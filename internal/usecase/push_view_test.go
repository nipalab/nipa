package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// pushViewFixture is a push onto an empty branch by a caller whose read
// filter the test sets, with a Branch usecase over the same permissions to
// compare the token with what the manifest path hands out.
type pushViewFixture struct {
	push   *Push
	branch *Branch
	ctx    context.Context
	filter *PathFilter
	err    error
}

func newPushViewFixture(t *testing.T) *pushViewFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	pushRepo := NewMockpushRepository(ctrl)
	f := &pushViewFixture{filter: AllowAllFilter()}
	perm.EXPECT().HasProjectAccess(gomock.Any(), gomock.Any(), gomock.Any()).Return(true).AnyTimes()
	perm.EXPECT().HasPathAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true).AnyTimes()
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).DoAndReturn(
		func(context.Context, snow.ID, domain.Permission) (*PathFilter, error) {
			return f.filter, f.err
		}).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main"}, nil).AnyTimes()
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	f.push = NewPush(perm, repo, pushRepo, node)
	f.branch = NewBranch(perm, repo, node)
	f.ctx = domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
	return f
}

func (f *pushViewFixture) restrict(rules ...*domain.PBACRule) {
	f.filter = &PathFilter{set: &permissionSet{rules: rules}, permission: domain.PermissionRead}
}

func (f *pushViewFixture) run(t *testing.T, paths ...string) (*domain.PushResult, error) {
	t.Helper()
	files := make([]*domain.PushFile, 0, len(paths))
	for _, path := range paths {
		chunk, file := chunkAndFileHash(t, path)
		files = append(files, &domain.PushFile{Path: path, Mode: 0o644, SizeBytes: int64(len(path)), FileHash: file, ChunkHashes: []domain.Hash{chunk}})
	}
	return f.push.Push(f.ctx, snow.ID(1), "main", "", "msg", files, nil, "", "")
}

// manifestView is the token GetTreeManifest returns to the same caller for a
// full checkout: no paths, recursive.
func (f *pushViewFixture) manifestView(t *testing.T) []byte {
	t.Helper()
	view, err := f.branch.ManifestView(f.ctx, snow.ID(1), nil, true)
	require.NoError(t, err)
	require.Len(t, view, 32)
	return view
}

func TestPush_ViewIsTheFullManifestView(t *testing.T) {
	f := newPushViewFixture(t)

	result, err := f.run(t, "a/x.txt", "b/y.txt")
	require.NoError(t, err)
	require.Equal(t, f.manifestView(t), result.View.Token, "admin: the manifest's token")
	require.True(t, result.View.Unrestricted)
	require.False(t, result.View.HidesPushed)
	admin := result.View.Token

	// A root rule reads everything too, but is a different view.
	f.restrict(&domain.PBACRule{PathPrefix: "", Permission: domain.PermissionRead | domain.PermissionWrite})
	result, err = f.run(t, "a/x.txt")
	require.NoError(t, err)
	require.Equal(t, f.manifestView(t), result.View.Token)
	require.NotEqual(t, admin, result.View.Token)
	require.True(t, result.View.Unrestricted)
	require.False(t, result.View.HidesPushed)

	// Path restricted: every pushed path readable.
	f.restrict(&domain.PBACRule{PathPrefix: "a", Permission: domain.PermissionRead | domain.PermissionWrite})
	result, err = f.run(t, "a/x.txt", "a/deep/y.txt")
	require.NoError(t, err)
	restricted := result.View.Token
	require.Equal(t, f.manifestView(t), restricted)
	require.False(t, result.View.Unrestricted)
	require.False(t, result.View.HidesPushed)

	// A rule change changes the token, exactly as it changes the manifest's.
	f.restrict(
		&domain.PBACRule{PathPrefix: "a", Permission: domain.PermissionRead | domain.PermissionWrite},
		&domain.PBACRule{PathPrefix: "c", Permission: domain.PermissionRead},
	)
	result, err = f.run(t, "a/x.txt")
	require.NoError(t, err)
	require.Equal(t, f.manifestView(t), result.View.Token)
	require.NotEqual(t, restricted, result.View.Token)

	// A rule that grants no read leaves the view as it was.
	f.restrict(
		&domain.PBACRule{PathPrefix: "a", Permission: domain.PermissionRead | domain.PermissionWrite},
		&domain.PBACRule{PathPrefix: "drop", Permission: domain.PermissionWrite},
	)
	result, err = f.run(t, "a/x.txt")
	require.NoError(t, err)
	require.Equal(t, restricted, result.View.Token)
	require.False(t, result.View.HidesPushed)

	// Writing a path the pusher cannot read: the pushed tree is not the view.
	result, err = f.run(t, "a/x.txt", "drop/secret.txt")
	require.NoError(t, err)
	require.Equal(t, restricted, result.View.Token)
	require.False(t, result.View.Unrestricted)
	require.True(t, result.View.HidesPushed)
}

func TestPush_ViewFilterErrorFailsThePush(t *testing.T) {
	f := newPushViewFixture(t)
	f.err = errors.New("rules unavailable")
	_, err := f.run(t, "a/x.txt")
	require.ErrorIs(t, err, f.err)
}

// BenchmarkPush_View is the work pushView adds to a push once the write check
// has loaded the caller's rules: the fingerprint, one SHA-256 and, for a
// path-restricted reader, one Allow per pushed file.
func BenchmarkPush_View(b *testing.B) {
	rules := make([]*domain.PBACRule, 0, 50)
	for i := 0; i < 50; i++ {
		rules = append(rules, &domain.PBACRule{PathPrefix: fmt.Sprintf("team%02d/assets", i), Permission: domain.PermissionRead | domain.PermissionWrite})
	}
	files := func(n int) []*domain.PushFile {
		out := make([]*domain.PushFile, n)
		for i := range out {
			out[i] = &domain.PushFile{Path: fmt.Sprintf("team%02d/assets/file%05d.png", i%50, i)}
		}
		return out
	}
	for _, bc := range []struct {
		name   string
		filter *PathFilter
		files  int
	}{
		{"admin", AllowAllFilter(), 5},
		{"restricted 50 rules, 5 files", &PathFilter{set: &permissionSet{rules: rules}, permission: domain.PermissionRead}, 5},
		{"restricted 50 rules, 10000 files", &PathFilter{set: &permissionSet{rules: rules}, permission: domain.PermissionRead}, 10000},
	} {
		b.Run(bc.name, func(b *testing.B) {
			ctrl := gomock.NewController(b)
			perm := NewMockpermissionUsecase(ctrl)
			perm.EXPECT().CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).Return(bc.filter, nil).AnyTimes()
			p := &Push{permUc: perm}
			pushed := files(bc.files)
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := p.pushView(ctx, 1, pushed); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

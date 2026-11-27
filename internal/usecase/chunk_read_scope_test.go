package usecase

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// A pack holds chunks of every path of the push that wrote it, and its
// presigned URL lets the holder read all of it. A path-restricted reader is
// therefore handed the server's signed path for each of its chunks and
// nothing that names the pack.
func TestChunk_PresignDownload_PathRestrictedGetsNoPackURL(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newPackFixture(t)
	store.downloadURL = "https://s3.example.com/loose"
	visible, hidden := []byte("bytes of a visible path"), []byte("bytes of a hidden path")
	visibleHash, hiddenHash, looseHash := chunker.Sum(visible), chunker.Sum(hidden), domain.Hash{4}
	const packID = snow.ID(7)
	store.packs[storage.PackKey(testProject.ID, packID)] = append(append([]byte("NPK1"), visible...), hidden...)
	repo.states[packID] = ChunkPackStored
	repo.recordAt(testProject.ID, visibleHash, int64(len(visible)), ChunkLocation{PackID: packID, Offset: 4})
	repo.recordAt(testProject.ID, hiddenHash, int64(len(hidden)), ChunkLocation{PackID: packID, Offset: 4 + int64(len(visible))})
	repo.recordAt(testProject.ID, looseHash, 40, ChunkLocation{})

	var zero ChunkReadScope
	require.Equal(t, ChunkReadPaths, zero, "a caller that does not say is treated as path-restricted")

	urls, next, err := uc.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{visibleHash, looseHash}, 0, "", ChunkReadPaths)
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, urls, 2)
	require.Zero(t, store.presigns, "no pack URL is signed for a path-restricted reader")
	require.Equal(t, ChunkURL{Hash: looseHash, URL: "https://s3.example.com/loose"}, urls[1], "a loose object holds one chunk: its own URL is fine")

	packed := urls[0]
	require.Equal(t, visibleHash, packed.Hash)
	require.Zero(t, packed.Offset, "the answer shows no place in the pack")
	require.Zero(t, packed.Length)
	require.NotContains(t, packed.URL, storage.PackPrefix(testProject.ID))
	parsed, err := url.Parse(packed.URL)
	require.NoError(t, err)
	require.False(t, parsed.IsAbs(), "a path on the server itself")
	require.Equal(t, "/api/chunks/"+testOrg.Slug+"/"+testProject.Slug+"/"+visibleHash.String(), parsed.Path)
	params, err := chunkurl.ParseParams(parsed.Query())
	require.NoError(t, err)
	require.NoError(t, uc.VerifyTransferURL(testOrg.Slug, testProject.Slug, visibleHash.String(), params))
	err = uc.VerifyTransferURL(testOrg.Slug, testProject.Slug, hiddenHash.String(), params)
	require.True(t, domain.IsErrorNoPermission(err), "the signature names one hash: it does not open the hidden chunk")

	// The route behind that path reads exactly the chunk's range, from the
	// location presign already looked up.
	repo.sizesErr = errors.New("the location is known from presign")
	got, err := uc.Download(ctx, testProject.ID, visibleHash)
	require.NoError(t, err)
	require.Equal(t, visible, got)
	require.Equal(t, 1, store.ranges)
	repo.sizesErr = nil

	// The same chunk for a reader of the whole project is a range of the
	// pack object.
	urls, _, err = uc.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{visibleHash}, 0, "", ChunkReadProject)
	require.NoError(t, err)
	require.Equal(t, []ChunkURL{{
		Hash:   visibleHash,
		URL:    "https://s3.example.com/" + storage.PackKey(testProject.ID, packID),
		Offset: 4,
		Length: int64(len(visible)),
	}}, urls)
	require.Equal(t, 1, store.presigns)
}

// Without the location cache of PackedReads the restricted answer is the
// same; only the lookup at download time is not spared.
func TestChunk_PresignDownload_PathRestrictedWithoutPackedReads(t *testing.T) {
	store := newFakePackStore()
	uc, repo := newLinkFixture(t, store)
	hash := domain.Hash{1}
	repo.recordAt(testProject.ID, hash, 10, ChunkLocation{PackID: 7, Offset: 4})
	urls, _, err := uc.PresignDownloadURLs(context.Background(), testOrg, testProject, []domain.Hash{hash}, 0, "", ChunkReadPaths)
	require.NoError(t, err)
	require.Len(t, urls, 1)
	require.True(t, strings.HasPrefix(urls[0].URL, "/api/chunks/"), urls[0].URL)
	require.Zero(t, urls[0].Length)
	require.Zero(t, store.presigns)
}

func TestPathFilter_Unrestricted(t *testing.T) {
	read, write := domain.PermissionRead, domain.PermissionWrite
	rule := func(prefix string, permission domain.Permission) *domain.PBACRule {
		return &domain.PBACRule{PathPrefix: prefix, Permission: permission}
	}
	def := func(prefix string, permission domain.Permission) *domain.ProjectPathPermission {
		return &domain.ProjectPathPermission{PathPrefix: prefix, Permission: permission}
	}
	var none *PathFilter
	tests := []struct {
		name   string
		filter *PathFilter
		want   bool
	}{
		{name: "nil filter", filter: none},
		{name: "allow all", filter: AllowAllFilter(), want: true},
		{name: "org owner", filter: &PathFilter{set: &permissionSet{orgOwner: true}, permission: read}, want: true},
		{name: "no rule set", filter: &PathFilter{permission: read}},
		{name: "no permission asked", filter: &PathFilter{set: &permissionSet{rules: []*domain.PBACRule{rule("", read)}}}},
		{name: "no rules", filter: &PathFilter{set: &permissionSet{}, permission: read}},
		{name: "rule on the root", filter: &PathFilter{set: &permissionSet{rules: []*domain.PBACRule{rule("", read|write)}}, permission: read}, want: true},
		{name: "root rule without the bit", filter: &PathFilter{set: &permissionSet{rules: []*domain.PBACRule{rule("", write)}}, permission: read}},
		{name: "rule on a folder", filter: &PathFilter{set: &permissionSet{rules: []*domain.PBACRule{rule("pub", read)}}, permission: read}},
		{
			name: "root rule wins over a denying default",
			filter: &PathFilter{set: &permissionSet{
				rules:    []*domain.PBACRule{rule("", read)},
				defaults: []*domain.ProjectPathPermission{def("secret", 0)},
			}, permission: read},
			want: true,
		},
		{name: "root default", filter: &PathFilter{set: &permissionSet{defaults: []*domain.ProjectPathPermission{def("", read)}}, permission: read}, want: true},
		{name: "folder default only", filter: &PathFilter{set: &permissionSet{defaults: []*domain.ProjectPathPermission{def("pub", read)}}, permission: read}},
		{
			name: "root default shadowed by a denying folder default",
			filter: &PathFilter{set: &permissionSet{
				defaults: []*domain.ProjectPathPermission{def("", read), def("secret", 0)},
			}, permission: read},
		},
		{
			name: "root default with a granting folder default",
			filter: &PathFilter{set: &permissionSet{
				defaults: []*domain.ProjectPathPermission{def("pub", read|write), def("", read)},
			}, permission: read},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.filter.Unrestricted())
			if tt.want && tt.filter.set != nil {
				// Unrestricted must never say more than Allow does.
				for _, path := range []string{"a.txt", "pub/a.txt", "secret/a.txt", "secret/deep/a.txt"} {
					require.True(t, tt.filter.Allow(path), path)
				}
			}
		})
	}
}

func TestBranch_ChunkReadScope(t *testing.T) {
	boom := errors.New("rules unavailable")
	tests := []struct {
		name   string
		filter *PathFilter
		err    error
		want   ChunkReadScope
	}{
		{name: "whole project", filter: AllowAllFilter(), want: ChunkReadProject},
		{
			name:   "some paths",
			filter: &PathFilter{set: &permissionSet{rules: []*domain.PBACRule{{PathPrefix: "pub", Permission: domain.PermissionRead}}}, permission: domain.PermissionRead},
			want:   ChunkReadPaths,
		},
		{name: "rules unavailable", err: boom, want: ChunkReadPaths},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perm := NewMockpermissionUsecase(gomock.NewController(t))
			perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(tt.filter, tt.err)
			uc := NewBranch(perm, newMemTreeRepo(), newTestBranchNode(t))
			scope, err := uc.ChunkReadScope(context.Background(), 1)
			require.ErrorIs(t, err, tt.err)
			require.Equal(t, tt.want, scope)
		})
	}
}

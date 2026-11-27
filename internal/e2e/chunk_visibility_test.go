package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// chunkVisibilityHarness is a project with a public and a secret folder and a
// reader who starts with read on the public folder only. Every access change
// goes through the real management RPCs, as an admin would make it.
type chunkVisibilityHarness struct {
	*deltaHarness
	commit string
	userID string
	pub    []serverDomain.Hash
	secret []serverDomain.Hash
}

func newChunkVisibilityHarness(t *testing.T) *chunkVisibilityHarness {
	t.Helper()
	h := newDeltaHarness(t)
	model := deltaModel{
		"pub/readme.md":  {content: "public content\n", mode: 0o644},
		"secret/key.bin": {content: "top secret content\n", mode: 0o644},
	}
	commit := deltaPush(t, h.ctx, h.admin, deltaModel{}, model, "")
	hashesOf := func(path string) []serverDomain.Hash {
		_, encoded, err := chunker.EncodeBytes([]byte(model[path].content), path, false, "")
		require.NoError(t, err)
		out := make([]serverDomain.Hash, 0, len(encoded))
		for _, e := range encoded {
			out = append(out, e.Hash)
		}
		return out
	}
	v := &chunkVisibilityHarness{
		deltaHarness: h,
		commit:       commit,
		userID:       snow.ID(deltaUserID).Base36(),
		pub:          hashesOf("pub/readme.md"),
		secret:       hashesOf("secret/key.bin"),
	}
	require.NotEqual(t, v.pub, v.secret)
	// Drop the bootstrap rule on the whole project, then grant the public
	// folder like an admin would.
	h.setFilter(deltaFilter{name: "none"})
	v.grant("pub")
	return v
}

func (v *chunkVisibilityHarness) grant(prefix string) int64 {
	v.t.Helper()
	rule, err := v.admin.CreatePBACRule(v.ctx, e2eOrgSlug, e2eProjectSlug, v.userID, "", prefix, uint64(serverDomain.PermissionRead))
	require.NoError(v.t, err)
	return rule.ID
}

// download asks for the chunks as the reader, for the pushed commit.
func (v *chunkVisibilityHarness) download(hashes []serverDomain.Hash) error {
	scope := domain.ChunkScope{Org: e2eOrgSlug, Project: e2eProjectSlug, CommitIDs: []string{v.commit}}
	got := 0
	err := v.user.DownloadChunks(v.ctx, scope, hashes, func(serverDomain.Hash, []byte) error {
		got++
		return nil
	})
	if err == nil {
		require.Equal(v.t, len(hashes), got)
	}
	return err
}

// TestEndToEnd_ChunkDownloadFollowsRevoke checks that a reader who just
// downloaded the chunks of a path is refused them on the very next request
// once the right is taken away, however it was held.
func TestEndToEnd_ChunkDownloadFollowsRevoke(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// give grants read on secret/ and returns what takes it away again.
		give func(v *chunkVisibilityHarness) (revoke func())
	}{
		{name: "user rule deleted", give: func(v *chunkVisibilityHarness) func() {
			id := v.grant("secret")
			return func() {
				require.NoError(v.t, v.admin.DeletePBACRule(v.ctx, e2eOrgSlug, e2eProjectSlug, id))
			}
		}},
		{name: "removed from group", give: func(v *chunkVisibilityHarness) func() {
			group, err := v.admin.CreateGroup(v.ctx, e2eOrgSlug, "vault", "")
			require.NoError(v.t, err)
			require.NoError(v.t, v.admin.AddGroupMember(v.ctx, e2eOrgSlug, group.ID, v.userID))
			_, err = v.admin.CreatePBACRule(v.ctx, e2eOrgSlug, e2eProjectSlug, "", group.ID, "secret", uint64(serverDomain.PermissionRead))
			require.NoError(v.t, err)
			return func() {
				require.NoError(v.t, v.admin.RemoveGroupMember(v.ctx, e2eOrgSlug, group.ID, v.userID))
			}
		}},
		{name: "group rule deleted", give: func(v *chunkVisibilityHarness) func() {
			group, err := v.admin.CreateGroup(v.ctx, e2eOrgSlug, "vault", "")
			require.NoError(v.t, err)
			require.NoError(v.t, v.admin.AddGroupMember(v.ctx, e2eOrgSlug, group.ID, v.userID))
			rule, err := v.admin.CreatePBACRule(v.ctx, e2eOrgSlug, e2eProjectSlug, "", group.ID, "secret", uint64(serverDomain.PermissionRead))
			require.NoError(v.t, err)
			return func() {
				require.NoError(v.t, v.admin.DeletePBACRule(v.ctx, e2eOrgSlug, e2eProjectSlug, rule.ID))
			}
		}},
		{name: "path default set to deny", give: func(v *chunkVisibilityHarness) func() {
			_, err := v.admin.SetProjectPathPermission(v.ctx, e2eOrgSlug, e2eProjectSlug, "secret", uint64(serverDomain.PermissionRead))
			require.NoError(v.t, err)
			return func() {
				_, err := v.admin.SetProjectPathPermission(v.ctx, e2eOrgSlug, e2eProjectSlug, "secret", 0)
				require.NoError(v.t, err)
			}
		}},
		{name: "path default removed", give: func(v *chunkVisibilityHarness) func() {
			_, err := v.admin.SetProjectPathPermission(v.ctx, e2eOrgSlug, e2eProjectSlug, "secret", uint64(serverDomain.PermissionRead))
			require.NoError(v.t, err)
			return func() {
				require.NoError(v.t, v.admin.DeleteProjectPathPermission(v.ctx, e2eOrgSlug, e2eProjectSlug, "secret"))
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := newChunkVisibilityHarness(t)
			revoke := tc.give(v)
			require.NoError(t, v.download(v.secret), "the reader holds the right")
			require.NoError(t, v.download(v.secret), "and holds it on a repeated request")

			revoke()

			err := v.download(v.secret)
			require.Error(t, err, "a revoked path must not be downloadable on the next request")
			require.Contains(t, err.Error(), "not found")
			require.NoError(t, v.download(v.pub), "what the reader still holds stays downloadable")
		})
	}
}

// TestEndToEnd_ChunkDownloadFollowsGrant is the inverse: a reader refused the
// chunks of a path gets them on the next request once the right is granted.
func TestEndToEnd_ChunkDownloadFollowsGrant(t *testing.T) {
	t.Parallel()
	v := newChunkVisibilityHarness(t)
	require.NoError(t, v.download(v.pub))
	require.Error(t, v.download(v.secret), "hidden before the grant")

	v.grant("secret")

	require.NoError(t, v.download(v.secret), "a granted path must be downloadable on the next request")
	require.NoError(t, v.download(v.pub))
}

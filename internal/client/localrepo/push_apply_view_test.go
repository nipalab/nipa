package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

var (
	pushOldView = []byte("view-of-older-rules")
	pushNewView = []byte("view-from-the-push")
)

// The view token an applied push is recorded with, for every base record and
// every answer the server can give. The rows are the same in every case.
func TestApplyPush_RecordsTheViewThePushVouchesFor(t *testing.T) {
	tc := pushCase{base: applyBase, pushed: map[string]pushSpec{"src/a.go": spec(30)}}
	want := manifestTree(tc.after())

	restricted := domain.PushView{Token: pushNewView}
	cases := []struct {
		name string
		// base is the record of the base tree: no token, the push's token or
		// an older one.
		base []byte
		view domain.PushView
		want []byte
	}{
		{"older server keeps no token", nil, domain.PushView{}, nil},
		{"older server keeps the stored token", pushOldView, domain.PushView{}, pushOldView},
		{"unrestricted fills a missing token", nil, domain.PushView{Token: pushNewView, Unrestricted: true}, pushNewView},
		{"unrestricted replaces an older token", pushOldView, domain.PushView{Token: pushNewView, Unrestricted: true}, pushNewView},
		{"restricted keeps the same token", pushNewView, restricted, pushNewView},
		{"restricted drops an older token", pushOldView, restricted, nil},
		{"restricted cannot vouch for a tree without token", nil, restricted, nil},
		{"hidden pushed path drops the token", pushNewView, domain.PushView{Token: pushNewView, HidesPushed: true}, nil},
		{"hidden pushed path drops it even unrestricted", pushNewView, domain.PushView{Token: pushNewView, Unrestricted: true, HidesPushed: true}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lr := newSyncedRepo(t)
			require.NoError(t, lr.SaveSyncedTreeView(manifestTree(applyBase), "c1", nil, c.base))
			in := tc.apply("c1", "c2", want.Hash)
			in.View = c.view
			require.NoError(t, lr.ApplyPush(in))

			saved := newSyncedRepo(t)
			require.NoError(t, saved.SaveSyncedTreeView(want, "c2", nil, c.want))
			require.Equal(t, readFullState(t, saved), readFullState(t, lr))
			synced, err := lr.LoadSyncedTree()
			require.NoError(t, err)
			require.Equal(t, &clientDomain.SyncedTree{CommitID: "c2", Sparse: []string{}, View: c.want}, synced)
		})
	}
}

// A push onto an empty branch has no stored rows, so a restricted view whose
// pushed paths are all readable is exactly the pushed tree.
func TestApplyPush_FirstPushRecordsARestrictedView(t *testing.T) {
	tc := pushCase{pushed: map[string]pushSpec{"a.txt": spec(1), "src/main.go": spec(2)}}
	want := manifestTree(tc.after())
	for _, view := range []domain.PushView{{Token: pushNewView}, {Token: pushNewView, Unrestricted: true}} {
		lr := newSyncedRepo(t)
		in := tc.apply("", "c1", want.Hash)
		in.View = view
		require.NoError(t, lr.ApplyPush(in))
		synced, err := lr.LoadSyncedTree()
		require.NoError(t, err)
		require.Equal(t, pushNewView, synced.View)
	}

	lr := newSyncedRepo(t)
	in := tc.apply("", "c1", want.Hash)
	in.View = domain.PushView{Token: pushNewView, HidesPushed: true}
	require.NoError(t, lr.ApplyPush(in))
	synced, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Empty(t, synced.View)
}

// A refused apply writes nothing, the token included.
func TestApplyPush_MismatchRecordsNoView(t *testing.T) {
	tc := pushCase{base: applyBase, pushed: map[string]pushSpec{"src/a.go": spec(30)}}
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTreeView(manifestTree(applyBase), "c1", nil, pushOldView))
	before := readFullState(t, lr)
	in := tc.apply("c1", "c2", domain.Hash{1})
	in.View = domain.PushView{Token: pushNewView, Unrestricted: true}
	var mismatch *clientDomain.PushApplyMismatchError
	require.ErrorAs(t, lr.ApplyPush(in), &mismatch)
	require.Equal(t, before, readFullState(t, lr))
}

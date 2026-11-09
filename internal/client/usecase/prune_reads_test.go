package usecase

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

func TestRestore_PrunesDownloadedBinaryKeepsText(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{
		"a.bin":     "\x00binary original\x01",
		"notes.txt": "cached text\n",
	})
	writeFileAt(t, fx.root, "a.bin", "\x00binary edited\x02")
	writeFileAt(t, fx.root, "notes.txt", "edited text\n")
	fx.dropObjects(t)
	dl := &stubContentDownloader{data: fx.stored}

	require.NoError(t, fx.workingCopy(t, dl).Restore(context.Background(), []string{"a.bin", "notes.txt"}))
	require.Equal(t, 1, dl.calls)
	require.Equal(t, "\x00binary original\x01", string(readRepoFile(t, fx.root, "a.bin")))
	fx.requirePruned(t, "a.bin")
	fx.requireCached(t, "notes.txt")
}

func TestRestore_KeepsPinnedBinary(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.bin": "\x00binary original\x01"})
	require.NoError(t, fx.lr.PinPaths([]string{"a.bin"}))
	writeFileAt(t, fx.root, "a.bin", "\x00binary edited\x02")

	require.NoError(t, fx.workingCopy(t, nil).Restore(context.Background(), []string{"a.bin"}))
	fx.requireCached(t, "a.bin")
}

func TestDiff_WithPrunePrunesDownloadedOldSide(t *testing.T) {
	for _, prune := range []bool{true, false} {
		t.Run(fmt.Sprintf("prune=%v", prune), func(t *testing.T) {
			fx := newPristineFixture(t, map[string]string{"a.bin": "\x00old\x01"})
			fx.dropObjects(t)
			writeFileAt(t, fx.root, "a.bin", "\x00new\x02")
			client := &stubDiffClient{download: fx.stored}
			d := NewDiff(diffAuth(t), client, fx.lr)
			if prune {
				d = d.WithPrune()
			}

			files, err := d.Run(context.Background(), fx.root, nil, DiffOptions{Binary: true})
			require.NoError(t, err)
			require.NotEmpty(t, client.downloaded)
			require.Equal(t, "\x00old\x01", string(workingDiffFile(t, files, "a.bin").Old))
			if prune {
				fx.requirePruned(t, "a.bin")
			} else {
				fx.requireCached(t, "a.bin")
			}
		})
	}
}

func TestDiff_WithPruneSkipsOnFailure(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.bin": "\x00old\x01"})

	_, err := NewDiff(nil, nil, fx.lr).WithPrune().Run(context.Background(), fx.root, []string{"a", "b", "c"}, DiffOptions{})
	require.ErrorContains(t, err, "too many revisions")
	fx.requireCached(t, "a.bin")
}

func TestOfflineError(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "connection error")
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	read := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("reset")}
	other := status.Error(codes.PermissionDenied, "nope")

	require.NoError(t, offlineError(nil))
	for _, err := range []error{unavailable, fmt.Errorf("download: %w", unavailable), dial, fmt.Errorf("get: %w", dial)} {
		got := offlineError(err)
		var userErr *clientDomain.Error
		require.ErrorAs(t, got, &userErr)
		require.Equal(t, 400, userErr.Code)
		require.Equal(t, errNotCached, userErr.Message)
	}
	require.Same(t, read, offlineError(read))
	require.Equal(t, other, offlineError(other))
}

func TestRestore_UnreachableServerIsUserError(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.bin": "\x00binary original\x01"})
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.bin", "\x00binary edited\x02")
	dl := &stubContentDownloader{connectErr: status.Error(codes.Unavailable, "connection refused")}

	err := fx.workingCopy(t, dl).Restore(context.Background(), []string{"a.bin"})
	require.EqualError(t, err, errNotCached)
	require.Equal(t, "\x00binary edited\x02", string(readRepoFile(t, fx.root, "a.bin")))
}

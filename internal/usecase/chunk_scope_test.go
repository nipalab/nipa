package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

func newLinkFixture(t *testing.T, store storage.ChunkStore) (*Chunk, *fakeChunkRepo) {
	t.Helper()
	repo := newFakeChunkRepo()
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour, MaxPageSize: 10})
	return uc, repo
}

// chunkTxRecorder runs fn in a fake transaction: it marks the context, so
// the ledger and the repository can check they joined it, and records when
// the transaction ended.
type chunkTxRecorder struct {
	began, committed, rolledBack int
}

type fakeTxKey struct{}

func (r *chunkTxRecorder) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	r.began++
	if err := fn(context.WithValue(ctx, fakeTxKey{}, r)); err != nil {
		r.rolledBack++
		return err
	}
	r.committed++
	return nil
}

func inFakeTx(ctx context.Context) bool {
	_, ok := ctx.Value(fakeTxKey{}).(*chunkTxRecorder)
	return ok
}

// TestChunk_ConfirmUploads_LedgerThenRowsInOneTransaction checks S5 for
// per-project rows: the ledger attributes first, the rows are inserted after
// it accepted them, and both run in the same transaction, so a row is never
// visible before the quota passed.
func TestChunk_ConfirmUploads_LedgerThenRowsInOneTransaction(t *testing.T) {
	ctx := context.Background()
	data := []byte("confirmed content")
	hash := chunker.Sum(data)
	store := newStubChunkStore()
	store.data[ck(hash)] = data
	uc, repo := newLinkFixture(t, store)
	tx := &chunkTxRecorder{}
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc.WithStorageLedger(ledger).WithTransactor(tx)

	var order []string
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject, []LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}}).
		DoAndReturn(func(ctx context.Context, _ *domain.Organization, _ *domain.Project, _ []LedgerChunk) error {
			require.True(t, inFakeTx(ctx), "the ledger joins the confirm's transaction")
			require.False(t, repo.has(testProject.ID, hash), "no row before the ledger accepted it")
			order = append(order, "ledger")
			return nil
		})
	repo.onInsert = func(ctx context.Context, projectID snow.ID, _ []ChunkRef) {
		require.True(t, inFakeTx(ctx), "the rows are inserted in the same transaction")
		require.Equal(t, testProject.ID, projectID)
		require.Zero(t, tx.committed, "rows are written before the transaction commits")
		order = append(order, "rows")
	}

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.Equal(t, []string{"ledger", "rows"}, order)
	require.Equal(t, 1, tx.committed)
	require.True(t, repo.has(testProject.ID, hash))
}

func TestChunk_ConfirmUploads_QuotaFailureRollsBackWithoutRows(t *testing.T) {
	ctx := context.Background()
	data := []byte("over quota")
	hash := chunker.Sum(data)
	store := newStubChunkStore()
	store.data[ck(hash)] = data
	uc, repo := newLinkFixture(t, store)
	tx := &chunkTxRecorder{}
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc.WithStorageLedger(ledger).WithTransactor(tx)
	quota := domain.NewErrorQuotaExceeded("over quota")
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject, gomock.Any()).Return(quota)

	_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
	require.ErrorIs(t, err, quota)
	require.Equal(t, 1, tx.rolledBack)
	require.Empty(t, repo.inserts)
}

func TestChunk_ConfirmUploads_NothingNewOpensNoTransaction(t *testing.T) {
	ctx := context.Background()
	data := []byte("already recorded")
	hash := chunker.Sum(data)
	uc, repo := newLinkFixture(t, newStubChunkStore())
	tx := &chunkTxRecorder{}
	uc.WithStorageLedger(NewMockStorageLedger(gomock.NewController(t))).WithTransactor(tx)
	repo.record(testProject.ID, hash, int64(len(data)))

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.Zero(t, tx.began, "recorded chunks need no ledger and no rows")
}

// TestChunk_SameHashInTwoProjects checks per-project dedup end to end on the
// local store: the same bytes uploaded by two projects become two objects and
// two rows, each project's confirm only sees its own object, and one
// project's row never makes the other's presign skip the upload.
func TestChunk_SameHashInTwoProjects(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	uc, repo := newLinkFixture(t, store)
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }
	other := &domain.Project{ID: 2, Slug: "other"}

	data := []byte("engine binary")
	hash := chunker.Sum(data)
	ref := []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}

	for _, project := range []*domain.Project{testProject, other} {
		urls, _, err := uc.PresignUploadURLs(ctx, testOrg, project, ref, 10, "")
		require.NoError(t, err)
		require.False(t, urls[0].AlreadyStored, "project %s must upload its own copy", project.Slug)
		require.NotEmpty(t, urls[0].URL)

		receipt, err := uc.StoreUploaded(ctx, project.ID, hash, data)
		require.NoError(t, err)
		missing, err := uc.ConfirmUploads(ctx, testOrg, project, []domain.Hash{hash}, []ChunkReceipt{{Hash: hash, Receipt: receipt}})
		require.NoError(t, err)
		require.Empty(t, missing)
	}

	require.True(t, repo.has(testProject.ID, hash))
	require.True(t, repo.has(other.ID, hash))
	for _, project := range []snow.ID{testProject.ID, other.ID} {
		got, err := store.Get(ctx, project, hash)
		require.NoError(t, err)
		require.Equal(t, data, got)
		urls, _, err := uc.PresignUploadURLs(ctx, testOrg, &domain.Project{ID: project, Slug: "x"}, ref, 10, "")
		require.NoError(t, err)
		require.True(t, urls[0].AlreadyStored)
	}

	// Deleting one project's content leaves the other's untouched.
	require.NoError(t, store.DeleteProject(ctx, other.ID))
	got, err := uc.Download(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
	_, err = uc.Download(ctx, other.ID, hash)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestChunk_AttributeProjectChunks_RecordsWithoutLedger(t *testing.T) {
	ctx := context.Background()
	uc, repo := newLinkFixture(t, newStubChunkStore())
	tx := &chunkTxRecorder{}
	uc.WithTransactor(tx)
	hashes := []domain.Hash{chunker.Sum([]byte("a")), chunker.Sum([]byte("b"))}

	require.NoError(t, uc.AttributeProjectChunks(ctx, testProject.ID, []LedgerChunk{{Hash: hashes[0], SizeBytes: 1}, {Hash: hashes[1], SizeBytes: 1}}))
	require.Equal(t, [][]ChunkRef{{{Hash: hashes[0], SizeBytes: 1}, {Hash: hashes[1], SizeBytes: 1}}}, repo.inserts)
	require.Equal(t, 1, tx.committed)
}

func TestChunk_AttributeProjectChunks_QuotaFailureLeavesNoRow(t *testing.T) {
	ctx := context.Background()
	uc, repo := newLinkFixture(t, newStubChunkStore())
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc.WithStorageLedger(ledger)
	quota := domain.NewErrorQuotaExceeded("over quota")
	chunks := []LedgerChunk{{Hash: chunker.Sum([]byte("merge")), SizeBytes: 5}}
	ledger.EXPECT().AttributeProjectChunks(gomock.Any(), testProject.ID, chunks).Return(quota)

	require.ErrorIs(t, uc.AttributeProjectChunks(ctx, testProject.ID, chunks), quota)
	require.Empty(t, repo.inserts)
}

func TestChunk_AttributeProjectChunks_ResolverError(t *testing.T) {
	uc, _ := newLinkFixture(t, newStubChunkStore())
	resolveErr := errors.New("resolver down")
	uc.WithChunkStores(fakeChunkStores{err: resolveErr})

	err := uc.AttributeProjectChunks(context.Background(), testProject.ID, []LedgerChunk{{Hash: domain.Hash{1}, SizeBytes: 1}})
	require.ErrorIs(t, err, resolveErr)
}

func TestChunk_UploadForProject(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	uc, repo := newLinkFixture(t, store)
	data := []byte("server content")
	hash := chunker.Sum(data)

	isNew, err := uc.UploadForProject(ctx, testProject.ID, hash, data)
	require.NoError(t, err)
	require.True(t, isNew)
	require.True(t, repo.has(testProject.ID, hash))
	require.Equal(t, data, store.data[ck(hash)])

	isNew, err = uc.UploadForProject(ctx, testProject.ID, hash, data)
	require.NoError(t, err)
	require.False(t, isNew)

	_, err = uc.UploadForProject(ctx, testProject.ID, hash, []byte("mismatch"))
	require400(t, err, "chunk hash mismatch")
}

func TestUniqueHashes(t *testing.T) {
	a, b := domain.Hash{1}, domain.Hash{2}
	require.Equal(t, []domain.Hash{a, b}, uniqueHashes([]domain.Hash{a, b, a, b, a}))
	require.Empty(t, uniqueHashes(nil))
}

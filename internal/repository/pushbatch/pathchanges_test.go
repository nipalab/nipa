package pushbatch

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/usecase"
)

func TestInsertPathChanges(t *testing.T) {
	old := pathChangeBatch
	pathChangeBatch = 2
	t.Cleanup(func() { pathChangeBatch = old })

	var payloads []string
	insert := func(_ context.Context, payload []byte) error {
		payloads = append(payloads, string(payload))
		return nil
	}
	changes := []usecase.PathChange{
		{Path: "b", Kind: usecase.PathChangeFile},
		{Path: "a/x", Kind: usecase.PathChangeMode},
		{Path: "a", Kind: usecase.PathChangeDir},
		{Path: "a/x", Kind: usecase.PathChangeMode},
		{Path: "a", Kind: usecase.PathChangeFile},
	}
	require.NoError(t, InsertPathChanges(context.Background(), changes, insert))
	require.Equal(t, []string{`[["a",1],["a",3]]`, `[["a/x",2],["b",1]]`}, payloads)
	require.Equal(t, "b", changes[0].Path, "the caller's slice is left alone")

	payloads = nil
	require.NoError(t, InsertPathChanges(context.Background(), nil, insert))
	require.Empty(t, payloads)

	wantErr := errors.New("db down")
	err := InsertPathChanges(context.Background(), changes, func(context.Context, []byte) error { return wantErr })
	require.ErrorIs(t, err, wantErr)
}

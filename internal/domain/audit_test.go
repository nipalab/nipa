package domain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/snow"
)

func TestRequestMetaContext(t *testing.T) {
	_, ok := RequestMetaFromContext(context.Background())
	require.False(t, ok)

	ctx := ContextWithRequestMeta(context.Background(), RequestMeta{ClientIP: "10.0.0.1", UserAgent: "ua"})
	meta, ok := RequestMetaFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, RequestMeta{ClientIP: "10.0.0.1", UserAgent: "ua"}, meta)

	id := IDPtr(snow.ID(4))
	require.Equal(t, snow.ID(4), *id)
}

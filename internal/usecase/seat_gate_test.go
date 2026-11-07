package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdmitSeat_NilGateInsertsDirectly(t *testing.T) {
	called := false
	require.NoError(t, admitSeat(context.Background(), nil, 1, 2, func(context.Context) error {
		called = true
		return nil
	}))
	require.True(t, called)
}

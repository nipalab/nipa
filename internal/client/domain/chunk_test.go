package domain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifyObserver(t *testing.T) {
	NotifyVerify(context.Background(), 3)

	var got []int
	ctx := WithVerifyObserver(context.Background(), func(n int) { got = append(got, n) })
	NotifyVerify(ctx, 7)
	require.Equal(t, []int{7}, got)
}

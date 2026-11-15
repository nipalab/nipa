package maintenance

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCErrorRoundTrip(t *testing.T) {
	end := testNow.Add(10 * time.Minute)
	e := NewError(projectWindow(1, 10, "acme", "game", ReadOnly, testNow), ScopeProject)
	e.Info.Message, e.Info.EndsAt = "repair", &end

	err := e.GRPCError(testNow)
	st := status.Convert(err)
	require.Equal(t, codes.Unavailable, st.Code())
	require.Equal(t, "project acme/game is in maintenance (read-only): repair", st.Message())
	require.Len(t, st.Details(), 2)
	info := st.Details()[0].(*errdetails.ErrorInfo)
	require.Equal(t, "MAINTENANCE", info.GetReason())
	require.Equal(t, "nipa", info.GetDomain())
	require.Equal(t, "acme/game", info.GetMetadata()["project"])
	require.Equal(t, "acme", info.GetMetadata()["org"])
	require.Equal(t, 10*time.Minute, st.Details()[1].(*errdetails.RetryInfo).GetRetryDelay().AsDuration())

	got, ok := FromGRPC(fmt.Errorf("restore: %w", err))
	require.True(t, ok)
	require.Equal(t, e.Error(), got.Error())
	require.True(t, end.Equal(*got.Info.EndsAt))

	noEnd := NewError(Window{Mode: Full}, ScopeServer).GRPCError(testNow)
	require.Len(t, status.Convert(noEnd).Details(), 1)
}

func TestFromGRPCIgnoresOtherErrors(t *testing.T) {
	_, ok := FromGRPC(nil)
	require.False(t, ok)
	_, ok = FromGRPC(errors.New("plain"))
	require.False(t, ok)
	_, ok = FromGRPC(status.Error(codes.Unavailable, "connection refused"))
	require.False(t, ok)
	_, ok = FromGRPC(status.Error(codes.Internal, "boom"))
	require.False(t, ok)
	other, err := status.New(codes.Unavailable, "x").WithDetails(&errdetails.ErrorInfo{Reason: "OTHER", Domain: Domain})
	require.NoError(t, err)
	_, ok = FromGRPC(other.Err())
	require.False(t, ok)
}

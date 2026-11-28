package domain

import (
	"testing"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestPushApplyErrors(t *testing.T) {
	clash := &PushApplyClashError{Path: "a/x"}
	require.EqualError(t, clash, `the pushed tree holds "a/x" as a file and as a folder`)

	mismatch := &PushApplyMismatchError{Expected: serverDomain.Hash{1}, Computed: serverDomain.Hash{2}}
	require.Contains(t, mismatch.Error(), mismatch.Expected.String())
	require.Contains(t, mismatch.Error(), mismatch.Computed.String())
}

package output

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

func TestNewMergeRequestChecks(t *testing.T) {
	created := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	checks := NewMergeRequestChecks([]*clientDomain.MergeRequestCheck{
		nil,
		{
			ID:         "k",
			Name:       "build",
			State:      clientDomain.MergeRequestCheckSuccess,
			DetailsURL: "https://ci.example/run/1",
			Reporter:   clientDomain.ReviewActor{UserID: "7", Name: "Alice"},
			CreatedAt:  created,
			UpdatedAt:  created,
		},
	})
	require.Len(t, checks.Checks, 1)
	require.Equal(t, "k", checks.Checks[0].ID)
	require.Equal(t, "build", checks.Checks[0].Name)
	require.Equal(t, clientDomain.MergeRequestCheckSuccess, checks.Checks[0].State)
	require.Equal(t, "https://ci.example/run/1", checks.Checks[0].DetailsURL)
	require.Equal(t, "Alice", checks.Checks[0].Reporter.Name)
	require.NotEmpty(t, checks.Checks[0].CreatedAt)
	require.NotEmpty(t, checks.Checks[0].UpdatedAt)

	empty := NewMergeRequestChecks(nil)
	require.NotNil(t, empty.Checks)
	require.Empty(t, empty.Checks)
}

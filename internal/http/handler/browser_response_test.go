package handler

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestToBranchResponse_ProtectionFields(t *testing.T) {
	branch := &domain.Branch{
		ID:                  3,
		Name:                "main",
		RequireStatusChecks: true,
		RequiredChecks:      []string{"build", "test"},
		RequiredReviewers: []domain.ReviewActor{
			{UserID: snow.ID(7), Name: "Alice", PhotoURL: "p.png"},
		},
	}

	resp := toBranchResponse(branch)
	require.True(t, resp.RequireStatusChecks)
	require.Equal(t, []string{"build", "test"}, resp.RequiredChecks)
	require.Len(t, resp.RequiredReviewers, 1)
	require.Equal(t, snow.ID(7).Base36(), resp.RequiredReviewers[0].UserID)
	require.Equal(t, "Alice", resp.RequiredReviewers[0].Name)
	require.Equal(t, "p.png", resp.RequiredReviewers[0].PhotoURL)
}

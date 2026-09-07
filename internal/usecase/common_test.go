package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubCommonOrgRepo struct {
	org  *domain.Organization
	err  error
}

func (s *stubCommonOrgRepo) GetBySlug(_ context.Context, _ string) (*domain.Organization, error) {
	return s.org, s.err
}

type stubCommonProjectRepo struct {
	project *domain.Project
	err     error
}

func (s *stubCommonProjectRepo) GetByOrgIDAndSlug(_ context.Context, _ snow.ID, _ string) (*domain.Project, error) {
	return s.project, s.err
}

func TestResolveBySlug_Success(t *testing.T) {
	org := &domain.Organization{ID: snow.ID(1), Slug: "default"}
	project := &domain.Project{ID: snow.ID(42), OrgID: snow.ID(1), Slug: "sample"}
	c := NewCommon(&stubCommonOrgRepo{org: org}, &stubCommonProjectRepo{project: project})

	gotOrg, gotProject, err := c.ResolveBySlug(context.Background(), "default", "sample")
	require.NoError(t, err)
	require.Equal(t, org, gotOrg)
	require.Equal(t, project, gotProject)
}

func TestResolveBySlug_OrgNotFound(t *testing.T) {
	c := NewCommon(&stubCommonOrgRepo{err: domain.NewErrorNotFound("record not found")}, &stubCommonProjectRepo{})

	_, _, err := c.ResolveBySlug(context.Background(), "missing", "sample")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `organization "missing" not found`, domErr.Message)
}

func TestResolveBySlug_ProjectNotFound(t *testing.T) {
	org := &domain.Organization{ID: snow.ID(1), Slug: "default"}
	c := NewCommon(&stubCommonOrgRepo{org: org}, &stubCommonProjectRepo{err: domain.NewErrorNotFound("record not found")})

	_, _, err := c.ResolveBySlug(context.Background(), "default", "missing")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `project "missing" not found`, domErr.Message)
}

func TestResolveBySlug_OrgRepoError(t *testing.T) {
	wantErr := errors.New("db down")
	c := NewCommon(&stubCommonOrgRepo{err: wantErr}, &stubCommonProjectRepo{})

	_, _, err := c.ResolveBySlug(context.Background(), "default", "sample")
	require.ErrorIs(t, err, wantErr)
}

func TestResolveBySlug_ProjectRepoError(t *testing.T) {
	wantErr := errors.New("db down")
	org := &domain.Organization{ID: snow.ID(1), Slug: "default"}
	c := NewCommon(&stubCommonOrgRepo{org: org}, &stubCommonProjectRepo{err: wantErr})

	_, _, err := c.ResolveBySlug(context.Background(), "default", "sample")
	require.ErrorIs(t, err, wantErr)
}
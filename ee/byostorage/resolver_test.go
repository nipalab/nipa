package byostorage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/snow"
)

func TestResolver(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	f.repo.projects[project1] = server.ID
	fallback := &stubStore{}
	resolver := f.svc.Resolver(fallback)
	now := time.Unix(1000, 0)
	resolver.now = func() time.Time { return now }
	ctx := context.Background()

	plain, err := resolver.ForProject(ctx, snow.ID(99))
	require.NoError(t, err)
	require.Same(t, fallback, plain.Store)
	require.False(t, plain.External)

	resolved, err := resolver.ForProject(ctx, project1)
	require.NoError(t, err)
	require.True(t, resolved.External)
	require.False(t, resolved.ReadOnly)
	opened := resolved.Store.(*stubStore)
	require.Equal(t, "secret", opened.cfg.SecretAccessKey)
	require.Equal(t, "assets", opened.cfg.Bucket)
	require.True(t, opened.cfg.SkipProbe)

	again, err := resolver.ForProject(ctx, project1)
	require.NoError(t, err)
	require.Same(t, opened, again.Store)
	require.Equal(t, 1, f.repo.calls["ProjectServer"]-1, "the mapping is cached within the TTL")

	f.repo.servers[server.ID].UpdatedAt = server.UpdatedAt.Add(time.Minute)
	f.repo.servers[server.ID].AccessKeyID = "rotated"
	cached, err := resolver.ForProject(ctx, project1)
	require.NoError(t, err)
	require.Same(t, opened, cached.Store, "an edit is seen only after the TTL")

	now = now.Add(ResolverTTL)
	reopened, err := resolver.ForProject(ctx, project1)
	require.NoError(t, err)
	require.NotSame(t, opened, reopened.Store)
	require.True(t, opened.closed, "the replaced store is closed")
	require.Equal(t, "rotated", reopened.Store.(*stubStore).cfg.AccessKeyID)

	resolver.svc.WithEntitlement(stubEntitlement{active: false})
	readOnly, err := resolver.ForProject(ctx, project1)
	require.NoError(t, err)
	require.True(t, readOnly.ReadOnly)
	require.True(t, readOnly.External)

	resolver.svc.WithEntitlement(stubEntitlement{err: errBoom})
	_, err = resolver.ForProject(ctx, project1)
	require.ErrorIs(t, err, errBoom)
}

func TestResolver_Failures(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	f.repo.projects[project1] = server.ID
	resolver := f.svc.Resolver(&stubStore{})
	ctx := context.Background()

	f.repo.fail["ProjectServer"] = errBoom
	_, err := resolver.ForProject(ctx, project1)
	require.ErrorIs(t, err, errBoom)
	f.repo.fail = map[string]error{}

	f.opener.openErr = errBoom
	_, err = resolver.ForProject(ctx, project1)
	require.Equal(t, 502, errorCode(err))
	require.ErrorContains(t, err, "storage server unavailable")
	f.opener.openErr = nil

	f.repo.servers[server.ID].SecretSealed = []byte("bad")
	resolver = f.svc.Resolver(&stubStore{})
	_, err = resolver.ForProject(ctx, project1)
	require.Equal(t, 502, errorCode(err))
}

func TestResolver_PrunesExpiredProjects(t *testing.T) {
	f := newFixture(t)
	resolver := f.svc.Resolver(&stubStore{})
	now := time.Unix(1000, 0)
	resolver.now = func() time.Time { return now }
	for i := 0; i < maxCachedProjects; i++ {
		resolver.projects[snow.ID(10000+i)] = projectEntry{fetched: now.Add(-2 * ResolverTTL)}
	}
	_, err := resolver.ForProject(context.Background(), project1)
	require.NoError(t, err)
	require.Len(t, resolver.projects, 1)
}

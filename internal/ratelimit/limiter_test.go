package ratelimit

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/obs"
)

func TestLimiterNilAllows(t *testing.T) {
	var l *Limiter
	assert.True(t, l.Allow(context.Background(), ScopeAuthIP, "1.2.3.4").Allowed)
	require.NoError(t, l.Close())
}

func TestLimiterSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	l := NewLimiter(store, map[string]Rule{ScopeAuthIP: {Rate: 1, Burst: 1}, ScopeAPIUser: {}})
	assert.True(t, l.Allow(context.Background(), "unknown", "x").Allowed)
	assert.True(t, l.Allow(context.Background(), ScopeAPIUser, "x").Allowed)
	assert.True(t, l.Allow(context.Background(), ScopeAuthIP, "").Allowed)
}

func TestLimiterDelegatesAndPrefixesKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	rule := Rule{Rate: 1, Burst: 2}
	l := NewLimiter(store, map[string]Rule{ScopeAuthIP: rule}).WithMetrics(obs.NewMetrics())

	store.EXPECT().Take(gomock.Any(), "auth_ip:1.2.3.4", rule, 1).Return(Result{Allowed: true}, nil)
	assert.True(t, l.Allow(context.Background(), ScopeAuthIP, "1.2.3.4").Allowed)

	store.EXPECT().Take(gomock.Any(), "auth_ip:1.2.3.4", rule, 1).Return(Result{}, nil)
	assert.False(t, l.Allow(context.Background(), ScopeAuthIP, "1.2.3.4").Allowed)
}

func TestLimiterFailsOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	l := NewLimiter(store, map[string]Rule{ScopeAPIUser: {Rate: 1, Burst: 1}})
	store.EXPECT().Take(gomock.Any(), gomock.Any(), gomock.Any(), 1).Return(Result{}, errors.New("redis down")).Times(3)
	for range 3 {
		assert.True(t, l.Allow(context.Background(), ScopeAPIUser, "x").Allowed)
	}
	assert.Nil(t, l.fallback)
}

func TestLimiterAuthScopesFallBackToMemory(t *testing.T) {
	for _, scope := range []string{ScopeAuthIP, ScopeAuthID, ScopeAuthEmail, ScopeRegisterEmail} {
		t.Run(scope, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := NewMockStore(ctrl)
			store.EXPECT().Close().Return(nil)
			l := NewLimiter(store, map[string]Rule{scope: {Rate: 0.001, Burst: 2}}).WithMetrics(obs.NewMetrics())
			store.EXPECT().Take(gomock.Any(), gomock.Any(), gomock.Any(), 1).Return(Result{}, errors.New("redis down")).Times(3)
			assert.True(t, l.Allow(context.Background(), scope, "x").Allowed)
			assert.True(t, l.Allow(context.Background(), scope, "x").Allowed)
			res := l.Allow(context.Background(), scope, "x")
			assert.False(t, res.Allowed)
			assert.Positive(t, res.RetryAfter)
			require.NoError(t, l.Close())
		})
	}
}

func TestLimiterFallbackErrorAllows(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	fallback := NewMockStore(ctrl)
	l := NewLimiter(store, map[string]Rule{ScopeAuthID: {Rate: 1, Burst: 1}})
	l.newFallback = func() Store { return fallback }
	store.EXPECT().Take(gomock.Any(), "auth_id:x", gomock.Any(), 1).Return(Result{}, errors.New("redis down"))
	fallback.EXPECT().Take(gomock.Any(), "auth_id:x", gomock.Any(), 1).Return(Result{}, errors.New("broken"))
	assert.True(t, l.Allow(context.Background(), ScopeAuthID, "x").Allowed)
}

func TestLimiterClosedFallbackAllows(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	store.EXPECT().Close().Return(nil)
	l := NewLimiter(store, map[string]Rule{ScopeAuthIP: {Rate: 1, Burst: 1}})
	require.NoError(t, l.Close())
	store.EXPECT().Take(gomock.Any(), gomock.Any(), gomock.Any(), 1).Return(Result{}, errors.New("redis down"))
	assert.True(t, l.Allow(context.Background(), ScopeAuthIP, "x").Allowed)
}

func TestLimiterClose(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	store.EXPECT().Close().Return(nil)
	require.NoError(t, NewLimiter(store, nil).Close())
}

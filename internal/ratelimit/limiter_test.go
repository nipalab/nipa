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
	l := NewLimiter(store, map[string]Rule{ScopeAuthIP: {Rate: 1, Burst: 1}})
	store.EXPECT().Take(gomock.Any(), gomock.Any(), gomock.Any(), 1).Return(Result{}, errors.New("redis down"))
	assert.True(t, l.Allow(context.Background(), ScopeAuthIP, "x").Allowed)
}

func TestLimiterClose(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	store.EXPECT().Close().Return(nil)
	require.NoError(t, NewLimiter(store, nil).Close())
}

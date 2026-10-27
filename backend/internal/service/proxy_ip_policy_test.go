//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeProxyIPPolicy(t *testing.T) {
	p := &ProxyIPPolicy{Platform: " ollama ", Mode: ProxyIPPolicyModeSticky, OnEgressFailure: ProxyIPFailureReassign, PoolIDs: []int64{3, 0, 1, 3}}
	require.NoError(t, normalizeProxyIPPolicy(p))
	require.Equal(t, "ollama", p.Platform)
	require.Equal(t, ProxyIPFailurePause, p.OnEgressFailure, "sticky 故障只暂停")
	require.Equal(t, []int64{1, 3}, p.PoolIDs)

	c := &ProxyIPPolicy{Platform: "kiro", Mode: ProxyIPPolicyModeChurn}
	require.NoError(t, normalizeProxyIPPolicy(c))
	require.Equal(t, ProxyIPFailureReassign, c.OnEgressFailure)

	require.ErrorIs(t, normalizeProxyIPPolicy(&ProxyIPPolicy{Platform: "x", Mode: "rotate"}), ErrProxyIPPolicyInvalid)
	require.ErrorIs(t, normalizeProxyIPPolicy(&ProxyIPPolicy{Platform: "", Mode: ProxyIPPolicyModeChurn}), ErrProxyIPPolicyInvalid)
	require.ErrorIs(t, normalizeProxyIPPolicy(&ProxyIPPolicy{Platform: "x", Mode: ProxyIPPolicyModeChurn, MaxLifetimeAccounts: intPtr(0)}), ErrProxyPoolLimitInvalid)
}

func TestProxyPoolPick_RequiresPolicyAndAllowedPool(t *testing.T) {
	pool := &ProxyPool{ID: 1}
	other := &ProxyPool{ID: 2}
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{1: pool, 2: other},
		policies: []ProxyIPPolicy{{Platform: "kiro", Mode: ProxyIPPolicyModeChurn, PoolIDs: []int64{1}}},
		members:  []ProxyPoolMember{poolMember(1, StatusActive, nil)},
	}
	svc := withProxyPoolService(t, repo)
	ctx := context.Background()

	_, err := svc.pickProxy(ctx, pool, "opencode", 0, nil, false)
	require.ErrorIs(t, err, ErrProxyIPPolicyMissing, "没有策略的平台不能从池分配")
	_, err = svc.pickProxy(ctx, other, "kiro", 0, nil, false)
	require.ErrorIs(t, err, ErrProxyIPPolicyPoolDenied)
	p, err := svc.pickProxy(ctx, pool, "kiro", 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID)
}

func TestProxyPoolPick_StickyPrefersChurnUsedUp(t *testing.T) {
	pool := &ProxyPool{ID: 1}
	used := poolMember(1, StatusActive, map[string]int64{"kiro": 5})
	fresh := poolMember(2, StatusActive, nil)
	busy := poolMember(3, StatusActive, map[string]int64{"kiro": 5, "ollama": 1})
	busy.ActiveCounts = map[string]int64{"ollama": 1}
	repo := &proxyPoolRepoStub{
		pools: map[int64]*ProxyPool{1: pool},
		policies: []ProxyIPPolicy{
			churnPolicy("kiro", 5),
			{Platform: "ollama", Mode: ProxyIPPolicyModeSticky, MaxActiveAccounts: intPtr(1)},
		},
		members: []ProxyPoolMember{fresh, busy, used},
	}
	svc := withProxyPoolService(t, repo)
	ctx := context.Background()

	// 长期平台：优先 kiro 用满的 IP；3 号对 ollama 已满（max_active=1）被跳过。
	p, err := svc.pickProxy(ctx, pool, "ollama", 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID)

	// 消耗型平台：已退役的 1、3 不可用，只剩新 IP。
	p, err = svc.pickProxy(ctx, pool, "kiro", 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), p.ID)
}

func TestDeletePoolReferencedByPolicy(t *testing.T) {
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{1: {ID: 1, IsDefault: true}, 2: {ID: 2}, 3: {ID: 3}},
		policies: []ProxyIPPolicy{{Platform: "kiro", Mode: ProxyIPPolicyModeChurn, PoolIDs: []int64{2}}},
	}
	svc := withProxyPoolService(t, repo)
	ctx := context.Background()
	require.ErrorIs(t, svc.DeletePool(ctx, 2), ErrProxyPoolReferencedByPolicy)
	require.NoError(t, svc.DeletePool(ctx, 3))
}

func TestUpsertPolicyRejectsMissingPool(t *testing.T) {
	repo := &proxyPoolRepoStub{pools: map[int64]*ProxyPool{1: {ID: 1}}}
	svc := withProxyPoolService(t, repo)
	_, err := svc.UpsertPolicy(context.Background(), &ProxyIPPolicy{Platform: "kiro", Mode: ProxyIPPolicyModeChurn, PoolIDs: []int64{9}})
	require.ErrorIs(t, err, ErrProxyIPPolicyPoolNotFound)
	p, err := svc.UpsertPolicy(context.Background(), &ProxyIPPolicy{Platform: "kiro", Mode: ProxyIPPolicyModeChurn, PoolIDs: []int64{1}})
	require.NoError(t, err)
	require.Equal(t, ProxyIPFailureReassign, p.OnEgressFailure)
}

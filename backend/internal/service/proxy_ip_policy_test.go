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

	_, err := svc.pickProxy(ctx, pool, "opencode", nil, 0, nil, false)
	require.ErrorIs(t, err, ErrProxyIPPolicyMissing, "没有策略的平台不能从池分配")
	_, err = svc.pickProxy(ctx, other, "kiro", nil, 0, nil, false)
	require.ErrorIs(t, err, ErrProxyIPPolicyPoolDenied)
	p, err := svc.pickProxy(ctx, pool, "kiro", nil, 0, nil, false)
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
	p, err := svc.pickProxy(ctx, pool, "ollama", nil, 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID)

	// 消耗型平台：已退役的 1、3 不可用，只剩新 IP。
	p, err = svc.pickProxy(ctx, pool, "kiro", nil, 0, nil, false)
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

func groupPolicy(id int64, platform string, p ProxyIPPolicy) ProxyIPPolicy {
	p.ID, p.GroupID, p.Platform, p.GroupName = id, &id, platform, "g"
	if p.OnEgressFailure == "" {
		p.OnEgressFailure = ProxyIPFailurePause
	}
	return p
}

func TestPolicyFor_GroupOverridesPlatform(t *testing.T) {
	base := ProxyIPPolicy{ID: 1, Platform: "openai", Mode: ProxyIPPolicyModeSticky, OnEgressFailure: ProxyIPFailurePause}
	plus := groupPolicy(10, "openai", ProxyIPPolicy{Mode: ProxyIPPolicyModeChurn, MaxLifetimeAccounts: intPtr(3)})
	pro := groupPolicy(20, "openai", ProxyIPPolicy{Mode: ProxyIPPolicyModeSticky, MaxActiveAccounts: intPtr(1)})
	other := groupPolicy(30, "kiro", ProxyIPPolicy{Mode: ProxyIPPolicyModeChurn})
	policies := []ProxyIPPolicy{base, plus, pro, other}

	p, err := policyFor(policies, "openai", []int64{10})
	require.NoError(t, err)
	require.Equal(t, ProxyIPPolicyModeChurn, p.Mode, "分组策略优先于平台策略")

	p, err = policyFor(policies, "openai", []int64{99})
	require.NoError(t, err)
	require.Nil(t, p.GroupID, "分组没配策略时回到平台策略")

	p, err = policyFor(policies, "openai", []int64{30})
	require.NoError(t, err)
	require.Nil(t, p.GroupID, "其他平台的分组策略不适用")

	_, err = policyFor(policies, "openai", []int64{10, 20})
	require.ErrorIs(t, err, ErrProxyIPPolicyGroupConflict, "多个分组策略不一致时拒绝")

	same := groupPolicy(40, "openai", ProxyIPPolicy{Mode: ProxyIPPolicyModeChurn, MaxLifetimeAccounts: intPtr(3)})
	p, err = policyFor(append(policies, same), "openai", []int64{10, 40})
	require.NoError(t, err, "规则一致的多个分组策略可以共存")
	require.Equal(t, int64(10), *p.GroupID)

	_, err = policyFor([]ProxyIPPolicy{plus}, "openai", nil)
	require.ErrorIs(t, err, ErrProxyIPPolicyMissing, "不在分组内且无平台策略")
	p, err = policyFor([]ProxyIPPolicy{plus}, "openai", []int64{10})
	require.NoError(t, err, "只有分组策略也能分配")
	require.Equal(t, int64(10), *p.GroupID)
}

func TestProxyPoolPick_GroupPolicy(t *testing.T) {
	pool := &ProxyPool{ID: 1}
	used := poolMember(1, StatusActive, map[string]int64{"openai": 3})
	fresh := poolMember(2, StatusActive, nil)
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{1: pool},
		policies: []ProxyIPPolicy{{ID: 1, Platform: "openai", Mode: ProxyIPPolicyModeSticky}},
		groupPolicies: []ProxyIPPolicy{
			groupPolicy(10, "openai", ProxyIPPolicy{Mode: ProxyIPPolicyModeChurn, MaxLifetimeAccounts: intPtr(3)}),
			groupPolicy(20, "openai", ProxyIPPolicy{Mode: ProxyIPPolicyModeSticky, PoolIDs: []int64{2}}),
		},
		members: []ProxyPoolMember{fresh, used},
	}
	svc := withProxyPoolService(t, repo)
	ctx := context.Background()

	// plus（churn，上限 3）：1 号已用满退役，只能拿新 IP。
	p, err := svc.pickProxy(ctx, pool, "openai", []int64{10}, 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), p.ID)

	// 平台策略（sticky）：优先被同平台 churn 分组用满的 1 号，把新 IP 留给消耗型分组。
	p, err = svc.pickProxy(ctx, pool, "openai", nil, 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID)

	// pro 分组策略只允许 2 号池。
	_, err = svc.pickProxy(ctx, pool, "openai", []int64{20}, 0, nil, false)
	require.ErrorIs(t, err, ErrProxyIPPolicyPoolDenied)
}

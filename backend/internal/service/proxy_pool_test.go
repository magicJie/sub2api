//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type proxyPoolRepoStub struct {
	pools         map[int64]*ProxyPool
	policies      []ProxyIPPolicy
	groupPolicies []ProxyIPPolicy
	members       []ProxyPoolMember
	membersByPool map[int64][]ProxyPoolMember
	memberReads   map[int64]int
	assignments   [][]int64
	locked        map[int64]bool
	lockErr       error
	lockLog       []int64
}

func (r *proxyPoolRepoStub) ListPools(context.Context) ([]ProxyPool, error) {
	pools := make([]ProxyPool, 0, len(r.pools))
	for _, pool := range r.pools {
		pools = append(pools, *pool)
	}
	return pools, nil
}
func (r *proxyPoolRepoStub) GetPool(_ context.Context, id int64) (*ProxyPool, error) {
	if p, ok := r.pools[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, ErrProxyPoolNotFound
}
func (r *proxyPoolRepoStub) CreatePool(context.Context, *ProxyPool) error { return nil }
func (r *proxyPoolRepoStub) UpdatePool(context.Context, *ProxyPool) error { return nil }
func (r *proxyPoolRepoStub) DeletePool(context.Context, int64) error      { return nil }
func (r *proxyPoolRepoStub) AssignProxies(_ context.Context, _ *ProxyPool, ids []int64) error {
	r.assignments = append(r.assignments, append([]int64(nil), ids...))
	return nil
}
func (r *proxyPoolRepoStub) ListMembers(_ context.Context, pool *ProxyPool, _ int64) ([]ProxyPoolMember, error) {
	if r.memberReads != nil {
		r.memberReads[pool.ID]++
	}
	if r.membersByPool != nil {
		return r.membersByPool[pool.ID], nil
	}
	return r.members, nil
}
func (r *proxyPoolRepoStub) ListBindingsByProxy(context.Context, int64) ([]ProxyAccountBinding, error) {
	return nil, nil
}
func (r *proxyPoolRepoStub) GetOpenBinding(context.Context, int64) (*ProxyAccountBinding, error) {
	return nil, ErrProxyPoolBindingNotFound
}
func (r *proxyPoolRepoStub) LockBinding(_ context.Context, accountID, _ int64) error {
	r.lockLog = append(r.lockLog, accountID)
	return r.lockErr
}
func (r *proxyPoolRepoStub) UnlockBinding(context.Context, int64) error { return nil }
func (r *proxyPoolRepoStub) ListPolicies(context.Context) ([]ProxyIPPolicy, error) {
	return r.policies, nil
}
func (r *proxyPoolRepoStub) UpsertPolicy(_ context.Context, p *ProxyIPPolicy) error {
	r.policies = append(r.policies, *p)
	return nil
}
func (r *proxyPoolRepoStub) DeletePolicy(context.Context, string) error { return nil }
func (r *proxyPoolRepoStub) ListGroupPolicies(context.Context) ([]ProxyIPPolicy, error) {
	return r.groupPolicies, nil
}
func (r *proxyPoolRepoStub) UpsertGroupPolicy(_ context.Context, p *ProxyIPPolicy) error {
	r.groupPolicies = append(r.groupPolicies, *p)
	return nil
}
func (r *proxyPoolRepoStub) DeleteGroupPolicy(context.Context, int64) error { return nil }
func (r *proxyPoolRepoStub) EgressHostTaken(_ context.Context, host string, exclude int64) (bool, error) {
	for _, m := range r.members {
		if m.Proxy.ID != exclude && m.Proxy.Host == host {
			return true, nil
		}
	}
	return false, nil
}
func (r *proxyPoolRepoStub) ListLockedAccountIDs(_ context.Context, ids []int64) ([]int64, error) {
	out := []int64{}
	for _, id := range ids {
		if r.locked[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func poolMember(id int64, status string, counts map[string]int64) ProxyPoolMember {
	if counts == nil {
		counts = map[string]int64{}
	}
	return ProxyPoolMember{
		Proxy:         Proxy{ID: id, Name: "p", Host: "203.0.113.1", Status: status},
		HistoryCounts: counts,
		ActiveCounts:  map[string]int64{},
	}
}

func churnPolicy(platform string, lifetime int) ProxyIPPolicy {
	return ProxyIPPolicy{Platform: platform, Mode: ProxyIPPolicyModeChurn, MaxLifetimeAccounts: intPtr(lifetime)}
}

func withProxyPoolService(t *testing.T, repo *proxyPoolRepoStub) *ProxyPoolService {
	t.Helper()
	svc := NewProxyPoolService(repo, nil)
	svc.latencyCache = poolQualityFixture(repo.members)
	prev := activeProxyPoolService.Swap(svc)
	t.Cleanup(func() { activeProxyPoolService.Store(prev) })
	return svc
}

func TestProxyPoolPick_LeastUsedSkipsRetiredAndInactive(t *testing.T) {
	pool := &ProxyPool{ID: 1}
	past := time.Now().Add(-time.Hour)
	expired := poolMember(4, StatusActive, nil)
	expired.Proxy.ExpiresAt = &past
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{1: pool},
		policies: []ProxyIPPolicy{churnPolicy("kiro", 5), {Platform: "openai", Mode: ProxyIPPolicyModeChurn}},
		members: []ProxyPoolMember{
			poolMember(1, StatusActive, map[string]int64{"kiro": 5}),              // 已退役
			poolMember(2, StatusActive, map[string]int64{"kiro": 3, "openai": 0}), // 可用
			poolMember(3, "inactive", nil),                                        // 停用
			expired,                                                               // 过期
			poolMember(5, StatusActive, map[string]int64{"kiro": 2, "openai": 9}), // 最少
		},
	}
	svc := withProxyPoolService(t, repo)

	p, err := svc.pickProxy(context.Background(), pool, &Account{Platform: "kiro"}, nil, 0, nil, false, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.NoError(t, err)
	require.Equal(t, int64(5), p.ID)

	// openai 策略不限上限：按 openai 计数最少选。
	p, err = svc.pickProxy(context.Background(), pool, &Account{Platform: "openai"}, nil, 0, nil, false, proxyQualityTargetForAccount(&Account{Platform: "openai"}))
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID, "id 1 has openai count 0 and smallest id")
}

func TestProxyPoolPick_AllRetired(t *testing.T) {
	pool := &ProxyPool{ID: 1}
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{1: pool},
		policies: []ProxyIPPolicy{churnPolicy("kiro", 1)},
		members:  []ProxyPoolMember{poolMember(1, StatusActive, map[string]int64{"kiro": 1})},
	}
	svc := withProxyPoolService(t, repo)
	_, err := svc.pickProxy(context.Background(), pool, &Account{Platform: "kiro"}, nil, 0, nil, false, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.ErrorIs(t, err, ErrProxyPoolNoAvailable)
}

func TestProxyPoolPick_StrictPreferred(t *testing.T) {
	pool := &ProxyPool{ID: 1}
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{1: pool},
		policies: []ProxyIPPolicy{churnPolicy("kiro", 2)},
		members: []ProxyPoolMember{
			poolMember(1, StatusActive, map[string]int64{"kiro": 0}),
			poolMember(2, StatusActive, map[string]int64{"kiro": 1}),
			poolMember(3, StatusActive, map[string]int64{"kiro": 2}),
		},
	}
	svc := withProxyPoolService(t, repo)
	two, three, missing := int64(2), int64(3), int64(99)

	p, err := svc.pickProxy(context.Background(), pool, &Account{Platform: "kiro"}, nil, 0, &two, true, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.NoError(t, err)
	require.Equal(t, int64(2), p.ID)

	_, err = svc.pickProxy(context.Background(), pool, &Account{Platform: "kiro"}, nil, 0, &three, true, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool, "retired proxy cannot be forced")
	_, err = svc.pickProxy(context.Background(), pool, &Account{Platform: "kiro"}, nil, 0, &missing, true, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)

	// 非严格：首选不可用时回退到最少用量。
	p, err = svc.pickProxy(context.Background(), pool, &Account{Platform: "kiro"}, nil, 0, &three, false, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID)
}

func TestBeginProxyPoolAccountCreate(t *testing.T) {
	pool := &ProxyPool{ID: 7}
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{7: pool},
		policies: []ProxyIPPolicy{churnPolicy("kiro", 5)},
		members:  []ProxyPoolMember{poolMember(11, StatusActive, nil)},
	}
	withProxyPoolService(t, repo)

	// 未指定代理池：不改动输入。
	manual := int64(3)
	input := &CreateAccountInput{Platform: "kiro", ProxyID: &manual}
	release, err := beginProxyPoolAccountCreate(context.Background(), input, nil)
	require.NoError(t, err)
	release()
	require.Equal(t, int64(3), *input.ProxyID)
	require.NoError(t, finishProxyPoolAccountCreate(context.Background(), input, &Account{ID: 1}))
	require.Empty(t, repo.lockLog)

	poolID := int64(7)
	input = &CreateAccountInput{Platform: "kiro", ProxyPoolID: &poolID}
	release, err = beginProxyPoolAccountCreate(context.Background(), input, nil)
	require.NoError(t, err)
	require.Equal(t, int64(11), *input.ProxyID)
	require.NoError(t, finishProxyPoolAccountCreate(context.Background(), input, &Account{ID: 42}))
	release()
	require.Equal(t, []int64{42}, repo.lockLog)

	// 预览代理不可用（不在池内）时回退到池内代理，而不是报错或沿用池外代理。
	outside := int64(99)
	input = &CreateAccountInput{Platform: "kiro", ProxyPoolID: &poolID, ProxyID: &outside}
	release, err = beginProxyPoolAccountCreate(context.Background(), input, nil)
	require.NoError(t, err)
	release()
	require.Equal(t, int64(11), *input.ProxyID)

	// 锁定失败要冒泡，避免账号拿到代理却没有被锁定。
	repo.lockErr = errors.New("boom")
	require.Error(t, finishProxyPoolAccountCreate(context.Background(), input, &Account{ID: 43}))

	missing := int64(8)
	_, err = beginProxyPoolAccountCreate(context.Background(), &CreateAccountInput{Platform: "kiro", ProxyPoolID: &missing}, nil)
	require.ErrorIs(t, err, ErrProxyPoolNotFound)
}

func TestCheckAccountProxyPoolLock(t *testing.T) {
	repo := &proxyPoolRepoStub{locked: map[int64]bool{1: true}}
	withProxyPoolService(t, repo)
	ctx := context.Background()
	cur := int64(10)
	same, other, clear := int64(10), int64(20), int64(0)
	locked := &Account{ID: 1, ProxyID: &cur}
	free := &Account{ID: 2, ProxyID: &cur}

	require.NoError(t, checkAccountProxyPoolLock(ctx, locked, nil))
	require.NoError(t, checkAccountProxyPoolLock(ctx, locked, &same), "unchanged proxy is allowed")
	require.ErrorIs(t, checkAccountProxyPoolLock(ctx, locked, &other), ErrAccountProxyLocked)
	require.ErrorIs(t, checkAccountProxyPoolLock(ctx, locked, &clear), ErrAccountProxyLocked)
	require.NoError(t, checkAccountProxyPoolLock(ctx, free, &other))

	require.NoError(t, checkAccountsProxyPoolLock(ctx, []*Account{locked, free}, &same))
	err := checkAccountsProxyPoolLock(ctx, []*Account{locked, free}, &other)
	require.Error(t, err)
	require.Contains(t, err.Error(), "[1]")
}

func TestNormalizeProxyPoolInput(t *testing.T) {
	p := &ProxyPool{Name: "  kiro-us "}
	require.NoError(t, normalizeProxyPoolInput(p))
	require.Equal(t, "kiro-us", p.Name)
	require.ErrorIs(t, normalizeProxyPoolInput(&ProxyPool{Name: " "}), ErrProxyPoolNameRequired)
}

func TestProxyPoolRetiredPlatforms(t *testing.T) {
	policies := []ProxyIPPolicy{churnPolicy("kiro", 5), churnPolicy("openai", 2), {Platform: "ollama", Mode: ProxyIPPolicyModeSticky}}
	m := poolMember(1, StatusActive, map[string]int64{"kiro": 5, "openai": 1, "ollama": 9})
	require.Equal(t, []string{"kiro"}, ProxyPoolRetiredPlatforms(policies, &m))
}

func TestProxyEgressHostUnique(t *testing.T) {
	ctx := context.Background()
	prevSvc := activeProxyPoolService.Load()
	t.Cleanup(func() { activeProxyPoolService.Store(prevSvc) })
	poolRepo := &proxyPoolRepoStub{members: []ProxyPoolMember{{Proxy: Proxy{ID: 5, Host: "1.2.3.4"}}}}
	activeProxyPoolService.Store(NewProxyPoolService(poolRepo, nil))

	// 新建同落地 IP 的代理：拒绝。
	require.ErrorIs(t, checkProxyEgressHost(ctx, &Proxy{Host: "1.2.3.4"}, nil), ErrProxyEgressHostExists)
	require.NoError(t, checkProxyEgressHost(ctx, &Proxy{Host: "5.6.7.8"}, nil))

	// 存量重复：host 不变的编辑不阻断；改成已占用的 host 则拒绝。
	prev := Proxy{ID: 6, Host: "1.2.3.4"}
	same := prev
	require.NoError(t, checkProxyEgressHost(ctx, &same, &prev))
	prev2 := Proxy{ID: 6, Host: "9.9.9.9"}
	moved := prev2
	moved.Host = "1.2.3.4"
	require.ErrorIs(t, checkProxyEgressHost(ctx, &moved, &prev2), ErrProxyEgressHostExists)
}

type poolQualityCache struct {
	infos  map[int64]*ProxyLatencyInfo
	err    error
	reads  int
	failAt int
}

func (c *poolQualityCache) GetProxyLatencies(_ context.Context, ids []int64) (map[int64]*ProxyLatencyInfo, error) {
	c.reads++
	if c.err != nil && (c.failAt == 0 || c.reads == c.failAt) {
		return nil, c.err
	}
	out := make(map[int64]*ProxyLatencyInfo, len(ids))
	for _, id := range ids {
		out[id] = c.infos[id]
	}
	return out, nil
}

func (c *poolQualityCache) SetProxyLatency(_ context.Context, id int64, info *ProxyLatencyInfo) error {
	c.infos[id] = info
	return nil
}

func poolQualityFixture(members []ProxyPoolMember) *poolQualityCache {
	cache := &poolQualityCache{infos: make(map[int64]*ProxyLatencyInfo, len(members))}
	for i := range members {
		proxy := &members[i].Proxy
		checked := time.Now().Unix()
		cache.infos[proxy.ID] = &ProxyLatencyInfo{
			Success: true, IPAddress: proxy.Host, QualityStatus: "healthy",
			QualityBaseEligible: true, QualityFingerprint: proxyQualityFingerprint(proxy),
			QualityAIPlatforms: []ProxyAIPlatformAssessment{
				{Target: "anthropic", Status: "reachable", Eligible: true},
				{Target: "openai", Status: "reachable", Eligible: true},
				{Target: "chatgpt", Status: "reachable", Eligible: true},
				{Target: "gemini", Status: "reachable", Eligible: true},
				{Target: "gemini_code_assist", Status: "reachable", Eligible: true},
				{Target: "grok", Status: "reachable", Eligible: true},
			},
			QualityCheckedAt:  &checked,
			QualityValidUntil: time.Now().Add(24 * time.Hour).Unix(),
		}
	}
	return cache
}

func poolQualityEvidenceCases() []struct {
	name       string
	change     func(*ProxyLatencyInfo)
	missing    bool
	noCache    bool
	cacheError bool
} {
	return []struct {
		name       string
		change     func(*ProxyLatencyInfo)
		missing    bool
		noCache    bool
		cacheError bool
	}{
		{name: "unknown reputation", change: func(info *ProxyLatencyInfo) {
			info.QualityBaseEligible, info.QualityStatus, info.QualityGrade = false, "warn", "Unknown"
		}},
		{name: "risky reputation", change: func(info *ProxyLatencyInfo) {
			info.QualityBaseEligible, info.QualityStatus = false, "failed"
		}},
		{name: "stale", change: func(info *ProxyLatencyInfo) {
			stale := time.Now().Add(-25 * time.Hour).Unix()
			info.QualityCheckedAt = &stale
		}},
		{name: "expired source evidence", change: func(info *ProxyLatencyInfo) {
			info.QualityValidUntil = time.Now().Add(-time.Minute).Unix()
		}},
		{name: "failed connectivity", change: func(info *ProxyLatencyInfo) {
			info.Success = false
		}},
		{name: "changed configuration", change: func(info *ProxyLatencyInfo) {
			info.QualityFingerprint = "outdated"
		}},
		{name: "changed exit IP", change: func(info *ProxyLatencyInfo) {
			info.IPAddress = "198.51.100.5"
		}},
		{name: "missing snapshot", missing: true},
		{name: "missing cache", noCache: true},
		{name: "cache read failure", cacheError: true},
		{name: "fresh low risk"},
	}
}

func TestProxyPoolQualityIsAdvisory(t *testing.T) {
	ctx := context.Background()
	for _, tc := range poolQualityEvidenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			pool := &ProxyPool{ID: 1}
			repo := &proxyPoolRepoStub{
				pools:    map[int64]*ProxyPool{1: pool},
				policies: []ProxyIPPolicy{churnPolicy("kiro", 5)},
				members:  []ProxyPoolMember{poolMember(1, StatusActive, nil)},
			}
			svc := withProxyPoolService(t, repo)
			cache := svc.latencyCache.(*poolQualityCache)
			if tc.change != nil {
				tc.change(cache.infos[1])
			}
			if tc.missing {
				delete(cache.infos, 1)
			}
			if tc.noCache {
				svc.latencyCache = nil
			}
			if tc.cacheError {
				cache.err = errors.New("cache offline")
			}
			check := func(proxy *Proxy, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, int64(1), proxy.ID)
			}
			proxy, err := svc.PreviewProxy(ctx, 1, &Account{Platform: "kiro"}, nil)
			check(proxy, err)
			id := int64(1)
			proxy, err = svc.pickProxy(ctx, pool, &Account{Platform: "kiro"}, nil, 0, &id, true, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
			check(proxy, err)
			proxy, err = svc.pickProxy(ctx, pool, &Account{Platform: "kiro"}, nil, 0, &id, false, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
			check(proxy, err)

			input := &CreateAccountInput{Platform: "kiro", ProxyPoolID: &pool.ID}
			release, err := beginProxyPoolAccountCreate(ctx, input, nil)
			require.NoError(t, err)
			release()
			require.Equal(t, int64(1), *input.ProxyID)

			accounts := &healthAccountRepo{accounts: map[int64]*Account{10: {ID: 10, Platform: "kiro"}}}
			svc.accountRepo = accounts
			proxy, err = svc.AssignAccount(ctx, 10, 1)
			check(proxy, err)
			require.Equal(t, &proxy.ID, accounts.accounts[10].ProxyID)
			require.Equal(t, []int64{10}, repo.lockLog)

			reads := cache.reads
			err = svc.AssignProxies(ctx, 1, []int64{1})
			require.NoError(t, err)
			require.Equal(t, [][]int64{{1}}, repo.assignments)
			require.Equal(t, reads, cache.reads)
		})
	}
}

func TestProxyPoolAutomaticPrefersRecommendedAndHonorsExplicitChoice(t *testing.T) {
	repo := &proxyPoolRepoStub{
		pools:    map[int64]*ProxyPool{1: {ID: 1}},
		policies: []ProxyIPPolicy{churnPolicy("kiro", 5)},
		members:  []ProxyPoolMember{poolMember(1, StatusActive, nil), poolMember(2, StatusActive, nil)},
	}
	svc := withProxyPoolService(t, repo)
	svc.latencyCache.(*poolQualityCache).infos[1].QualityBaseEligible = false
	proxy, err := svc.PreviewProxy(context.Background(), 1, &Account{Platform: "kiro"}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), proxy.ID)
	preferred := int64(1)
	proxy, err = svc.pickProxy(context.Background(), repo.pools[1], &Account{Platform: "kiro"}, nil, 0, &preferred, false, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.NoError(t, err)
	require.Equal(t, int64(1), proxy.ID)
	proxy, err = svc.pickProxy(context.Background(), repo.pools[1], &Account{Platform: "kiro"}, nil, 0, &preferred, true, proxyQualityTargetForAccount(&Account{Platform: "kiro"}))
	require.NoError(t, err)
	require.Equal(t, int64(1), proxy.ID)
	svc.latencyCache.(*poolQualityCache).infos[2].QualityBaseEligible = false
	proxy, err = svc.PreviewProxy(context.Background(), 1, &Account{Platform: "kiro"}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), proxy.ID)
}

func TestProxyPoolAssignProxiesValidatesEveryMemberAcrossPools(t *testing.T) {
	one, two := poolMember(1, StatusActive, nil), poolMember(2, StatusActive, nil)
	repo := &proxyPoolRepoStub{
		pools:         map[int64]*ProxyPool{1: {ID: 1, IsDefault: true}, 2: {ID: 2}},
		membersByPool: map[int64][]ProxyPoolMember{1: {one}, 2: {two}},
		memberReads:   map[int64]int{},
	}
	svc := withProxyPoolService(t, repo)
	cache := poolQualityFixture([]ProxyPoolMember{one, two})
	svc.latencyCache = cache
	cache.infos[2].QualityBaseEligible = false
	require.ErrorIs(t, svc.AssignProxies(context.Background(), 2, []int64{1, 99}), ErrProxyPoolProxyNotInPool)
	require.Empty(t, repo.assignments, "nonexistent IDs must fail the entire move")
	require.NoError(t, svc.AssignProxies(context.Background(), 2, []int64{1, 2}))
	require.Equal(t, [][]int64{{1, 2}}, repo.assignments)
}

func TestProxyPoolRecommendationsFollowAccountAccessPath(t *testing.T) {
	for _, tc := range []struct {
		name, platform, kind, oauthType string
		recommended                     bool
	}{
		{"Claude", "anthropic", "oauth", "", false},
		{"Gemini API", "gemini", "apikey", "", true},
		{"Gemini Studio", "gemini", "oauth", "ai_studio", true},
		{"Gemini Code Assist", "gemini", "oauth", "code_assist", false},
		{"OpenAI API", "openai", "apikey", "", true},
		{"ChatGPT", "openai", "oauth", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &proxyPoolRepoStub{
				pools:    map[int64]*ProxyPool{1: {ID: 1}},
				policies: []ProxyIPPolicy{churnPolicy(tc.platform, 5)},
				members:  []ProxyPoolMember{poolMember(8, StatusActive, nil), poolMember(9, StatusActive, nil)},
			}
			svc := withProxyPoolService(t, repo)
			cache := svc.latencyCache.(*poolQualityCache)
			cache.infos[8].QualityBaseEligible = false
			info := accountAdmissionService(nil, 9).proxyLatencyCache.(*accountAdmissionCache).info
			info.QualityFingerprint = proxyQualityFingerprint(&repo.members[1].Proxy)
			cache.infos[9] = info
			account := &Account{Platform: tc.platform, Type: tc.kind, Credentials: map[string]any{"oauth_type": tc.oauthType}}
			if tc.oauthType == "code_assist" {
				account.Credentials["project_id"] = "project"
			}
			proxy, err := svc.PreviewProxy(context.Background(), 1, account, nil)
			require.NoError(t, err)
			want := int64(8)
			if tc.recommended {
				want = 9
			}
			require.Equal(t, want, proxy.ID)
		})
	}
}

func TestGeminiProxyPreviewDistinguishesKnownProjectFromBootstrap(t *testing.T) {
	for _, tc := range []struct {
		name        string
		credentials map[string]any
		wantProxyID int64
	}{
		{"known project overrides Studio and relay", map[string]any{"oauth_type": "ai_studio", "project_id": " project ", "base_url": "https://relay.example/v1"}, 3},
		{"known empty Code Assist uses Gemini", map[string]any{"oauth_type": "code_assist", "project_id": ""}, 2},
		{"known whitespace project uses Gemini", map[string]any{"oauth_type": "google_one", "project_id": " \t "}, 2},
		{"known empty Google One respects relay", map[string]any{"oauth_type": "google_one", "project_id": "", "base_url": "https://relay.example/v1"}, 1},
		{"missing Code Assist project bootstraps", map[string]any{"oauth_type": "code_assist", "base_url": "https://relay.example/v1"}, 3},
		{"missing Google One project bootstraps", map[string]any{"oauth_type": "google_one"}, 3},
		{"missing Studio project uses Gemini", map[string]any{"oauth_type": "ai_studio"}, 2},
		{"missing legacy project uses Gemini", map[string]any{}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &proxyPoolRepoStub{
				pools:    map[int64]*ProxyPool{1: {ID: 1}},
				policies: []ProxyIPPolicy{churnPolicy("gemini", 5)},
				members: []ProxyPoolMember{
					poolMember(1, StatusActive, nil),
					poolMember(2, StatusActive, nil),
					poolMember(3, StatusActive, nil),
				},
			}
			svc := withProxyPoolService(t, repo)
			cache := svc.latencyCache.(*poolQualityCache)
			cache.infos[1].QualityAIPlatforms = nil
			cache.infos[2].QualityAIPlatforms = []ProxyAIPlatformAssessment{{Target: "gemini", Status: "reachable", Eligible: true}}
			cache.infos[3].QualityAIPlatforms = []ProxyAIPlatformAssessment{{Target: "gemini_code_assist", Status: "reachable", Eligible: true}}
			account := &Account{Platform: "gemini", Type: "oauth", Credentials: tc.credentials}
			proxy, err := svc.PreviewProxy(context.Background(), 1, account, nil)
			require.NoError(t, err)
			require.Equal(t, tc.wantProxyID, proxy.ID)
			if tc.wantProxyID != 1 {
				cache.infos[tc.wantProxyID].QualityAIPlatforms = nil
				proxy, err = svc.PreviewProxy(context.Background(), 1, account, nil)
				require.NoError(t, err)
				require.Equal(t, int64(1), proxy.ID)
			}
		})
	}
}

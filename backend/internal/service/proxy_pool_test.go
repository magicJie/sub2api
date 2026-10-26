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
	pools   map[int64]*ProxyPool
	members []ProxyPoolMember
	locked  map[int64]bool
	lockErr error
	lockLog []int64
}

func (r *proxyPoolRepoStub) ListPools(context.Context) ([]ProxyPool, error) { return nil, nil }
func (r *proxyPoolRepoStub) GetPool(_ context.Context, id int64) (*ProxyPool, error) {
	if p, ok := r.pools[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, ErrProxyPoolNotFound
}
func (r *proxyPoolRepoStub) CreatePool(context.Context, *ProxyPool) error             { return nil }
func (r *proxyPoolRepoStub) UpdatePool(context.Context, *ProxyPool) error             { return nil }
func (r *proxyPoolRepoStub) DeletePool(context.Context, int64) error                  { return nil }
func (r *proxyPoolRepoStub) AssignProxies(context.Context, *ProxyPool, []int64) error { return nil }
func (r *proxyPoolRepoStub) ListMembers(context.Context, *ProxyPool, int64) ([]ProxyPoolMember, error) {
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
		Proxy:         Proxy{ID: id, Name: "p", Status: status},
		HistoryCounts: counts,
		ActiveCounts:  map[string]int64{},
	}
}

func withProxyPoolService(t *testing.T, repo *proxyPoolRepoStub) *ProxyPoolService {
	t.Helper()
	svc := NewProxyPoolService(repo, nil)
	prev := activeProxyPoolService.Swap(svc)
	t.Cleanup(func() { activeProxyPoolService.Store(prev) })
	return svc
}

func TestProxyPoolPick_LeastUsedSkipsRetiredAndInactive(t *testing.T) {
	pool := &ProxyPool{ID: 1, PlatformLimits: map[string]int{"kiro": 5}}
	past := time.Now().Add(-time.Hour)
	expired := poolMember(4, StatusActive, nil)
	expired.Proxy.ExpiresAt = &past
	repo := &proxyPoolRepoStub{
		pools: map[int64]*ProxyPool{1: pool},
		members: []ProxyPoolMember{
			poolMember(1, StatusActive, map[string]int64{"kiro": 5}),              // 已退役
			poolMember(2, StatusActive, map[string]int64{"kiro": 3, "openai": 0}), // 可用
			poolMember(3, "inactive", nil),                                        // 停用
			expired,                                                               // 过期
			poolMember(5, StatusActive, map[string]int64{"kiro": 2, "openai": 9}), // 最少
		},
	}
	svc := withProxyPoolService(t, repo)

	p, err := svc.pickProxy(context.Background(), pool, "kiro", 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(5), p.ID)

	// openai 未配置上限：不受限，按 openai 计数最少选（2 的计数 0）。
	p, err = svc.pickProxy(context.Background(), pool, "openai", 0, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID, "id 1 has openai count 0 and smallest id")
}

func TestProxyPoolPick_AllRetired(t *testing.T) {
	pool := &ProxyPool{ID: 1, PlatformLimits: map[string]int{"kiro": 1}}
	repo := &proxyPoolRepoStub{
		pools:   map[int64]*ProxyPool{1: pool},
		members: []ProxyPoolMember{poolMember(1, StatusActive, map[string]int64{"kiro": 1})},
	}
	svc := withProxyPoolService(t, repo)
	_, err := svc.pickProxy(context.Background(), pool, "kiro", 0, nil, false)
	require.ErrorIs(t, err, ErrProxyPoolNoAvailable)
}

func TestProxyPoolPick_StrictPreferred(t *testing.T) {
	pool := &ProxyPool{ID: 1, PlatformLimits: map[string]int{"kiro": 2}}
	repo := &proxyPoolRepoStub{
		pools: map[int64]*ProxyPool{1: pool},
		members: []ProxyPoolMember{
			poolMember(1, StatusActive, map[string]int64{"kiro": 0}),
			poolMember(2, StatusActive, map[string]int64{"kiro": 1}),
			poolMember(3, StatusActive, map[string]int64{"kiro": 2}),
		},
	}
	svc := withProxyPoolService(t, repo)
	two, three, missing := int64(2), int64(3), int64(99)

	p, err := svc.pickProxy(context.Background(), pool, "kiro", 0, &two, true)
	require.NoError(t, err)
	require.Equal(t, int64(2), p.ID)

	_, err = svc.pickProxy(context.Background(), pool, "kiro", 0, &three, true)
	require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool, "retired proxy cannot be forced")
	_, err = svc.pickProxy(context.Background(), pool, "kiro", 0, &missing, true)
	require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)

	// 非严格：首选不可用时回退到最少用量。
	p, err = svc.pickProxy(context.Background(), pool, "kiro", 0, &three, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), p.ID)
}

func TestBeginProxyPoolAccountCreate(t *testing.T) {
	pool := &ProxyPool{ID: 7, PlatformLimits: map[string]int{"kiro": 5}}
	repo := &proxyPoolRepoStub{
		pools:   map[int64]*ProxyPool{7: pool},
		members: []ProxyPoolMember{poolMember(11, StatusActive, nil)},
	}
	withProxyPoolService(t, repo)

	// 未指定代理池：不改动输入。
	manual := int64(3)
	input := &CreateAccountInput{Platform: "kiro", ProxyID: &manual}
	release, err := beginProxyPoolAccountCreate(context.Background(), input)
	require.NoError(t, err)
	release()
	require.Equal(t, int64(3), *input.ProxyID)
	require.NoError(t, finishProxyPoolAccountCreate(context.Background(), input, &Account{ID: 1}))
	require.Empty(t, repo.lockLog)

	poolID := int64(7)
	input = &CreateAccountInput{Platform: "kiro", ProxyPoolID: &poolID}
	release, err = beginProxyPoolAccountCreate(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, int64(11), *input.ProxyID)
	require.NoError(t, finishProxyPoolAccountCreate(context.Background(), input, &Account{ID: 42}))
	release()
	require.Equal(t, []int64{42}, repo.lockLog)

	// 预览代理不可用（不在池内）时回退到池内代理，而不是报错或沿用池外代理。
	outside := int64(99)
	input = &CreateAccountInput{Platform: "kiro", ProxyPoolID: &poolID, ProxyID: &outside}
	release, err = beginProxyPoolAccountCreate(context.Background(), input)
	require.NoError(t, err)
	release()
	require.Equal(t, int64(11), *input.ProxyID)

	// 锁定失败要冒泡，避免账号拿到代理却没有被锁定。
	repo.lockErr = errors.New("boom")
	require.Error(t, finishProxyPoolAccountCreate(context.Background(), input, &Account{ID: 43}))

	missing := int64(8)
	_, err = beginProxyPoolAccountCreate(context.Background(), &CreateAccountInput{Platform: "kiro", ProxyPoolID: &missing})
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
	p := &ProxyPool{Name: "  kiro-us ", PlatformLimits: map[string]int{" kiro ": 5, "": 3}}
	require.NoError(t, normalizeProxyPoolInput(p))
	require.Equal(t, "kiro-us", p.Name)
	require.Equal(t, map[string]int{"kiro": 5}, p.PlatformLimits)

	require.ErrorIs(t, normalizeProxyPoolInput(&ProxyPool{Name: " "}), ErrProxyPoolNameRequired)
	require.ErrorIs(t, normalizeProxyPoolInput(&ProxyPool{Name: "x", PlatformLimits: map[string]int{"kiro": 0}}), ErrProxyPoolLimitInvalid)
}

func TestProxyPoolRetiredPlatforms(t *testing.T) {
	pool := &ProxyPool{PlatformLimits: map[string]int{"kiro": 5, "openai": 2}}
	m := poolMember(1, StatusActive, map[string]int64{"kiro": 5, "openai": 1})
	require.Equal(t, []string{"kiro"}, ProxyPoolRetiredPlatforms(pool, &m))
}

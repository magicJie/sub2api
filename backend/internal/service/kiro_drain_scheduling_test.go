//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestKiroSchedulingStrategyDefaultsToDrain(t *testing.T) {
	require.Equal(t, kiroSchedulingStrategyDrain, kiroSchedulingStrategy())
	t.Setenv("SUB2API_KIRO_SCHEDULING_STRATEGY", "pace")
	require.Equal(t, kiroSchedulingStrategyPace, kiroSchedulingStrategy())
	t.Setenv("SUB2API_KIRO_SCHEDULING_STRATEGY", "bogus")
	require.Equal(t, kiroSchedulingStrategyDrain, kiroSchedulingStrategy())
	t.Setenv("SUB2API_KIRO_PACE_SCHEDULING", "off")
	require.Equal(t, kiroSchedulingStrategyOff, kiroSchedulingStrategy())
}

func TestFilterByKiroDrainPrefersHigherUsageWithinSameReset(t *testing.T) {
	// 与 testenv 7/8/9 同形：98.4% / 98.3% / 37.3%，同一天重置。
	reset := 7 * 24 * time.Hour
	setKiroPace(t, 9111, 37.3, reset)
	setKiroPace(t, 9112, 98.3, reset)
	setKiroPace(t, 9113, 98.4, reset)
	got := filterKiroCandidates([]accountWithLoad{kiroPaceCandidate(9111), kiroPaceCandidate(9112), kiroPaceCandidate(9113)})
	require.Equal(t, []int64{9113}, ids(got))
}

func TestFilterByKiroDrainKeepsFullyUsedAccount(t *testing.T) {
	// 显示 100% 但未被上游拒绝：仍然是首选（上游可能还能接请求）。
	setKiroPace(t, 9121, 100, 5*24*time.Hour)
	setKiroPace(t, 9122, 50, 5*24*time.Hour)
	got := filterKiroCandidates([]accountWithLoad{kiroPaceCandidate(9122), kiroPaceCandidate(9121)})
	require.Equal(t, []int64{9121}, ids(got))
}

func TestFilterByKiroDrainUnknownAccountsGoLastButStay(t *testing.T) {
	setKiroPace(t, 9131, 5, 25*24*time.Hour)
	got := filterKiroCandidates([]accountWithLoad{kiroPaceCandidate(9132), kiroPaceCandidate(9131)})
	require.Equal(t, []int64{9131}, ids(got))

	// 只剩未知号（含快照过期、已过重置时间）时全部保留，交给负载率 / LRU。
	stale := time.Now().Add(5 * 24 * time.Hour)
	recordKiroPaceSnapshot(9133, &UsageInfo{
		KiroCredit:  &KiroCreditProgress{CurrentUsage: 4000, UsageLimit: 5000, PercentageUsed: 80},
		KiroResetAt: &stale,
	}, time.Now().Add(-7*time.Hour))
	t.Cleanup(func() { kiroPaceSnapshots.Delete(9133) })
	setKiroPace(t, 9134, 99, -time.Hour)
	got = filterKiroCandidates([]accountWithLoad{kiroPaceCandidate(9132), kiroPaceCandidate(9133), kiroPaceCandidate(9134)})
	require.ElementsMatch(t, []int64{9132, 9133, 9134}, ids(got))
}

func TestFilterByKiroDrainKeepsTiesForLoadBalancing(t *testing.T) {
	setKiroPace(t, 9141, 60, 3*24*time.Hour)
	setKiroPace(t, 9142, 60, 3*24*time.Hour)
	got := filterKiroCandidates([]accountWithLoad{kiroPaceCandidate(9141), kiroPaceCandidate(9142)})
	require.ElementsMatch(t, []int64{9141, 9142}, ids(got))
}

func TestFilterByKiroDrainIgnoresMixedPlatforms(t *testing.T) {
	setKiroPace(t, 9151, 90, time.Hour)
	other := accountWithLoad{account: &Account{ID: 9152, Platform: PlatformAnthropic}, loadInfo: &AccountLoadInfo{}}
	in := []accountWithLoad{kiroPaceCandidate(9151), other}
	require.Equal(t, ids(in), ids(filterKiroCandidates(in)))
}

func TestFilterKiroCandidatesPaceStrategyUsesPaceLogic(t *testing.T) {
	t.Setenv("SUB2API_KIRO_SCHEDULING_STRATEGY", "pace")
	setKiroPace(t, 9161, 8, 7*24*time.Hour)
	setKiroPace(t, 9162, 96, 7*24*time.Hour)
	got := filterKiroCandidates([]accountWithLoad{kiroPaceCandidate(9162), kiroPaceCandidate(9161)})
	require.Equal(t, []int64{9161}, ids(got), "pace 策略：剩余多的先用")
}

func kiroDrainSelectionService(accounts []Account, cache *mockConcurrencyCache) *GatewayService {
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	byID := make(map[int64]*Account, len(accounts))
	for i := range accounts {
		byID[accounts[i].ID] = &accounts[i]
	}
	return &GatewayService{
		accountRepo:         &mockAccountRepoForGemini{accounts: accounts, accountsByID: byID},
		concurrencyService:  NewConcurrencyService(cache),
		cfg:                 cfg,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
}

func kiroDrainSelectionAccounts(ids ...int64) []Account {
	out := make([]Account, 0, len(ids))
	for _, id := range ids {
		out = append(out, Account{ID: id, Platform: PlatformKiro, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1})
	}
	return out
}

func TestSelectAccountWithLoadAwarenessKiroDrainPicksMostSpent(t *testing.T) {
	setKiroPace(t, 9201, 98.4, 7*24*time.Hour)
	setKiroPace(t, 9202, 37.3, 7*24*time.Hour)
	svc := kiroDrainSelectionService(kiroDrainSelectionAccounts(9202, 9201), &mockConcurrencyCache{})
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "", nil, "", 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, int64(9201), result.Account.ID)
}

func TestSelectAccountWithLoadAwarenessKiroDrainOrdering(t *testing.T) {
	t.Setenv("SUB2API_KIRO_SCHEDULING_STRATEGY", "drain")
	t.Setenv("SUB2API_KIRO_PACE_SCHEDULING", "on")
	for _, tc := range []struct {
		name         string
		concurrency  [2]int
		loadRate     [2]int
		used         [2]float64
		resetIn      [2]time.Duration
		sessionEnd   [2]time.Duration
		unknownFirst bool
		sticky       bool
		want         int64
	}{
		{
			name:        "absolute concurrency precedes quota and load rate",
			concurrency: [2]int{1, 2}, loadRate: [2]int{50, 20},
			used: [2]float64{40, 99}, resetIn: [2]time.Duration{20 * 24 * time.Hour, 24 * time.Hour},
			want: 9251,
		},
		{
			name:        "idle unknown account precedes busy known account",
			concurrency: [2]int{0, 1}, loadRate: [2]int{0, 10},
			unknownFirst: true, used: [2]float64{0, 99},
			resetIn: [2]time.Duration{20 * 24 * time.Hour, 24 * time.Hour},
			want:    9251,
		},
		{
			name:        "quota precedes reset and load rate at equal concurrency",
			concurrency: [2]int{1, 1}, loadRate: [2]int{10, 50},
			used: [2]float64{40, 99}, resetIn: [2]time.Duration{24 * time.Hour, 20 * 24 * time.Hour},
			want: 9252,
		},
		{
			name:        "prefer soonest reset preserves concurrency and quota ordering",
			concurrency: [2]int{1, 1}, loadRate: [2]int{10, 50},
			used: [2]float64{40, 99}, resetIn: [2]time.Duration{24 * time.Hour, 20 * 24 * time.Hour},
			sessionEnd: [2]time.Duration{time.Hour, 2 * time.Hour},
			want:       9252,
		},
		{
			name:        "reset breaks equal concurrency and quota ties",
			concurrency: [2]int{1, 1}, loadRate: [2]int{10, 50},
			used: [2]float64{99, 99}, resetIn: [2]time.Duration{20 * 24 * time.Hour, 24 * time.Hour},
			want: 9252,
		},
		{
			name:        "sticky session precedes concurrency and quota",
			concurrency: [2]int{2, 0}, loadRate: [2]int{20, 0},
			used: [2]float64{40, 99}, resetIn: [2]time.Duration{20 * 24 * time.Hour, 24 * time.Hour},
			sticky: true, want: 9251,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := kiroDrainSelectionAccounts(9251, 9252)
			loadMap := make(map[int64]*AccountLoadInfo, len(accounts))
			for i := range accounts {
				account := &accounts[i]
				account.Concurrency = 10
				if i != 0 || !tc.unknownFirst {
					setKiroPace(t, account.ID, tc.used[i], tc.resetIn[i])
				}
				if tc.sessionEnd[i] > 0 {
					end := time.Now().Add(tc.sessionEnd[i])
					account.SessionWindowEnd = &end
				}
				loadMap[account.ID] = &AccountLoadInfo{
					AccountID: account.ID, CurrentConcurrency: tc.concurrency[i], LoadRate: tc.loadRate[i],
				}
			}
			svc := kiroDrainSelectionService(accounts, &mockConcurrencyCache{loadMap: loadMap})
			svc.cfg.Gateway.Scheduling.PreferSoonestReset = tc.sessionEnd[0] > 0
			session := ""
			if tc.sticky {
				session = "kiro-drain-ordering"
				svc.cache = &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{session: 9251}}
			}
			ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)
			result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, session, "", nil, "", 0)
			require.NoError(t, err)
			require.True(t, result.Acquired)
			require.Equal(t, tc.want, result.Account.ID)
			result.ReleaseFunc()
		})
	}
}

func TestSelectAccountWithLoadAwarenessKiroDrainOverflowsWhenPreferredBusy(t *testing.T) {
	setKiroPace(t, 9211, 98.4, 7*24*time.Hour)
	setKiroPace(t, 9212, 98.3, 7*24*time.Hour)
	setKiroPace(t, 9213, 37.3, 7*24*time.Hour)
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	// 首选号抢槽失败（并发打满）：立即落到排序里的下一个号，不排队等待。
	cache := &mockConcurrencyCache{acquireResults: map[int64]bool{9211: false}}
	svc := kiroDrainSelectionService(kiroDrainSelectionAccounts(9213, 9212, 9211), cache)
	result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "", nil, "", 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Nil(t, result.WaitPlan)
	require.Equal(t, int64(9212), result.Account.ID)

	// 首选号负载已满（LoadRate=100）：直接不进入候选，同样用下一个号。
	cache = &mockConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{9211: {AccountID: 9211, CurrentConcurrency: 1, LoadRate: 100}}}
	svc = kiroDrainSelectionService(kiroDrainSelectionAccounts(9213, 9212, 9211), cache)
	result, err = svc.SelectAccountWithLoadAwareness(ctx, nil, "", "", nil, "", 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, int64(9212), result.Account.ID)
}

func TestSelectAccountWithLoadAwarenessKiroDrainSkipsRateLimitedAccount(t *testing.T) {
	setKiroPace(t, 9221, 99.9, 7*24*time.Hour)
	setKiroPace(t, 9222, 40, 7*24*time.Hour)
	accounts := kiroDrainSelectionAccounts(9221, 9222)
	reset := time.Now().Add(7 * 24 * time.Hour)
	accounts[0].RateLimitResetAt = &reset // 已被上游拒绝
	svc := kiroDrainSelectionService(accounts, &mockConcurrencyCache{})
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(9222), result.Account.ID)
}

func TestKiroStickyAccountSpentReleasesOnlyAfterUpstreamRejection(t *testing.T) {
	setKiroPace(t, 9231, 99.5, 5*24*time.Hour)
	setKiroPace(t, 9232, 40, 5*24*time.Hour)
	sticky := &Account{ID: 9231, Platform: PlatformKiro, Status: StatusActive, Schedulable: true}
	other := Account{ID: 9232, Platform: PlatformKiro, Status: StatusActive, Schedulable: true}
	pool := []Account{*sticky, other}
	require.False(t, kiroStickyAccountSpent(sticky, pool), "drain：显示 99.5% 但未被拒绝时保持粘性")

	reset := time.Now().Add(time.Hour)
	sticky.RateLimitResetAt = &reset
	require.True(t, kiroStickyAccountSpent(sticky, pool), "限流中（上游拒绝）时放开粘性")

	expired := time.Now().Add(-time.Minute)
	sticky.RateLimitResetAt = &expired
	require.False(t, kiroStickyAccountSpent(sticky, pool), "冷却结束后恢复原有粘性")

	sticky.RateLimitResetAt = nil
	kiroQuotaRejections.Store(sticky.ID, time.Now().Add(time.Hour))
	t.Cleanup(func() { kiroQuotaRejections.Delete(sticky.ID) })
	require.True(t, kiroStickyAccountSpent(sticky, pool), "本实例刚标记的拒绝在调度快照同步前也生效")

	kiroQuotaRejections.Store(sticky.ID, time.Now().Add(-time.Minute))
	require.False(t, kiroStickyAccountSpent(sticky, pool), "已过 reset 后恢复粘性")
}

func TestKiroStickyAccountSpentPaceStrategyKeepsOldRule(t *testing.T) {
	t.Setenv("SUB2API_KIRO_SCHEDULING_STRATEGY", "pace")
	setKiroPace(t, 9241, 98.5, 5*24*time.Hour)
	setKiroPace(t, 9242, 40, 5*24*time.Hour)
	sticky := &Account{ID: 9241, Platform: PlatformKiro, Status: StatusActive, Schedulable: true}
	other := Account{ID: 9242, Platform: PlatformKiro, Status: StatusActive, Schedulable: true}
	require.True(t, kiroStickyAccountSpent(sticky, []Account{*sticky, other}))
	require.False(t, kiroStickyAccountSpent(sticky, []Account{*sticky}))
}

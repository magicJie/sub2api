//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// kiroUsageSnapshotRepoStub 在 stubOpenAIAccountRepo 之上实现 KiroUsageSnapshotRepository。
type kiroUsageSnapshotRepoStub struct {
	stubOpenAIAccountRepo
	mu       sync.Mutex
	writes   int
	lastUsed map[int64]time.Time
}

func newKiroUsageSnapshotRepoStub(accounts ...Account) *kiroUsageSnapshotRepoStub {
	return &kiroUsageSnapshotRepoStub{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: accounts}, lastUsed: map[int64]time.Time{}}
}

func (r *kiroUsageSnapshotRepoStub) ListKiroUsageRefreshCandidates(context.Context) ([]KiroUsageRefreshCandidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]KiroUsageRefreshCandidate, 0, len(r.accounts))
	for i := range r.accounts {
		a := &r.accounts[i]
		if !isKiroUsageRefreshEligible(a) {
			continue
		}
		candidate := KiroUsageRefreshCandidate{ID: a.ID, Snapshot: decodeKiroUsageSnapshot(a.Extra)}
		if ts, ok := r.lastUsed[a.ID]; ok {
			candidate.LastUsedAt = &ts
		}
		out = append(out, candidate)
	}
	return out, nil
}

func (r *kiroUsageSnapshotRepoStub) UpdateKiroUsageSnapshot(_ context.Context, id int64, snapshot *KiroUsageSnapshot, expected *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		a := &r.accounts[i]
		if a.ID != id {
			continue
		}
		current := decodeKiroUsageSnapshot(a.Extra)
		switch {
		case expected == nil && current != nil:
			return ErrKiroUsageSnapshotStale
		case expected != nil && (current == nil || !current.LastAttemptAt.Equal(*expected)):
			return ErrKiroUsageSnapshotStale
		}
		encoded, _ := json.Marshal(snapshot)
		var decoded map[string]any
		_ = json.Unmarshal(encoded, &decoded)
		extra := shallowCopyMap(a.Extra)
		if extra == nil {
			extra = map[string]any{}
		}
		extra[KiroUsageSnapshotExtraKey] = decoded
		a.Extra = extra
		r.writes++
		return nil
	}
	return ErrAccountNotFound
}

func (r *kiroUsageSnapshotRepoStub) RequestKiroUsageRefreshAll(_ context.Context, at time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for i := range r.accounts {
		a := &r.accounts[i]
		snapshot := decodeKiroUsageSnapshot(a.Extra)
		if !isKiroUsageRefreshEligible(a) || snapshot == nil {
			continue
		}
		requested := at.UTC()
		snapshot.RefreshRequestedAt = &requested
		extra := shallowCopyMap(a.Extra)
		extra[KiroUsageSnapshotExtraKey] = snapshot
		a.Extra = extra
		n++
	}
	return n, nil
}

func (r *kiroUsageSnapshotRepoStub) snapshotOf(id int64) *KiroUsageSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return decodeKiroUsageSnapshot(r.accounts[i].Extra)
		}
	}
	return nil
}

func kiroUsageTestAccount(id int64, extra map[string]any) Account {
	return Account{
		ID:       id,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
			"provider":     "Github",
			"auth_method":  "social",
		},
		Extra: extra,
	}
}

func kiroUsageTestSnapshot(lastAttempt time.Time, usedPercent float64) map[string]any {
	updatedAt := lastAttempt.UTC()
	return map[string]any{KiroUsageSnapshotExtraKey: &KiroUsageSnapshot{
		KiroCredit:    &KiroCreditProgress{CurrentUsage: usedPercent * 10, UsageLimit: 1000, PercentageUsed: usedPercent},
		UpdatedAt:     &updatedAt,
		LastAttemptAt: updatedAt,
	}}
}

func cleanupKiroPaceState(t *testing.T, ids ...int64) {
	t.Helper()
	t.Cleanup(func() {
		for _, id := range ids {
			kiroPaceSnapshots.Delete(id)
			kiroPaceRefreshAttempts.Delete(id)
		}
	})
}

func newKiroUsageRefresherForTest(repo AccountRepository, usage *AccountUsageService, concurrency int) *KiroUsageRefreshService {
	svc := NewKiroUsageRefreshService(repo, usage, nil)
	settings := defaultKiroUsageRefreshSettings()
	settings.Concurrency = concurrency
	svc.applySettings(settings)
	return svc
}

func TestKiroUsageRefreshInterval_FastForHotOrActiveAccounts(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	base := 15 * time.Minute
	cold := &KiroUsageSnapshot{KiroCredit: &KiroCreditProgress{UsageLimit: 1000, PercentageUsed: 40}}
	hot := &KiroUsageSnapshot{KiroCredit: &KiroCreditProgress{UsageLimit: 1000, PercentageUsed: 90}}
	recent := now.Add(-10 * time.Minute)
	stale := now.Add(-31 * time.Minute)

	require.Equal(t, base, kiroUsageRefreshInterval(cold, nil, now, base))
	require.Equal(t, base, kiroUsageRefreshInterval(cold, &stale, now, base))
	require.Equal(t, 5*time.Minute, kiroUsageRefreshInterval(hot, nil, now, base))
	require.Equal(t, 5*time.Minute, kiroUsageRefreshInterval(cold, &recent, now, base))
	// 基础间隔比加速间隔还短时不会被"加速"拉长。
	require.Equal(t, 5*time.Minute, kiroUsageRefreshInterval(hot, nil, now, 5*time.Minute))
}

func TestKiroUsageRefreshBackoff_ExponentialCappedAtOneHour(t *testing.T) {
	require.Equal(t, time.Duration(0), kiroUsageRefreshBackoff(0))
	require.Equal(t, time.Minute, kiroUsageRefreshBackoff(1))
	require.Equal(t, 2*time.Minute, kiroUsageRefreshBackoff(2))
	require.Equal(t, 4*time.Minute, kiroUsageRefreshBackoff(3))
	require.Equal(t, 32*time.Minute, kiroUsageRefreshBackoff(6))
	require.Equal(t, time.Hour, kiroUsageRefreshBackoff(7))
	require.Equal(t, time.Hour, kiroUsageRefreshBackoff(50))
}

func TestKiroUsageRefreshJitter_DeterministicAndBounded(t *testing.T) {
	attempt := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	interval := 15 * time.Minute
	seen := map[time.Duration]struct{}{}
	for id := int64(1); id <= 50; id++ {
		got := kiroUsageRefreshJitter(id, attempt, interval)
		require.Equal(t, got, kiroUsageRefreshJitter(id, attempt, interval), "same account+attempt must be stable")
		require.GreaterOrEqual(t, got, interval-90*time.Second)
		require.LessOrEqual(t, got, interval+90*time.Second)
		seen[got] = struct{}{}
	}
	require.Greater(t, len(seen), 10, "accounts should be spread out")
}

func TestKiroUsageNextRefreshAt(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	base := 15 * time.Minute
	require.True(t, kiroUsageNextRefreshAt(1, nil, nil, now, base).IsZero(), "no snapshot → refresh now")

	ok := &KiroUsageSnapshot{LastAttemptAt: now.Add(-time.Minute), KiroCredit: &KiroCreditProgress{UsageLimit: 1, PercentageUsed: 10}}
	next := kiroUsageNextRefreshAt(1, ok, nil, now, base)
	require.True(t, next.After(now.Add(10*time.Minute)))

	requested := now.Add(-time.Second)
	queued := *ok
	queued.RefreshRequestedAt = &requested
	require.True(t, kiroUsageNextRefreshAt(1, &queued, nil, now, base).IsZero(), "refresh-all request wins")

	failed := &KiroUsageSnapshot{LastAttemptAt: now, FailureCount: 3}
	require.Equal(t, now.Add(4*time.Minute), kiroUsageNextRefreshAt(1, failed, nil, now, base))
}

func TestKiroUsageRefresher_RunDueRespectsConcurrencyLimit(t *testing.T) {
	ids := []int64{91001, 91002, 91003, 91004, 91005, 91006}
	cleanupKiroPaceState(t, ids...)
	accounts := make([]Account, 0, len(ids))
	for _, id := range ids {
		accounts = append(accounts, kiroUsageTestAccount(id, nil))
	}
	repo := newKiroUsageSnapshotRepoStub(accounts...)
	svc := newKiroUsageRefresherForTest(repo, nil, 2)

	var active, peak, calls atomic.Int32
	svc.refreshFn = func(ctx context.Context, account *Account) error {
		calls.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		return nil
	}

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int32(len(ids)), calls.Load())
	require.Equal(t, int32(2), peak.Load())
}

func TestKiroUsageRefresher_OnlyDueAccountsAreRefreshed(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cleanupKiroPaceState(t, 92001, 92002, 92003, 92004, 92005)
	freshCold := kiroUsageTestAccount(92001, kiroUsageTestSnapshot(now.Add(-6*time.Minute), 30))
	freshHot := kiroUsageTestAccount(92002, kiroUsageTestSnapshot(now.Add(-6*time.Minute), 95))
	stale := kiroUsageTestAccount(92003, kiroUsageTestSnapshot(now.Add(-20*time.Minute), 30))
	activeCold := kiroUsageTestAccount(92004, kiroUsageTestSnapshot(now.Add(-6*time.Minute), 30))
	disabled := kiroUsageTestAccount(92005, nil)
	disabled.Status = StatusDisabled
	repo := newKiroUsageSnapshotRepoStub(freshCold, freshHot, stale, activeCold, disabled)
	repo.lastUsed[92004] = now.Add(-2 * time.Minute)

	svc := newKiroUsageRefresherForTest(repo, nil, 2)
	svc.now = func() time.Time { return now }
	var mu sync.Mutex
	refreshed := map[int64]bool{}
	svc.refreshFn = func(_ context.Context, account *Account) error {
		mu.Lock()
		refreshed[account.ID] = true
		mu.Unlock()
		return nil
	}

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, map[int64]bool{92002: true, 92003: true, 92004: true}, refreshed)
}

func TestKiroUsageRefresher_NonLeaderDoesNotFetchButRestoresPace(t *testing.T) {
	now := time.Now()
	cleanupKiroPaceState(t, 93001, 93002)
	repo := newKiroUsageSnapshotRepoStub(
		kiroUsageTestAccount(93001, kiroUsageTestSnapshot(now.Add(-time.Hour), 42)),
		kiroUsageTestAccount(93002, nil),
	)
	svc := newKiroUsageRefresherForTest(repo, nil, 2)
	lock := &fakeLeaderLockCache{}
	acquired, err := lock.TryAcquireLeaderLock(context.Background(), kiroUsageRefreshLeaderLockKey, "peer", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	svc.lockCache = lock
	var calls atomic.Int32
	svc.refreshFn = func(context.Context, *Account) error { calls.Add(1); return nil }

	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, calls.Load(), "non-leader must not hit upstream")

	used, _, known := kiroPaceInfo(93001, now)
	require.True(t, known, "every instance restores pace from the persisted snapshot")
	require.InDelta(t, 42, used, 0.001)

	// 锁释放后本实例成为 leader，开始刷新。
	require.NoError(t, lock.ReleaseLeaderLock(context.Background(), kiroUsageRefreshLeaderLockKey, "peer"))
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int32(2), calls.Load())
}

func TestKiroUsageRefresher_DisabledSkipsFetch(t *testing.T) {
	cleanupKiroPaceState(t, 94001)
	repo := newKiroUsageSnapshotRepoStub(kiroUsageTestAccount(94001, nil))
	svc := newKiroUsageRefresherForTest(repo, nil, 2)
	svc.enabled.Store(false)
	var calls atomic.Int32
	svc.refreshFn = func(context.Context, *Account) error { calls.Add(1); return nil }
	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, calls.Load())
}

func TestKiroUsageRefresher_FailureBacksOffAndKeepsLastGoodData(t *testing.T) {
	cleanupKiroPaceState(t, 95001)
	var hits atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, `{"message":"temporary failure"}`, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"usageBreakdownList":[{"resourceType":"CREDIT","currentUsageWithPrecision":300,"usageLimitWithPrecision":1000}]}`))
	}))
	defer server.Close()
	prevResolver := resolveKiroRuntimeEndpoint
	resolveKiroRuntimeEndpoint = func(string) string { return server.URL }
	defer func() { resolveKiroRuntimeEndpoint = prevResolver }()

	repo := newKiroUsageSnapshotRepoStub(kiroUsageTestAccount(95001, nil))
	usage := NewAccountUsageService(repo, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
	svc := newKiroUsageRefresherForTest(repo, usage, 2)
	clock := time.Now()
	svc.now = func() time.Time { return clock }

	require.NoError(t, svc.RunDue(context.Background()))
	first := repo.snapshotOf(95001)
	require.NotNil(t, first)
	require.True(t, first.hasData())
	require.InDelta(t, 30, first.KiroCredit.PercentageUsed, 0.001)
	require.Zero(t, first.FailureCount)

	// 让快照到期后上游开始失败：保留上次成功的数据，记录错误与失败次数。
	fail.Store(true)
	clock = clock.Add(20 * time.Minute)
	svc.attempts = sync.Map{}
	require.NoError(t, svc.RunDue(context.Background()))
	second := repo.snapshotOf(95001)
	require.Equal(t, 1, second.FailureCount)
	require.NotEmpty(t, second.LastError)
	require.NotNil(t, second.KiroCredit)
	require.InDelta(t, 30, second.KiroCredit.PercentageUsed, 0.001)
	require.Equal(t, first.UpdatedAt.Unix(), second.UpdatedAt.Unix())
	hitsAfterFailure := hits.Load()

	// 退避期内（第 1 次失败 → 1 分钟）不重试。
	clock = clock.Add(30 * time.Second)
	svc.attempts = sync.Map{}
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, hitsAfterFailure, hits.Load())

	// 退避期过后重试，成功后清零失败计数。
	fail.Store(false)
	clock = clock.Add(time.Minute)
	svc.attempts = sync.Map{}
	require.NoError(t, svc.RunDue(context.Background()))
	third := repo.snapshotOf(95001)
	require.Zero(t, third.FailureCount)
	require.Empty(t, third.LastError)
}

func TestKiroUsageBatch_ReadsSnapshotWithoutHittingUpstream(t *testing.T) {
	now := time.Now()
	cleanupKiroPaceState(t, 96001, 96002)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"usageBreakdownList":[{"resourceType":"CREDIT","currentUsageWithPrecision":1,"usageLimitWithPrecision":1000}]}`))
	}))
	defer server.Close()
	prevResolver := resolveKiroRuntimeEndpoint
	resolveKiroRuntimeEndpoint = func(string) string { return server.URL }
	defer func() { resolveKiroRuntimeEndpoint = prevResolver }()

	repo := newKiroUsageSnapshotRepoStub(
		kiroUsageTestAccount(96001, kiroUsageTestSnapshot(now.Add(-7*time.Minute), 55)),
		kiroUsageTestAccount(96002, nil),
	)
	usage := NewAccountUsageService(repo, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
	svc := newKiroUsageRefresherForTest(repo, usage, 2)

	// force=true 也不打上游：批量接口只读快照。
	result, errs, err := usage.GetUsageBatch(context.Background(), []int64{96001, 96002}, true)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Zero(t, hits.Load())

	withSnapshot := result[96001]
	require.NotNil(t, withSnapshot)
	require.NotNil(t, withSnapshot.KiroCredit)
	require.InDelta(t, 55, withSnapshot.KiroCredit.PercentageUsed, 0.001)
	require.NotNil(t, withSnapshot.UpdatedAt)
	require.False(t, withSnapshot.KiroUsagePending)

	missing := result[96002]
	require.NotNil(t, missing)
	require.Nil(t, missing.KiroCredit)
	require.True(t, missing.KiroUsagePending, "no snapshot → empty result, queued for background refresh")
	select {
	case <-svc.wake:
	default:
		t.Fatal("missing snapshot should wake the background refresher")
	}

	// 单号 force=true 仍同步刷新并落库。
	fresh, err := usage.GetUsage(context.Background(), 96002, true)
	require.NoError(t, err)
	require.Equal(t, int32(1), hits.Load())
	require.NotNil(t, fresh.KiroCredit)
	persisted := repo.snapshotOf(96002)
	require.NotNil(t, persisted)
	require.True(t, persisted.hasData())

	// 再次普通读取走快照，不再打上游。
	again, err := usage.GetUsage(context.Background(), 96002)
	require.NoError(t, err)
	require.Equal(t, int32(1), hits.Load())
	require.NotNil(t, again.KiroCredit)
}

func TestKiroUsageRefreshAll_QueuesWithoutFetching(t *testing.T) {
	now := time.Now()
	cleanupKiroPaceState(t, 97001, 97002)
	repo := newKiroUsageSnapshotRepoStub(
		kiroUsageTestAccount(97001, kiroUsageTestSnapshot(now.Add(-time.Minute), 10)),
		kiroUsageTestAccount(97002, nil),
	)
	svc := newKiroUsageRefresherForTest(repo, nil, 2)
	svc.now = func() time.Time { return now }
	var calls atomic.Int32
	svc.refreshFn = func(context.Context, *Account) error { calls.Add(1); return nil }

	queued, err := svc.RequestRefreshAll(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), queued)
	require.Zero(t, calls.Load(), "refresh-all only queues")
	require.True(t, repo.snapshotOf(97001).refreshRequested())

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int32(2), calls.Load(), "queued fresh snapshot is refreshed on the next cycle")
}

func TestBuildKiroUsageSnapshotAndUsageInfo(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	reset := now.Add(72 * time.Hour)
	ok := buildKiroUsageSnapshot(nil, &UsageInfo{KiroUsageInfo: KiroUsageInfo{
		KiroSubscriptionName: "KIRO PRO+",
		KiroResetAt:          &reset,
		KiroCredit:           &KiroCreditProgress{CurrentUsage: 100, UsageLimit: 1000, PercentageUsed: 10, WindowStats: &WindowStats{Requests: 3}},
		KiroQuotaState:       kiroQuotaStateNormal,
	}}, now)
	require.NotNil(t, ok.UpdatedAt)
	require.Nil(t, ok.KiroCredit.WindowStats, "window stats are derived on read, not persisted")

	encoded, err := json.Marshal(map[string]any{KiroUsageSnapshotExtraKey: ok})
	require.NoError(t, err)
	var extra map[string]any
	require.NoError(t, json.Unmarshal(encoded, &extra))
	decoded := decodeKiroUsageSnapshot(extra)
	require.NotNil(t, decoded)
	require.Equal(t, "KIRO PRO+", decoded.KiroSubscriptionName)
	require.True(t, decoded.UpdatedAt.Equal(now))

	info := decoded.usageInfo("active")
	require.Equal(t, "active", info.Source)
	require.False(t, info.KiroUsagePending)
	require.Empty(t, info.Error)
	require.True(t, info.UpdatedAt.Equal(now))

	failed := buildKiroUsageSnapshot(decoded, &UsageInfo{Error: "usage API error: boom", ErrorCode: errorCodeNetworkError}, now.Add(time.Minute))
	require.Equal(t, 1, failed.FailureCount)
	require.Equal(t, "usage API error: boom", failed.LastError)
	require.True(t, failed.UpdatedAt.Equal(now), "failure keeps last successful time")
	failedInfo := failed.usageInfo("active")
	require.Equal(t, "usage API error: boom", failedInfo.Error)
	require.NotNil(t, failedInfo.KiroCredit)

	require.True(t, (*KiroUsageSnapshot)(nil).usageInfo("active").KiroUsagePending)
	require.Nil(t, decodeKiroUsageSnapshot(map[string]any{KiroUsageSnapshotExtraKey: "not-json"}))
}

func TestKiroInvalidateUsageSnapshotOnIdentityChange(t *testing.T) {
	cleanupKiroPaceState(t, 98001)
	account := kiroUsageTestAccount(98001, kiroUsageTestSnapshot(time.Now(), 20))
	account.Credentials["refresh_token"] = "rt-1"
	previous := kiroUsageIdentity(&account)
	kiroPaceSnapshots.Store(int64(98001), kiroPaceSnapshot{usedPercent: 20, updatedAt: time.Now()})

	// 只换 access_token（后台 token 刷新）不算换号。
	account.Credentials["access_token"] = "new-access"
	kiroInvalidateUsageSnapshotOnIdentityChange(&account, previous)
	require.Contains(t, account.Extra, KiroUsageSnapshotExtraKey)

	account.Credentials["refresh_token"] = "rt-2"
	kiroInvalidateUsageSnapshotOnIdentityChange(&account, previous)
	require.NotContains(t, account.Extra, KiroUsageSnapshotExtraKey)
	_, ok := kiroPaceSnapshots.Load(int64(98001))
	require.False(t, ok)
}

func TestKiroUsageRefreshSettings_DefaultsAndValidation(t *testing.T) {
	repo := &upstreamBillingProbeSettingRepo{}
	settings := NewSettingService(repo, nil)
	got, err := settings.GetKiroUsageRefreshSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, &KiroUsageRefreshSettings{Enabled: true, IntervalMinutes: 15, Concurrency: 2}, got)

	require.Error(t, settings.SetKiroUsageRefreshSettings(context.Background(), &KiroUsageRefreshSettings{Enabled: true, IntervalMinutes: 1, Concurrency: 2}))
	require.Error(t, settings.SetKiroUsageRefreshSettings(context.Background(), &KiroUsageRefreshSettings{Enabled: true, IntervalMinutes: 15, Concurrency: 0}))

	svc := NewKiroUsageRefreshService(nil, nil, settings)
	updated, err := svc.UpdateSettings(context.Background(), &KiroUsageRefreshSettings{Enabled: false, IntervalMinutes: 30, Concurrency: 3})
	require.NoError(t, err)
	require.Equal(t, &KiroUsageRefreshSettings{Enabled: false, IntervalMinutes: 30, Concurrency: 3}, updated)
	require.False(t, svc.Enabled())
	require.Equal(t, int32(3), svc.concurrency.Load())
	require.Equal(t, int64(30*time.Minute), svc.interval.Load())
}

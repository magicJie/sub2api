package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type ollamaUsageTestRepo struct {
	*upstreamBillingProbeAccountRepo
	due                 []Account
	beforeSnapshot      func()
	disableAutoAttempts atomic.Int64
	disableAutoCalls    atomic.Int64
	groupResolveCalls   atomic.Int64
	getByIDCalls        atomic.Int64
}

// GetByID counts loads so a test can wait for a caller to reach the point just
// before the singleflight group, instead of guessing with a sleep.
func (r *ollamaUsageTestRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.getByIDCalls.Add(1)
	return r.upstreamBillingProbeAccountRepo.GetByID(ctx, id)
}

func (r *ollamaUsageTestRepo) ListOllamaCloudUsageGroupAccounts(_ context.Context, anchors []*Account) ([]Account, error) {
	r.groupResolveCalls.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	wanted := make(map[string]struct{}, len(anchors))
	for _, anchor := range anchors {
		if fingerprint, ok := ollamaCloudUsageGroupFingerprint(anchor); ok {
			wanted[fingerprint] = struct{}{}
		}
	}
	result := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		fingerprint, ok := ollamaCloudUsageGroupFingerprint(account)
		if _, match := wanted[fingerprint]; !ok || !match {
			continue
		}
		result = append(result, cloneOllamaUsageTestAccount(*account))
	}
	return result, nil
}

// cloneOllamaUsageTestAccount 深拷贝共享 map，模拟真实仓储每次查询返回全新行：
// 组写在 r.mu 下改成员 map，浅拷贝会让 RunDue 过滤循环无锁读到同一 map 而竞争。
func cloneOllamaUsageTestAccount(account Account) Account {
	account.Credentials = mergeMap(nil, account.Credentials)
	account.Extra = mergeMap(nil, account.Extra)
	return account
}

func (r *ollamaUsageTestRepo) SetOllamaCloudUsageAutoRefresh(_ context.Context, expected *Account, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil || !r.ollamaExpectedStateExistsLocked(members, expected) {
		return ErrOllamaCloudUsageIdentityChanged
	}
	for _, account := range members {
		applyOllamaUsageTestManagedExtra(account, expected)
		account.Extra[OllamaCloudUsageAutoRefreshExtraKey] = enabled
	}
	return nil
}

func (r *ollamaUsageTestRepo) UpdateOllamaCloudUsageSnapshot(_ context.Context, expected *Account, snapshot *OllamaCloudUsageSnapshot) error {
	if r.beforeSnapshot != nil {
		r.beforeSnapshot()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil || !r.ollamaExpectedStateExistsLocked(members, expected) {
		return ErrOllamaCloudUsageIdentityChanged
	}
	for _, account := range members {
		applyOllamaUsageTestManagedExtra(account, expected)
		account.Extra[OllamaCloudUsageSnapshotExtraKey] = snapshot
	}
	return nil
}

func (r *ollamaUsageTestRepo) DisableOllamaCloudUsageAutoRefresh(_ context.Context, expected *Account) error {
	r.disableAutoAttempts.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil || !r.ollamaExpectedStateExistsLocked(members, expected) {
		return ErrOllamaCloudUsageIdentityChanged
	}
	for _, account := range members {
		applyOllamaUsageTestManagedExtra(account, expected)
		account.Extra[OllamaCloudUsageAutoRefreshExtraKey] = false
		delete(account.Extra, OllamaCloudUsageSnapshotExtraKey)
	}
	r.disableAutoCalls.Add(1)
	return nil
}

func (r *ollamaUsageTestRepo) ollamaGroupMembersLocked(expected *Account) ([]*Account, error) {
	anchor := r.accounts[expected.ID]
	if !sameOllamaUsageTestIdentity(anchor, expected) {
		return nil, ErrOllamaCloudUsageIdentityChanged
	}
	fingerprint, ok := ollamaCloudUsageGroupFingerprint(expected)
	if !ok {
		return nil, ErrOllamaCloudUsageAccountInvalid
	}
	members := make([]*Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		candidate, valid := ollamaCloudUsageGroupFingerprint(account)
		if valid && candidate == fingerprint {
			if account.Extra == nil {
				account.Extra = make(map[string]any)
			}
			members = append(members, account)
		}
	}
	return members, nil
}

func (r *ollamaUsageTestRepo) ollamaExpectedStateExistsLocked(members []*Account, expected *Account) bool {
	for _, member := range members {
		if reflect.DeepEqual(member.Extra[OllamaCloudUsageAutoRefreshExtraKey], expected.Extra[OllamaCloudUsageAutoRefreshExtraKey]) &&
			reflect.DeepEqual(member.Extra[OllamaCloudUsageSnapshotExtraKey], expected.Extra[OllamaCloudUsageSnapshotExtraKey]) {
			return true
		}
	}
	return false
}

func applyOllamaUsageTestManagedExtra(account, source *Account) {
	delete(account.Extra, OllamaCloudUsageSessionExtraKey)
	delete(account.Extra, "ollama_cloud_usage_snapshot")
	for _, key := range []string{OllamaCloudUsageAutoRefreshExtraKey, OllamaCloudUsageSnapshotExtraKey} {
		delete(account.Extra, key)
		if value, ok := source.Extra[key]; ok {
			account.Extra[key] = value
		}
	}
}

func (r *ollamaUsageTestRepo) ListDueOllamaCloudUsageAccounts(_ context.Context, _ time.Time, _, _ time.Duration, limit int) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.due) > 0 {
		out := make([]Account, 0, min(limit, len(r.due)))
		for _, account := range r.due[:min(limit, len(r.due))] {
			out = append(out, cloneOllamaUsageTestAccount(account))
		}
		return out, nil
	}
	out := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		out = append(out, cloneOllamaUsageTestAccount(*account))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type ollamaRefreshPreflightIdentityChangeRepo struct {
	*ollamaUsageTestRepo
	getCalls atomic.Int64
}

func (r *ollamaRefreshPreflightIdentityChangeRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	if r.getCalls.Add(1) == 2 {
		r.mu.Lock()
		r.accounts[id].Credentials["api_key"] = "rotated-before-refresh"
		r.mu.Unlock()
	}
	return r.upstreamBillingProbeAccountRepo.GetByID(ctx, id)
}

type ollamaManagedExtraUpdateRepo struct {
	AccountRepository
	account *Account
	updated *Account
}

func (r *ollamaManagedExtraUpdateRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func (r *ollamaManagedExtraUpdateRepo) Update(_ context.Context, account *Account) error {
	r.updated = account
	return nil
}

func sameOllamaUsageTestIdentity(left, right *Account) bool {
	return left != nil && right != nil && left.Platform == right.Platform && left.Type == right.Type &&
		reflect.DeepEqual(left.Credentials, right.Credentials) && reflect.DeepEqual(left.ProxyID, right.ProxyID)
}

type ollamaUsageHTTPStub struct {
	status           int
	body             []byte
	usageBody        []byte
	responseOverride func(*http.Request) (*http.Response, error)
	usageStatus      int
	usageCalls       atomic.Int64
	usageRequest     *http.Request
	header           http.Header
	profileStatus    int
	profileBody      []byte
	profileCalls     atomic.Int64
	profileRequest   *http.Request
	calls            atomic.Int64
	active           atomic.Int64
	maxActive        atomic.Int64
	beforeResponse   func(*http.Request)
	lastRequest      *http.Request
	lastProxyURL     string
	mu               sync.Mutex
}

func (s *ollamaUsageHTTPStub) Do(req *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	if s.responseOverride != nil {
		return s.responseOverride(req)
	}
	if req.URL.Path == "/api/me" {
		s.profileCalls.Add(1)
		s.mu.Lock()
		s.profileRequest = req
		s.mu.Unlock()
		status := s.profileStatus
		if status == 0 {
			status = http.StatusUnauthorized
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(s.profileBody))), Request: req}, nil
	}
	if req.URL.Path == "/api/usage" {
		s.usageCalls.Add(1)
		s.mu.Lock()
		s.usageRequest = req
		s.mu.Unlock()
		body := s.usageBody
		if body == nil {
			body = []byte(`{"range":"7d","scope":"self","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{"request_count":0}}`)
		}
		status := s.usageStatus
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Header: s.header, Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
	}
	s.calls.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		peak := s.maxActive.Load()
		if active <= peak || s.maxActive.CompareAndSwap(peak, active) {
			break
		}
	}
	s.mu.Lock()
	s.lastRequest = req
	s.lastProxyURL = proxyURL
	s.mu.Unlock()
	if s.beforeResponse != nil {
		s.beforeResponse(req)
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	header := s.header
	if header == nil {
		header = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(string(s.body))), Request: req}, nil
}

func (s *ollamaUsageHTTPStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, concurrency)
}

func ollamaUsageAccount(id int64) *Account {
	return &Account{
		ID: id, Name: fmt.Sprintf("ollama-%d", id), Platform: PlatformOllama, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": fmt.Sprintf("key-%d", id)},
		Extra:       map[string]any{}, Status: StatusActive, Schedulable: true, Concurrency: 1,
	}
}

func newOllamaUsageTestService(t *testing.T, repo *ollamaUsageTestRepo, upstream HTTPUpstream, settingsRepo SettingRepository) *OllamaCloudUsageService {
	t.Helper()
	svc := NewOllamaCloudUsageService(repo, upstream, NewSettingService(settingsRepo, nil))
	t.Cleanup(svc.Stop)
	return svc
}

func ollamaUsageFixture(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"included":{"balance_usd":50,"allowance_usd":100,"period":{"from":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z"}},"purchased":{"balance_usd":25}}`)
}

func TestOllamaCloudUsageSettingsDefaultOffAndValidation(t *testing.T) {
	repo := &upstreamBillingProbeSettingRepo{}
	settingsService := NewSettingService(repo, nil)
	settings, err := settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 60, settings.IntervalMinutes)
	require.Equal(t, 1, settings.DebounceMinutes)

	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 14, DebounceMinutes: 1})
	require.Error(t, err)
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 90, DebounceMinutes: 61})
	require.Error(t, err)
	// DebounceMinutes=0 (legacy omit) defaults to 1 on write.
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 90, DebounceMinutes: 0})
	require.NoError(t, err)
	settings, err = settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, settings.DebounceMinutes)
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 90, DebounceMinutes: 2})
	require.NoError(t, err)
	settings, err = settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 90, settings.IntervalMinutes)
	require.Equal(t, 2, settings.DebounceMinutes)

	// debounce >= interval would make the debounce term unreachable in
	// min(lastUsed+debounce, fetchedAt+maxWait), silently ignoring the operator's
	// setting, so it is rejected rather than accepted and dropped.
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 15, DebounceMinutes: 15})
	require.Error(t, err, "debounce equal to interval must be rejected")
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 15, DebounceMinutes: 60})
	require.Error(t, err, "debounce greater than interval must be rejected")
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 16, DebounceMinutes: 15})
	require.NoError(t, err, "debounce below interval stays valid")

	// Legacy JSON without debounce_minutes defaults to 1.
	repo.values[SettingKeyOllamaCloudUsageSettings] = `{"enabled":true,"interval_minutes":45}`
	settings, err = settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 45, settings.IntervalMinutes)
	require.Equal(t, 1, settings.DebounceMinutes)
}

func TestOllamaCloudUsageIsAutoRefreshDue(t *testing.T) {
	debounce := time.Minute
	maxWait := time.Hour
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	fetched := now.Add(-30 * time.Minute)
	ptr := func(ts time.Time) *time.Time { return &ts }

	require.True(t, ollamaCloudUsageIsAutoRefreshDue(nil, nil, now, debounce, maxWait), "missing snapshot first due")
	require.True(t, ollamaCloudUsageIsAutoRefreshDue(&OllamaCloudUsageSnapshot{Status: "bogus"}, nil, now, debounce, maxWait), "invalid status first due")

	okSnap := &OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusOK, FetchedAt: ptr(fetched),
		LastAttemptAt: fetched, NextRefreshAt: fetched.Add(maxWait),
	}
	require.False(t, ollamaCloudUsageIsAutoRefreshDue(okSnap, nil, now, debounce, maxWait), "no request after success")
	require.False(t, ollamaCloudUsageIsAutoRefreshDue(okSnap, ptr(fetched), now, debounce, maxWait), "request not after fetched_at")
	require.False(t, ollamaCloudUsageIsAutoRefreshDue(okSnap, ptr(now.Add(-30*time.Second)), now, debounce, maxWait), "debounce not elapsed")
	require.True(t, ollamaCloudUsageIsAutoRefreshDue(okSnap, ptr(now.Add(-time.Minute)), now, debounce, maxWait), "single request quiet for debounce")

	// Continuous requests: last used is now, but max-wait from old fetch forces due.
	oldFetched := now.Add(-2 * time.Hour)
	oldSnap := &OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusOK, FetchedAt: ptr(oldFetched),
		LastAttemptAt: oldFetched, NextRefreshAt: oldFetched.Add(maxWait),
	}
	require.True(t, ollamaCloudUsageIsAutoRefreshDue(oldSnap, ptr(now), now, debounce, maxWait), "max-wait forces due while requests continue")
	// First request after a very old snapshot is immediately due because fetched+maxWait is past.
	require.True(t, ollamaCloudUsageIsAutoRefreshDue(oldSnap, ptr(now.Add(-time.Second)), now, debounce, maxWait), "stale snapshot first request immediate")

	failSnap := &OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusFailed, FetchedAt: ptr(fetched),
		LastAttemptAt: now.Add(-10 * time.Minute), NextRefreshAt: now.Add(20 * time.Minute),
	}
	require.False(t, ollamaCloudUsageIsAutoRefreshDue(failSnap, nil, now, debounce, maxWait), "failure without new request")
	require.False(t, ollamaCloudUsageIsAutoRefreshDue(failSnap, ptr(now.Add(-time.Minute)), now, debounce, maxWait), "failure blocked by backoff")
	failSnap.NextRefreshAt = now.Add(-time.Second)
	require.True(t, ollamaCloudUsageIsAutoRefreshDue(failSnap, ptr(now.Add(-time.Minute)), now, debounce, maxWait), "failure after backoff with new request")

	require.True(t, ollamaCloudUsageIsAutoRefreshDue(&OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusOK, LastAttemptAt: now,
	}, nil, now, debounce, maxWait), "ok without fetched_at fails open")
}

// The success path stopped consulting next_refresh_at, which is where
// nextOllamaCloudUsageDelay used to apply the minimum interval. Activity may pull
// a refresh forward only as far as that floor, otherwise request traffic spaced
// just wider than the debounce drives the group's outbound rate far above the
// pre-existing minimum.
func TestOllamaCloudUsageAutoRefreshDueAtHonoursMinFetchInterval(t *testing.T) {
	debounce := time.Minute
	maxWait := time.Hour
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	ptr := func(ts time.Time) *time.Time { return &ts }

	// Debounce elapsed, but the last successful fetch is inside the floor.
	recent := now.Add(-5 * time.Minute)
	recentSnap := &OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusOK, FetchedAt: ptr(recent), LastAttemptAt: recent,
	}
	dueAt, ok := ollamaCloudUsageAutoRefreshDueAt(recentSnap, ptr(now.Add(-2*time.Minute)), debounce, maxWait)
	require.True(t, ok)
	require.Equal(t, recent.Add(OllamaCloudUsageMinFetchInterval), dueAt,
		"due time must be clamped to fetched_at + min fetch interval")
	require.False(t, ollamaCloudUsageIsAutoRefreshDue(recentSnap, ptr(now.Add(-2*time.Minute)), now, debounce, maxWait),
		"debounce alone must not refresh within the min fetch interval")

	// Once the floor has passed the debounce governs again.
	atFloor := now.Add(-OllamaCloudUsageMinFetchInterval)
	floorSnap := &OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusOK, FetchedAt: ptr(atFloor), LastAttemptAt: atFloor,
	}
	require.True(t, ollamaCloudUsageIsAutoRefreshDue(floorSnap, ptr(now.Add(-2*time.Minute)), now, debounce, maxWait),
		"past the floor a quiet debounce window is due")

	// The floor never delays a refresh that max-wait has already forced.
	stale := now.Add(-2 * time.Hour)
	staleSnap := &OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusOK, FetchedAt: ptr(stale), LastAttemptAt: stale,
	}
	require.True(t, ollamaCloudUsageIsAutoRefreshDue(staleSnap, ptr(now), now, debounce, maxWait),
		"max-wait still forces due on a stale snapshot")
}

func TestScheduleOllamaCloudUsageActivityOnlyForOllama(t *testing.T) {
	deferred := NewDeferredService(nil, nil, time.Second)
	ollama := ollamaUsageAccount(1)
	other := ollamaUsageAccount(2)
	other.Platform = PlatformOpenAI

	scheduleOllamaCloudUsageActivity(deferred, ollama)
	scheduleOllamaCloudUsageActivity(deferred, other)
	scheduleOllamaCloudUsageActivity(nil, ollama)

	_, ok := deferred.lastUsedUpdates.Load(int64(1))
	require.True(t, ok)
	_, ok = deferred.lastUsedUpdates.Load(int64(2))
	require.False(t, ok)
}

func TestIsOllamaCloudUsageAccountUsesPlatform(t *testing.T) {
	require.False(t, IsOllamaCloudUsageAccount(nil))
	for _, baseURL := range []string{"", "https://ollama.com/v1", "https://relay.example.com"} {
		account := ollamaUsageAccount(1)
		account.Credentials["base_url"] = baseURL
		require.True(t, IsOllamaCloudUsageAccount(account))
	}
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformGrok} {
		account := ollamaUsageAccount(1)
		account.Platform = platform
		require.False(t, IsOllamaCloudUsageAccount(account), platform)
	}
}

// Ollama usage supports API key credentials only.
func TestIsOllamaCloudUsageAccountRejectsOAuthType(t *testing.T) {
	for _, platform := range []string{PlatformOllama, PlatformOpenAI, PlatformAnthropic} {
		account := ollamaUsageAccount(1)
		account.Platform = platform
		account.Type = AccountTypeOAuth
		require.False(t, IsOllamaCloudUsageAccount(account), platform)
	}
}

func TestOllamaCloudUsageManagedExtraCannotBeImported(t *testing.T) {
	remoteExtra := map[string]any{
		OllamaCloudUsageSessionExtraKey:     "remote-ciphertext",
		OllamaCloudUsageAutoRefreshExtraKey: true,
		OllamaCloudUsageSnapshotExtraKey:    map[string]any{"status": "forged"},
		"ollama_cloud_usage_snapshot":       map[string]any{"status": "ok"},
	}
	created, err := buildAccountForCreate(&CreateAccountInput{
		Name: "ollama", Platform: PlatformOllama, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": "key"},
		Concurrency: 1,
	}, mergeMap(nil, remoteExtra))
	require.NoError(t, err)
	require.NotContains(t, created.Extra, OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, created.Extra, OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, created.Extra, OllamaCloudUsageSnapshotExtraKey)
	require.NotContains(t, created.Extra, "ollama_cloud_usage_snapshot")

	existing := ollamaUsageAccount(6)
	existing.Extra = map[string]any{
		OllamaCloudUsageSessionExtraKey:     "local-ciphertext",
		OllamaCloudUsageAutoRefreshExtraKey: false,
		OllamaCloudUsageSnapshotExtraKey:    map[string]any{"status": OllamaCloudUsageStatusOK},
	}
	targetExtra := mergeMap(existing.Extra, remoteExtra)
	reconcileCRSUpstreamBillingProbeExtra(existing, existing.Platform, existing.Type, mergeMap(existing.Credentials, nil), targetExtra)
	require.NotContains(t, targetExtra, OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, targetExtra, "ollama_cloud_usage_snapshot")
	require.Equal(t, false, targetExtra[OllamaCloudUsageAutoRefreshExtraKey])
	require.Equal(t, map[string]any{"status": OllamaCloudUsageStatusOK}, targetExtra[OllamaCloudUsageSnapshotExtraKey])

	changedCredentials := mergeMap(existing.Credentials, map[string]any{"api_key": "rotated"})
	targetExtra = mergeMap(existing.Extra, remoteExtra)
	reconcileCRSUpstreamBillingProbeExtra(existing, existing.Platform, existing.Type, changedCredentials, targetExtra)
	require.NotContains(t, targetExtra, OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, targetExtra, OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, targetExtra, OllamaCloudUsageSnapshotExtraKey)
}

func TestAccountServiceUpdateStripsOllamaManagedExtra(t *testing.T) {
	account := ollamaUsageAccount(61)
	account.Extra = map[string]any{
		OllamaCloudUsageSessionExtraKey:     "local-ciphertext",
		OllamaCloudUsageAutoRefreshExtraKey: true,
		OllamaCloudUsageSnapshotExtraKey:    map[string]any{"status": OllamaCloudUsageStatusOK},
	}
	repo := &ollamaManagedExtraUpdateRepo{account: account}
	svc := NewAccountService(repo, nil)
	requestedExtra := map[string]any{
		"note":                              "preserved",
		OllamaCloudUsageSessionExtraKey:     "forged-ciphertext",
		OllamaCloudUsageAutoRefreshExtraKey: nil,
		OllamaCloudUsageSnapshotExtraKey:    nil,
	}

	_, err := svc.Update(context.Background(), account.ID, UpdateAccountRequest{Extra: &requestedExtra})
	require.NoError(t, err)
	require.Equal(t, "preserved", repo.updated.Extra["note"])
	require.NotContains(t, repo.updated.Extra, OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, repo.updated.Extra, OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, repo.updated.Extra, OllamaCloudUsageSnapshotExtraKey)
	// The request map is not mutated while managed fields are stripped.
	require.Contains(t, requestedExtra, OllamaCloudUsageSessionExtraKey)
}

func TestOllamaCloudUsageGroupSharesAcrossURLVariantsAndNewSiblings(t *testing.T) {
	now := time.Date(2026, time.November, 9, 3, 0, 0, 0, time.UTC)
	source := ollamaUsageAccount(71)
	source.Credentials["api_key"] = "shared-key"

	source.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	source.Extra[OllamaCloudUsageSnapshotExtraKey] = &OllamaCloudUsageSnapshot{
		Status:        OllamaCloudUsageStatusOK,
		Data:          &OllamaCloudUsageData{Balance: "$25"},
		LastAttemptAt: now.Add(-time.Hour),
	}
	source.UpdatedAt = now.Add(-time.Minute)
	sibling := ollamaUsageAccount(72)
	sibling.Credentials = map[string]any{"base_url": "https://ollama.com/v1", "api_key": "shared-key"}

	sibling.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	sibling.UpdatedAt = now
	different := ollamaUsageAccount(73)
	different.Credentials["api_key"] = "different-key"
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		source.ID: source, sibling.ID: sibling, different.ID: different,
	}}}
	svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}, &upstreamBillingProbeSettingRepo{})
	svc.now = func() time.Time { return now }

	state, err := svc.GetState(context.Background(), sibling.ID)
	require.NoError(t, err)
	require.True(t, state.Configured)
	require.True(t, state.AutoRefreshEnabled)
	require.Equal(t, "$25", state.Snapshot.Data.Balance)

	differentState, err := svc.GetState(context.Background(), different.ID)
	require.NoError(t, err)
	require.True(t, differentState.Configured)

	newSibling := ollamaUsageAccount(74)
	newSibling.Credentials = map[string]any{"base_url": "https://ollama.com:443", "api_key": "shared-key"}
	newSibling.UpdatedAt = now.Add(time.Minute)
	repo.mu.Lock()
	repo.accounts[newSibling.ID] = newSibling
	repo.mu.Unlock()
	newState, err := svc.GetState(context.Background(), newSibling.ID)
	require.NoError(t, err)
	require.True(t, newState.Configured)
	require.True(t, newState.AutoRefreshEnabled)
	require.Equal(t, state.Snapshot, newState.Snapshot)

	state, err = svc.SetAutoRefresh(context.Background(), newSibling.ID, false)
	require.NoError(t, err)
	require.False(t, state.AutoRefreshEnabled)
	for _, account := range []*Account{source, sibling, newSibling} {
		require.Equal(t, false, account.Extra[OllamaCloudUsageAutoRefreshExtraKey])
	}
	state, err = svc.Refresh(context.Background(), newSibling.ID)
	require.NoError(t, err)
	require.Equal(t, OllamaCloudUsageStatusOK, state.Snapshot.Status)

	before := repo.groupResolveCalls.Load()
	require.NoError(t, svc.ResolveAccounts(context.Background(), []*Account{source, sibling, different, newSibling}))
	require.Equal(t, before+1, repo.groupResolveCalls.Load(), "one list batch must issue one group lookup")
}

func TestOllamaCloudUsageAutoRefreshIsGroupScoped(t *testing.T) {
	first := ollamaUsageAccount(81)
	first.Credentials["api_key"] = "shared-key"
	second := ollamaUsageAccount(82)
	second.Credentials = map[string]any{"base_url": "https://ollama.com/v1", "api_key": "shared-key"}
	different := ollamaUsageAccount(83)
	different.Credentials["api_key"] = "different-key"
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		first.ID: first, second.ID: second, different.ID: different,
	}}}
	svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{}, &upstreamBillingProbeSettingRepo{})

	state, err := svc.SetAutoRefresh(context.Background(), first.ID, true)
	require.NoError(t, err)
	require.True(t, state.AutoRefreshEnabled)
	require.Equal(t, true, first.Extra[OllamaCloudUsageAutoRefreshExtraKey])
	require.Equal(t, true, second.Extra[OllamaCloudUsageAutoRefreshExtraKey])

	state, err = svc.SetAutoRefresh(context.Background(), second.ID, false)
	require.NoError(t, err)
	require.True(t, state.Configured)
	require.False(t, state.AutoRefreshEnabled)
	require.Equal(t, false, first.Extra[OllamaCloudUsageAutoRefreshExtraKey])
	require.Equal(t, false, second.Extra[OllamaCloudUsageAutoRefreshExtraKey])
	require.NotContains(t, different.Extra, OllamaCloudUsageAutoRefreshExtraKey)
}

func TestOllamaCloudUsageRefreshSingleflightAndRunnerDeduplicateSharedGroup(t *testing.T) {
	first := ollamaUsageAccount(91)
	first.Credentials["api_key"] = "shared-key"

	first.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	second := ollamaUsageAccount(92)
	second.Credentials = map[string]any{"base_url": "https://ollama.com:443/v1", "api_key": "shared-key"}

	second.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	repo := &ollamaUsageTestRepo{
		upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{first.ID: first, second.ID: second}},
		due:                             []Account{*first, *second},
	}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t), beforeResponse: func(*http.Request) {
		once.Do(func() { close(started) })
		<-release
	}}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo)

	errs := make(chan error, 2)
	go func() { _, err := svc.Refresh(context.Background(), first.ID); errs <- err }()
	<-started
	// The first caller is now parked in the stub, having loaded the account twice
	// (once to build the group key, once inside the singleflight function).
	loadsBeforeSecond := repo.getByIDCalls.Load()
	go func() { _, err := svc.Refresh(context.Background(), second.ID); errs <- err }()
	// Only release the first caller once the second one has loaded its own
	// account, which happens immediately before it joins the singleflight group.
	// Releasing right after starting the goroutine raced: if the first refresh
	// finished first, the second became a fresh singleflight execution, re-read
	// the account, saw the LastAttemptAt just written, and failed with the 30s
	// manual-refresh 429 instead of sharing the in-flight result.
	require.Eventually(t, func() bool {
		return repo.getByIDCalls.Load() > loadsBeforeSecond
	}, 5*time.Second, time.Millisecond, "the second caller must reach the singleflight group before the first is released")
	close(release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Equal(t, int64(1), upstream.calls.Load())
	require.NotNil(t, decodeOllamaCloudUsageSnapshot(first.Extra))
	require.Equal(t, decodeOllamaCloudUsageSnapshot(first.Extra), decodeOllamaCloudUsageSnapshot(second.Extra))

	delete(first.Extra, OllamaCloudUsageSnapshotExtraKey)
	delete(second.Extra, OllamaCloudUsageSnapshotExtraKey)
	upstream.beforeResponse = nil
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(2), upstream.calls.Load(), "RunDue must issue one request for the shared group")
}

func TestOllamaCloudUsageRefreshRejectsGroupChangeBeforeUpstreamRequest(t *testing.T) {
	account := ollamaUsageAccount(94)

	base := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
	repo := &ollamaRefreshPreflightIdentityChangeRepo{ollamaUsageTestRepo: base}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := NewOllamaCloudUsageService(repo, upstream, NewSettingService(&upstreamBillingProbeSettingRepo{}, nil))
	t.Cleanup(svc.Stop)

	_, err := svc.Refresh(context.Background(), account.ID)

	require.ErrorIs(t, err, ErrOllamaCloudUsageIdentityChanged)
	require.Zero(t, upstream.calls.Load())
	require.NotContains(t, account.Extra, OllamaCloudUsageSnapshotExtraKey)
}

func TestOllamaCloudUsageRefreshUsesFixedURLsBearerAndNoRedirects(t *testing.T) {
	account := ollamaUsageAccount(8)

	account.Extra[OllamaCloudUsageSessionExtraKey] = "legacy-cookie-must-not-be-sent"
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{8: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, &upstreamBillingProbeSettingRepo{})
	fixedNow := time.Date(2026, time.July, 22, 15, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	state, err := svc.Refresh(context.Background(), 8)
	require.NoError(t, err)
	require.Equal(t, OllamaCloudUsageStatusOK, state.Snapshot.Status)
	require.Equal(t, ollamaCloudUsageBalanceURL, upstream.lastRequest.URL.String())
	require.Equal(t, "ollama.com", upstream.lastRequest.Host)
	require.Empty(t, upstream.lastRequest.Header.Get("Cookie"))
	require.Equal(t, "Bearer key-8", upstream.lastRequest.Header.Get("Authorization"))
	require.Equal(t, ollamaCloudUsageHistoryURL, upstream.usageRequest.URL.String())
	require.Equal(t, http.MethodGet, upstream.lastRequest.Method)
	require.Equal(t, http.MethodGet, upstream.usageRequest.Method)
	require.Equal(t, "Bearer key-8", upstream.usageRequest.Header.Get("Authorization"))
	require.Empty(t, upstream.usageRequest.Header.Get("Cookie"))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.usageRequest.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastRequest.Context()))
}

func TestOllamaCloudUsageRefreshFetchesEmailForAPIKey(t *testing.T) {
	account := ollamaUsageAccount(88)

	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{88: account}}}
	upstream := &ollamaUsageHTTPStub{
		body:          ollamaUsageFixture(t),
		profileStatus: http.StatusOK,
		profileBody:   []byte(`{"email":"key-owner@example.com","name":"Owner"}`),
	}
	svc := newOllamaUsageTestService(t, repo, upstream, &upstreamBillingProbeSettingRepo{})

	state, err := svc.Refresh(context.Background(), 88)
	require.NoError(t, err)
	require.Equal(t, "key-owner@example.com", state.Snapshot.Data.Email)
	require.Equal(t, int64(1), upstream.profileCalls.Load())
	require.Equal(t, http.MethodPost, upstream.profileRequest.Method)
	require.Equal(t, ollamaCloudUsageProfileURL, upstream.profileRequest.URL.String())
	require.Equal(t, "Bearer key-88", upstream.profileRequest.Header.Get("Authorization"))
	require.Empty(t, upstream.profileRequest.Header.Get("Cookie"))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.profileRequest.Context()))

	// An unavailable profile endpoint must not invalidate a successful usage fetch.
	other := ollamaUsageAccount(89)

	otherRepo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{89: other}}}
	otherUpstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	otherSvc := newOllamaUsageTestService(t, otherRepo, otherUpstream, &upstreamBillingProbeSettingRepo{})
	state, err = otherSvc.Refresh(context.Background(), 89)
	require.NoError(t, err)
	require.Empty(t, state.Snapshot.Data.Email)
}

func TestOllamaCloudUsageManualRefreshUsesShortIndependentInterval(t *testing.T) {
	account := ollamaUsageAccount(12)

	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{12: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, &upstreamBillingProbeSettingRepo{})
	fixedNow := time.Date(2026, time.July, 22, 15, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	_, err := svc.Refresh(context.Background(), 12)
	require.NoError(t, err)
	_, err = svc.Refresh(context.Background(), 12)
	require.ErrorIs(t, err, ErrOllamaCloudUsageRefreshRateLimited)
	require.Equal(t, int64(1), upstream.calls.Load())

	fixedNow = fixedNow.Add(ollamaCloudUsageManualRefreshInterval)
	_, err = svc.Refresh(context.Background(), 12)
	require.NoError(t, err)
	require.Equal(t, int64(2), upstream.calls.Load())
}

func TestOllamaCloudUsageRefreshUsesHydratedProxyIdentity(t *testing.T) {
	account := ollamaUsageAccount(13)

	proxyID := int64(4)
	account.ProxyID = &proxyID
	account.Proxy = &Proxy{
		ID: proxyID, Protocol: "http", Host: "127.0.0.1", Port: 3128,
		Username: "proxy-user", Password: "proxy-pass", Status: StatusActive,
	}
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{13: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, &upstreamBillingProbeSettingRepo{})

	_, err := svc.Refresh(context.Background(), 13)
	require.NoError(t, err)
	require.Equal(t, account.Proxy.URL(), upstream.lastProxyURL)
}

func TestOllamaCloudUsageRedirectAndBodyLimitArePersistedSafely(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   []byte
		reason string
	}{
		{"redirect", http.StatusFound, nil, "redirect_blocked"},
		{"body limit", http.StatusOK, make([]byte, ollamaCloudUsageMaxBodyBytes+1), "response_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := ollamaUsageAccount(9)

			repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{9: account}}}
			svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{status: test.status, body: test.body}, &upstreamBillingProbeSettingRepo{})
			state, err := svc.Refresh(context.Background(), 9)
			require.NoError(t, err)
			require.Equal(t, OllamaCloudUsageStatusFailed, state.Snapshot.Status)
			require.Equal(t, test.reason, state.Snapshot.LastError)
		})
	}
}

func TestOllamaCloudUsageRefreshRejectsIdentityChange(t *testing.T) {
	account := ollamaUsageAccount(10)

	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{10: account}}}
	repo.beforeSnapshot = func() { account.Credentials["api_key"] = "rotated" }
	svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}, &upstreamBillingProbeSettingRepo{})
	_, err := svc.Refresh(context.Background(), 10)
	require.ErrorIs(t, err, ErrOllamaCloudUsageIdentityChanged)
	require.NotContains(t, account.Extra, OllamaCloudUsageSnapshotExtraKey)
}

func TestOllamaCloudUsageRunnerHonorsLeaderLockAndBackoff(t *testing.T) {
	account := ollamaUsageAccount(11)

	account.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{11: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	cache := &fakeLeaderLockCache{}
	_, acquired := tryAcquireSingletonLeaderLock(context.Background(), cache, nil, ollamaCloudUsageLeaderLockKey, "peer", time.Minute)
	require.True(t, acquired)
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo)
	svc.lockCache = cache
	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, upstream.calls.Load())
	require.NoError(t, cache.ReleaseLeaderLock(context.Background(), ollamaCloudUsageLeaderLockKey, "peer"))
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), upstream.calls.Load())

	firstFailure := nextOllamaCloudUsageDelay(60, 1, 0)
	thirdFailure := nextOllamaCloudUsageDelay(60, 3, 0)
	require.Greater(t, thirdFailure, firstFailure)
	require.GreaterOrEqual(t, nextOllamaCloudUsageDelay(60, 1, 3*time.Hour), 3*time.Hour)
	require.LessOrEqual(t, nextOllamaCloudUsageDelay(60, 20, 0), ollamaCloudUsageMaxDelay+5*time.Minute)
}

func TestOllamaCloudUsageRunnerDisablesAutoRefreshAfterUnpersistableIdentityError(t *testing.T) {
	account := ollamaUsageAccount(14)

	account.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	missingProxyID := int64(99)
	account.ProxyID = &missingProxyID
	account.Proxy = nil
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{14: account}}}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo)

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoCalls.Load())
	require.Equal(t, false, account.Extra[OllamaCloudUsageAutoRefreshExtraKey])
	require.Zero(t, upstream.calls.Load())

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoCalls.Load())
	require.Zero(t, upstream.calls.Load())
}

func TestOllamaCloudUsageRunnerIdentityChangePreservesOldGroupAndDoesNotLoop(t *testing.T) {
	anchor := ollamaUsageAccount(15)
	anchor.Credentials["api_key"] = "shared-before-rotation"

	anchor.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	sibling := ollamaUsageAccount(16)
	sibling.Credentials = map[string]any{"api_key": "shared-before-rotation", "base_url": "https://ollama.com:443/v1"}

	sibling.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
	dueAnchor := *anchor
	dueAnchor.Credentials = mergeMap(nil, anchor.Credentials)
	dueAnchor.Extra = mergeMap(nil, anchor.Extra)
	repo := &ollamaUsageTestRepo{
		upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
			anchor.ID: anchor, sibling.ID: sibling,
		}},
		due: []Account{dueAnchor},
	}
	var rotateOnce sync.Once
	repo.beforeSnapshot = func() {
		rotateOnce.Do(func() {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			anchor.Credentials["api_key"] = "rotated-account-key"
			delete(anchor.Extra, OllamaCloudUsageSessionExtraKey)
			delete(anchor.Extra, OllamaCloudUsageAutoRefreshExtraKey)
			delete(anchor.Extra, OllamaCloudUsageSnapshotExtraKey)
		})
	}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo)

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoAttempts.Load())
	require.Zero(t, repo.disableAutoCalls.Load(), "the stale anchor CAS must not disable the old sibling group")
	require.Equal(t, true, sibling.Extra[OllamaCloudUsageAutoRefreshExtraKey])
	require.NotContains(t, anchor.Extra, OllamaCloudUsageAutoRefreshExtraKey)

	repo.due = []Account{*anchor, *sibling}
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoAttempts.Load(), "the changed account must not be retried")
	require.Equal(t, true, sibling.Extra[OllamaCloudUsageAutoRefreshExtraKey])
	require.NotNil(t, decodeOllamaCloudUsageSnapshot(sibling.Extra), "the still-valid sibling must refresh normally")
	require.Equal(t, int64(2), upstream.calls.Load())
}

func TestOllamaCloudUsageSingleflightConcurrencyAndRunnerSwitches(t *testing.T) {
	accounts := make(map[int64]*Account)
	for id := int64(1); id <= 7; id++ {
		account := ollamaUsageAccount(id)

		account.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
		accounts[id] = account
	}
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: accounts}}
	unblock := make(chan struct{})
	entered := make(chan struct{}, 10)
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t), beforeResponse: func(*http.Request) {
		entered <- struct{}{}
		<-unblock
	}}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{}}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo)

	// Global automatic refresh is fail-safe off by default.
	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, upstream.calls.Load())

	settingsRepo.values[SettingKeyOllamaCloudUsageSettings] = `{"enabled":true,"interval_minutes":60}`
	var singleflight sync.WaitGroup
	singleflight.Add(2)
	for range 2 {
		go func() {
			defer singleflight.Done()
			_, _ = svc.Refresh(context.Background(), 1)
		}()
	}
	<-entered
	close(unblock)
	singleflight.Wait()
	require.Equal(t, int64(1), upstream.calls.Load())

	// Clear snapshots so all accounts are due, then verify the shared four-slot bound.
	for _, account := range accounts {
		delete(account.Extra, OllamaCloudUsageSnapshotExtraKey)
	}
	unblock2 := make(chan struct{})
	upstream.beforeResponse = func(*http.Request) { <-unblock2 }
	done := make(chan struct{})
	go func() {
		_ = svc.RunDue(context.Background())
		close(done)
	}()
	require.Eventually(t, func() bool { return upstream.active.Load() == ollamaCloudUsageConcurrency }, time.Second, 10*time.Millisecond)
	close(unblock2)
	<-done
	require.LessOrEqual(t, upstream.maxActive.Load(), int64(ollamaCloudUsageConcurrency))
	require.Equal(t, int64(8), upstream.calls.Load())
}

func TestOllamaCloudUsageIncompleteRefreshPreservesPreviousData(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprintf("scheduled=%v", scheduled), func(t *testing.T) {
			account := ollamaUsageAccount(701)

			account.Extra[OllamaCloudUsageAutoRefreshExtraKey] = true
			repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
			upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
			settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60,"debounce_minutes":1}`}}
			svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo)
			now := time.Date(2026, time.October, 8, 8, 0, 0, 0, time.UTC)
			svc.now = func() time.Time { return now }
			refresh := func() {
				if scheduled {
					require.NoError(t, svc.RunDue(context.Background()))
				} else {
					_, e := svc.Refresh(context.Background(), account.ID)
					require.NoError(t, e)
				}
			}
			refresh()
			before := decodeOllamaCloudUsageSnapshot(account.Extra)
			require.NotNil(t, before)
			require.Equal(t, OllamaCloudUsageStatusOK, before.Status)
			require.Equal(t, 50.0, before.Data.Monthly.UsedPercent)
			now = now.Add(2 * time.Hour)
			activity := now.Add(-5 * time.Minute)
			account.LastUsedAt = &activity
			upstream.body = []byte(`{"included":{},"purchased":{"balance_usd":39}}`)
			refresh()
			after := decodeOllamaCloudUsageSnapshot(account.Extra)
			require.Equal(t, OllamaCloudUsageStatusFailed, after.Status)
			require.Equal(t, "incomplete_usage", after.LastError)
			require.Equal(t, before.Data, after.Data)
			require.Equal(t, before.FetchedAt, after.FetchedAt)
			require.Equal(t, now, after.LastAttemptAt)
			require.Equal(t, 1, after.FailureCount)
			require.Equal(t, true, account.Extra[OllamaCloudUsageAutoRefreshExtraKey])
			require.Equal(t, int64(2), upstream.calls.Load())
		})
	}
}

func TestOllamaCloudBalanceAPI(t *testing.T) {
	for _, test := range []struct {
		name, body, balance string
		monthly             *float64
	}{
		{"monthly", `{"included":{"balance_usd":72.5,"allowance_usd":100,"period":{"from":"2026-09-15T09:30:00Z","until":"2026-10-15T09:30:00Z"}},"purchased":{"balance_usd":25}}`, "$97.5", func() *float64 { v := 27.5; return &v }()},
		{"fractional sum", `{"included":{"balance_usd":0.1,"allowance_usd":1,"period":{"from":"2026-09-15T09:30:00Z","until":"2026-10-15T09:30:00Z"}},"purchased":{"balance_usd":0.2}}`, "$0.3", func() *float64 { v := 90.0; return &v }()},
		{"zero allowance", `{"included":{"balance_usd":0,"allowance_usd":0,"period":{"from":"2026-09-15T09:30:00Z","until":"2026-10-15T09:30:00Z"}},"purchased":{"balance_usd":0}}`, "$0", nil},
		{"tiny purchased credits", `{"included":{"balance_usd":0,"allowance_usd":100,"period":{"from":"2026-09-15T09:30:00Z","until":"2026-10-15T09:30:00Z"}},"purchased":{"balance_usd":0.00000001}}`, "$0.00000001", func() *float64 { v := 100.0; return &v }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := parseOllamaCloudBalance([]byte(test.body))
			require.NoError(t, err)
			require.Equal(t, test.balance, data.Balance)
			require.Nil(t, data.FiveHour)
			require.Nil(t, data.SevenDay)
			if test.monthly == nil {
				require.Nil(t, data.Monthly)
			} else {
				require.InDelta(t, *test.monthly, data.Monthly.UsedPercent, 1e-9)
				require.Equal(t, "2026-10-15T09:30:00Z", data.Monthly.ResetAt.Format(time.RFC3339))
			}
		})
	}
	legacy, err := parseOllamaCloudBalance([]byte(`{"included":{"session":{"remaining_percent":75,"resets_at":"2026-10-01T07:00:00Z"},"weekly":{"remaining_percent":40,"resets_at":"2026-10-05T00:00:00Z"}},"purchased":{"balance_usd":25}}`))
	require.NoError(t, err)
	require.Equal(t, "$25", legacy.Balance)
	require.Equal(t, 25.0, legacy.FiveHour.UsedPercent)
	require.Equal(t, 60.0, legacy.SevenDay.UsedPercent)
	require.Equal(t, "2026-10-01T07:00:00Z", legacy.FiveHour.ResetAt.Format(time.RFC3339))
	require.Equal(t, "2026-10-05T00:00:00Z", legacy.SevenDay.ResetAt.Format(time.RFC3339))
	require.Nil(t, legacy.Monthly)
}

func TestOllamaCloudBalanceRequiresCompleteVariant(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"included":{},"purchased":{"balance_usd":0}}`,
		`{"included":{"balance_usd":0,"allowance_usd":100},"purchased":{"balance_usd":0}}`,
		`{"included":{"session":{"remaining_percent":0,"resets_at":"2026-10-01T07:00:00Z"}},"purchased":{"balance_usd":0}}`,
		`{"included":{"session":{"remaining_percent":101,"resets_at":"2026-10-01T07:00:00Z"},"weekly":{"remaining_percent":0,"resets_at":"2026-10-05T00:00:00Z"}},"purchased":{"balance_usd":0}}`,
		`{"included":{"balance_usd":0,"allowance_usd":100,"period":{"from":"2026-09-15T00:00:00Z","until":"2026-10-15T00:00:00Z"}},"purchased":{}}`,
	} {
		_, err := parseOllamaCloudBalance([]byte(body))
		require.ErrorIs(t, err, errOllamaCloudIncompleteUsage, body)
	}
	_, err := parseOllamaCloudBalance([]byte(`{"included":`))
	require.ErrorIs(t, err, errOllamaCloudInvalidJSON)
}

func TestOllamaCloudUsageHistoryPreservesOptionalMetrics(t *testing.T) {
	body := []byte(`{"range":"7d","scope":"self","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{"request_count":16,"usage_usd":0.0268,"input_tokens":116000,"cached_input_tokens":60000,"output_tokens":15000,"groups":[{"model":"glm-5.3","request_count":12,"usage_usd":0,"input_tokens":96000,"cached_input_tokens":0,"output_tokens":12000}],"remainder":{"request_count":4,"usage_usd":0.01}},"buckets":[{"unused":"discard"}]}`)
	history, err := parseOllamaCloudUsage(body)
	require.NoError(t, err)
	require.Equal(t, int64(16), history.Totals.RequestCount)
	require.Equal(t, 0.0268, *history.Totals.UsageUSD)
	require.Equal(t, int64(116000), *history.Totals.InputTokens)
	require.Equal(t, int64(60000), *history.Totals.CachedInputTokens)
	require.Equal(t, int64(15000), *history.Totals.OutputTokens)
	require.Equal(t, "2026-09-24T00:00:00Z", history.From.Format(time.RFC3339))
	require.Equal(t, "2026-10-01T02:30:00Z", history.Until.Format(time.RFC3339))
	require.Len(t, history.Totals.Groups, 1)
	require.Equal(t, "glm-5.3", history.Totals.Groups[0].Model)
	require.Zero(t, *history.Totals.Groups[0].UsageUSD)
	require.Zero(t, *history.Totals.Groups[0].CachedInputTokens)
	require.Equal(t, int64(4), history.Totals.Remainder.RequestCount)
	raw, err := json.Marshal(history)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "buckets")

	legacy, err := parseOllamaCloudUsage([]byte(`{"range":"7d","scope":"self","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{"request_count":0,"groups":[{"model":"old-model","request_count":0}],"remainder":{"request_count":0}}}`))
	require.NoError(t, err)
	raw, err = json.Marshal(legacy)
	require.NoError(t, err)
	for _, name := range []string{"usage_usd", "input_tokens", "cached_input_tokens", "output_tokens"} {
		require.NotContains(t, string(raw), name)
	}
	require.Contains(t, string(raw), `"request_count":0`)
}

func TestOllamaCloudUsageHistoryRequiresCompleteFields(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`,
		`{"range":"7d","scope":"self","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{}}`,
		`{"range":"30d","scope":"self","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{"request_count":0}}`,
		`{"range":"7d","scope":"team","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{"request_count":0}}`,
		`{"range":"7d","scope":"self","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{"request_count":0,"groups":[{"model":"model"}]}}`,
		`{"range":"7d","scope":"self","from":"2026-09-24T00:00:00Z","until":"2026-10-01T02:30:00Z","totals":{"request_count":0,"remainder":{}}}`,
	} {
		_, err := parseOllamaCloudUsage([]byte(body))
		require.ErrorIs(t, err, errOllamaCloudIncompleteUsage, body)
	}
}

func TestOllamaCloudAPIFailuresPreserveSnapshot(t *testing.T) {
	for _, endpoint := range []string{"balance", "usage"} {
		for _, test := range []struct {
			name           string
			status         int
			body           []byte
			reason         string
			snapshotStatus string
		}{
			{"invalid credentials", 401, []byte(`{"error":"secret"}`), "unauthorized", OllamaCloudUsageStatusUnauthorized},
			{"suspended", 403, []byte(`{"error":"secret"}`), "http_error", OllamaCloudUsageStatusFailed},
			{"rate limited", 429, []byte(`{"error":"secret"}`), "http_error", OllamaCloudUsageStatusFailed},
			{"redirect", 302, nil, "redirect_blocked", OllamaCloudUsageStatusFailed},
			{"invalid JSON", 200, []byte(`{"secret":`), "invalid_json", OllamaCloudUsageStatusFailed},
			{"missing fields", 200, []byte(`{}`), "incomplete_usage", OllamaCloudUsageStatusFailed},
			{"body limit", 200, make([]byte, ollamaCloudUsageMaxBodyBytes+1), "response_too_large", OllamaCloudUsageStatusFailed},
		} {
			t.Run(endpoint+"/"+test.name, func(t *testing.T) {
				account := ollamaUsageAccount(801)
				repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
				upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
				svc := newOllamaUsageTestService(t, repo, upstream, &upstreamBillingProbeSettingRepo{})
				now := time.Date(2026, 10, 1, 2, 30, 0, 0, time.UTC)
				svc.now = func() time.Time { return now }
				before, err := svc.Refresh(context.Background(), account.ID)
				require.NoError(t, err)
				now = now.Add(time.Minute)
				upstream.header = http.Header{"Retry-After": []string{"7200"}}
				if endpoint == "balance" {
					upstream.status, upstream.body = test.status, test.body
				} else {
					upstream.usageStatus, upstream.usageBody = test.status, test.body
				}
				after, err := svc.Refresh(context.Background(), account.ID)
				require.NoError(t, err)
				require.Equal(t, test.snapshotStatus, after.Snapshot.Status)
				require.Equal(t, test.reason, after.Snapshot.LastError)
				require.Equal(t, before.Snapshot.Data, after.Snapshot.Data)
				require.Equal(t, before.Snapshot.FetchedAt, after.Snapshot.FetchedAt)
				require.Equal(t, 1, after.Snapshot.FailureCount)
				require.NotContains(t, after.Snapshot.LastError, "secret")
				if test.status == 429 {
					require.False(t, after.Snapshot.NextRefreshAt.Before(now.Add(2*time.Hour)))
					now = now.Add(ollamaCloudUsageManualRefreshInterval)
					_, err = svc.Refresh(context.Background(), account.ID)
					require.ErrorIs(t, err, ErrOllamaCloudUsageRefreshRateLimited)
					now = after.Snapshot.NextRefreshAt
					upstream.status, upstream.usageStatus = http.StatusOK, http.StatusOK
					upstream.body, upstream.usageBody, upstream.header = ollamaUsageFixture(t), nil, nil
					recovered, err := svc.Refresh(context.Background(), account.ID)
					require.NoError(t, err)
					require.Equal(t, OllamaCloudUsageStatusOK, recovered.Snapshot.Status)
				}
			})
		}
	}
}

func TestOllamaCloudAPIKeyReadinessAndOldSnapshotCutover(t *testing.T) {
	account := ollamaUsageAccount(802)
	account.Extra["ollama_cloud_usage_snapshot"] = map[string]any{"status": "unauthorized", "next_refresh_at": "2099-01-01T00:00:00Z"}
	account.Extra[OllamaCloudUsageSessionExtraKey] = "unused legacy session"
	state := OllamaCloudUsageStateFromAccount(account)
	require.True(t, state.Configured)
	require.Nil(t, state.Snapshot)
	account.Credentials["api_key"] = "invalid\r\nheader"
	require.False(t, OllamaCloudUsageStateFromAccount(account).Configured)
	account.Credentials["api_key"] = ""
	require.False(t, OllamaCloudUsageStateFromAccount(account).Configured)
	account.Credentials["api_key"] = "working-key"
	repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
	upstream := &ollamaUsageHTTPStub{body: []byte(`{"included":{"balance_usd":0,"allowance_usd":0,"period":{"from":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z"}},"purchased":{"balance_usd":0}}`)}
	svc := newOllamaUsageTestService(t, repo, upstream, &upstreamBillingProbeSettingRepo{})
	state, err := svc.Refresh(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, OllamaCloudUsageStatusOK, state.Snapshot.Status)
	require.Nil(t, state.Snapshot.Data.Monthly)
	require.Equal(t, int64(0), state.Snapshot.Data.Usage.Totals.RequestCount)
	require.NotContains(t, account.Extra, "ollama_cloud_usage_snapshot")
	require.NotContains(t, account.Extra, OllamaCloudUsageSessionExtraKey)
}

type ollamaUsageReadError struct{}

func (ollamaUsageReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestOllamaCloudAPITransportFailuresPreserveSnapshot(t *testing.T) {
	for _, endpoint := range []string{"/api/balance", "/api/usage"} {
		for _, reason := range []string{"request_failed", "empty_response", "response_read_failed", "response_host_mismatch"} {
			t.Run(endpoint+"/"+reason, func(t *testing.T) {
				account := ollamaUsageAccount(803)
				repo := &ollamaUsageTestRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
				upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
				svc := newOllamaUsageTestService(t, repo, upstream, &upstreamBillingProbeSettingRepo{})
				now := time.Date(2026, 10, 1, 2, 30, 0, 0, time.UTC)
				svc.now = func() time.Time { return now }
				before, err := svc.Refresh(context.Background(), account.ID)
				require.NoError(t, err)
				now = now.Add(time.Minute)
				upstream.responseOverride = func(req *http.Request) (*http.Response, error) {
					if req.URL.Path != endpoint {
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(ollamaUsageFixture(t)))), Request: req}, nil
					}
					switch reason {
					case "request_failed":
						return nil, io.ErrUnexpectedEOF
					case "empty_response":
						return nil, nil
					case "response_read_failed":
						return &http.Response{StatusCode: 200, Body: io.NopCloser(ollamaUsageReadError{}), Request: req}, nil
					default:
						foreign := req.Clone(req.Context())
						urlCopy := *foreign.URL
						urlCopy.Host = "example.com"
						foreign.URL = &urlCopy
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Request: foreign}, nil
					}
				}
				after, err := svc.Refresh(context.Background(), account.ID)
				require.NoError(t, err)
				require.Equal(t, reason, after.Snapshot.LastError)
				require.Equal(t, OllamaCloudUsageStatusFailed, after.Snapshot.Status)
				require.Equal(t, before.Snapshot.Data, after.Snapshot.Data)
				require.Equal(t, before.Snapshot.FetchedAt, after.Snapshot.FetchedAt)
			})
		}
	}
}

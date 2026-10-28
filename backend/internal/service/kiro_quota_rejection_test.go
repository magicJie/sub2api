//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// recordingKiroQuotaRejectionRepo 记录限流与「可退役」标记的写入。
type recordingKiroQuotaRejectionRepo struct {
	recordingKiroTempUnschedRepo
	rejectedCalls int
	rejectedID    int64
	rejectedAt    time.Time
	rejectedWhy   string
	clearCalls    int
}

func (r *recordingKiroQuotaRejectionRepo) SetKiroQuotaRejected(_ context.Context, id int64, at time.Time, reason string) error {
	r.rejectedCalls++
	r.rejectedID = id
	r.rejectedAt = at
	r.rejectedWhy = reason
	return nil
}

func (r *recordingKiroQuotaRejectionRepo) ClearKiroQuotaRejected(context.Context, int64) error {
	r.clearCalls++
	return nil
}

func TestKiroQuotaRejectionCategory(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"monthly request count", http.StatusPaymentRequired, `{"reason":"MONTHLY_REQUEST_COUNT","message":"You have reached the limit."}`, kiroErrorMonthlyRequest},
		{"quota 429", http.StatusTooManyRequests, `{"message":"You have exceeded your quota."}`, kiroErrorQuotaExhausted},
		{"credit 429", http.StatusTooManyRequests, `{"message":"Your credits have been exhausted"}`, kiroErrorQuotaExhausted},
		{"overage 402", http.StatusPaymentRequired, `{"message":"Overage limit reached"}`, kiroErrorOverageExhausted},
		{"unknown 402", http.StatusPaymentRequired, `{"message":"payment required"}`, "payment_required_unknown"},
		{"empty 402", http.StatusPaymentRequired, ``, "payment_required_unknown"},
		{"throttle 429", http.StatusTooManyRequests, `{"message":"Too many requests, please wait before trying again."}`, ""},
		{"throttling reason 429", http.StatusTooManyRequests, `{"message":"Too many requests","reason":"THROTTLING"}`, ""},
		{"capacity 429", http.StatusTooManyRequests, `{"reason":"INSUFFICIENT_MODEL_CAPACITY"}`, ""},
		{"quota text on 403", http.StatusForbidden, `{"message":"quota exceeded"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, kiroQuotaRejectionCategory(tt.status, tt.body))
		})
	}
}

func kiroQuotaRejectionTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c
}

func TestClassifyKiroHTTPErrorQuota429MarksRejectedUntilSnapshotReset(t *testing.T) {
	account := kiroBridgeTestAccount()
	reset := time.Now().Add(9 * 24 * time.Hour).Truncate(time.Second)
	recordKiroPaceSnapshot(account.ID, &UsageInfo{
		KiroCredit:  &KiroCreditProgress{CurrentUsage: 4990, UsageLimit: 5000, PercentageUsed: 99.8},
		KiroResetAt: &reset,
	}, time.Now())
	t.Cleanup(func() {
		kiroPaceSnapshots.Delete(account.ID)
		kiroQuotaRejections.Delete(account.ID)
	})
	repo := &recordingKiroQuotaRejectionRepo{}
	svc := &GatewayService{accountRepo: repo}
	const body = `{"message":"You have exceeded your quota."}`

	_, err := svc.classifyKiroHTTPErrorAndRecordOps(t.Context(), newJSONResponse(http.StatusTooManyRequests, body), kiroQuotaRejectionTestContext(), account, "claude-sonnet-4.6", nil)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, repo.rateCalled)
	require.Equal(t, reset, repo.rateLimitReset)
	require.Equal(t, 1, repo.rejectedCalls)
	require.Equal(t, account.ID, repo.rejectedID)
	require.Contains(t, repo.rejectedWhy, "429 quota_exhausted")
	require.Contains(t, repo.rejectedWhy, "exceeded your quota")
	require.True(t, kiroAccountQuotaRejected(account, time.Now()))
}

func TestClassifyKiroHTTPErrorUnknown402MarksRejectedUntilNextMonth(t *testing.T) {
	account := kiroBridgeTestAccount()
	t.Cleanup(func() { kiroQuotaRejections.Delete(account.ID) })
	repo := &recordingKiroQuotaRejectionRepo{}
	svc := &GatewayService{accountRepo: repo}

	_, err := svc.classifyKiroHTTPErrorAndRecordOps(t.Context(), newJSONResponse(http.StatusPaymentRequired, `{"message":"billing problem"}`), kiroQuotaRejectionTestContext(), account, "", nil)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, repo.rateCalled)
	require.Equal(t, nextKiroMonthlyResetUTC(time.Now()), repo.rateLimitReset)
	require.Equal(t, 1, repo.rejectedCalls)
	require.Contains(t, repo.rejectedWhy, "payment_required_unknown")
}

func TestClassifyKiroHTTPErrorMonthlyRequestCountMarksRejected(t *testing.T) {
	account := kiroBridgeTestAccount()
	t.Cleanup(func() { kiroQuotaRejections.Delete(account.ID) })
	repo := &recordingKiroQuotaRejectionRepo{}
	svc := &GatewayService{accountRepo: repo}

	_, err := svc.classifyKiroHTTPErrorAndRecordOps(t.Context(), newJSONResponse(http.StatusPaymentRequired, `{"message":"You have reached the limit.","reason":"MONTHLY_REQUEST_COUNT"}`), kiroQuotaRejectionTestContext(), account, "", nil)
	require.Error(t, err)
	require.Equal(t, 1, repo.rateLimitedCall, "沿用 markKiroMonthlyRequestCountRateLimited，只限流一次")
	require.Equal(t, nextKiroMonthlyResetUTC(time.Now()), repo.rateLimitReset)
	require.Equal(t, 1, repo.rejectedCalls)
	require.Contains(t, repo.rejectedWhy, "monthly_request_count")
}

func TestClassifyKiroHTTPErrorThrottle429DoesNotMarkRejected(t *testing.T) {
	account := kiroBridgeTestAccount()
	repo := &recordingKiroQuotaRejectionRepo{}
	svc := &GatewayService{accountRepo: repo}

	_, err := svc.classifyKiroHTTPErrorAndRecordOps(t.Context(), newJSONResponse(http.StatusTooManyRequests, kiroThrottleBody), kiroQuotaRejectionTestContext(), account, "", nil)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, repo.rateCalled)
	require.Zero(t, repo.rejectedCalls)
	require.False(t, kiroAccountQuotaRejected(account, time.Now()))
}

func TestExecuteKiroUpstreamUnknown402MarksRejected(t *testing.T) {
	sleeps := stubKiroRetrySleep(t)
	svc, account, upstream, _ := newKiro429TestService(t, newJSONResponse(http.StatusPaymentRequired, `{"message":"payment required"}`))
	repo := &recordingKiroQuotaRejectionRepo{}
	svc.accountRepo = repo
	t.Cleanup(func() { kiroQuotaRejections.Delete(account.ID) })

	_, _, err := svc.executeKiroUpstream(context.Background(), account, kiro429TestPayload(t), "claude-sonnet-4-6", "claude-sonnet-4-6", "test-token", nil)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Len(t, upstream.requests, 1)
	require.Empty(t, *sleeps)
	require.True(t, repo.rateCalled)
	require.Equal(t, 1, repo.rejectedCalls)
}

func TestKiroQuotaRejectionManagedExtraIsPreserved(t *testing.T) {
	current := map[string]any{KiroQuotaRejectedAtExtraKey: "2026-10-20T00:00:00Z", KiroQuotaRejectedReasonExtraKey: "402"}
	edited := map[string]any{KiroQuotaRejectedAtExtraKey: "forged", "other": 1}
	kiroPreserveQuotaRejectionExtra(edited, current)
	require.Equal(t, "2026-10-20T00:00:00Z", edited[KiroQuotaRejectedAtExtraKey])
	require.Equal(t, "402", edited[KiroQuotaRejectedReasonExtraKey])

	updates := map[string]any{KiroQuotaRejectedAtExtraKey: "forged", KiroQuotaRejectedReasonExtraKey: "x", "other": 1}
	stripKiroQuotaRejectionExtra(updates)
	require.Equal(t, map[string]any{"other": 1}, updates)
}

func kiroRejectionSnapshot(updatedAt time.Time, used float64, resetAt time.Time) *KiroUsageSnapshot {
	return &KiroUsageSnapshot{
		KiroResetAt:   &resetAt,
		KiroCredit:    &KiroCreditProgress{CurrentUsage: used * 50, UsageLimit: 5000, PercentageUsed: used},
		UpdatedAt:     &updatedAt,
		LastAttemptAt: updatedAt,
	}
}

func TestKiroQuotaRejectionShouldClearAfterReset(t *testing.T) {
	now := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	rejectedAt := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	limitedUntil := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	oldReset := limitedUntil
	newReset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	account := &Account{
		ID:               77,
		Platform:         PlatformKiro,
		RateLimitResetAt: &limitedUntil,
		Extra:            map[string]any{KiroQuotaRejectedAtExtraKey: rejectedAt.Format(time.RFC3339Nano), KiroQuotaRejectedReasonExtraKey: "402"},
	}
	prev := kiroRejectionSnapshot(rejectedAt.Add(-time.Hour), 99.8, oldReset)

	// 新周期：限流已过，用量下降。
	require.True(t, kiroQuotaRejectionShouldClear(account, prev, kiroRejectionSnapshot(now, 0.4, newReset), now))
	// 用量没降但账期重置时间推进了。
	require.True(t, kiroQuotaRejectionShouldClear(account, prev, kiroRejectionSnapshot(now, 99.8, newReset), now))
	// 限流还没过：不清除。
	require.False(t, kiroQuotaRejectionShouldClear(account, prev, kiroRejectionSnapshot(now, 0.4, newReset), limitedUntil.Add(-time.Minute)))
	// 限流已过但快照仍是旧周期（用量没降、重置时间没变）：不清除。
	require.False(t, kiroQuotaRejectionShouldClear(account, prev, kiroRejectionSnapshot(now, 99.8, oldReset), now))
	// 快照早于拒绝时间：不清除。
	require.False(t, kiroQuotaRejectionShouldClear(account, prev, kiroRejectionSnapshot(rejectedAt.Add(-time.Minute), 0.4, newReset), now))
	// 没有标记：不清除。
	require.False(t, kiroQuotaRejectionShouldClear(&Account{ID: 78, Platform: PlatformKiro}, prev, kiroRejectionSnapshot(now, 0.4, newReset), now))
}

func TestClearKiroQuotaRejectionAfterResetWritesRepoAndDropsMarker(t *testing.T) {
	now := time.Now()
	rejectedAt := now.Add(-48 * time.Hour)
	limitedUntil := now.Add(-time.Hour)
	account := &Account{
		ID:               79,
		Platform:         PlatformKiro,
		RateLimitResetAt: &limitedUntil,
		Extra:            map[string]any{KiroQuotaRejectedAtExtraKey: rejectedAt.Format(time.RFC3339Nano), KiroQuotaRejectedReasonExtraKey: "402"},
	}
	kiroQuotaRejections.Store(account.ID, limitedUntil)
	t.Cleanup(func() { kiroQuotaRejections.Delete(account.ID) })
	repo := &recordingKiroQuotaRejectionRepo{}
	svc := &AccountUsageService{accountRepo: repo}

	prev := kiroRejectionSnapshot(rejectedAt.Add(-time.Hour), 100, limitedUntil)
	svc.clearKiroQuotaRejectionAfterReset(context.Background(), account, prev, kiroRejectionSnapshot(now, 1, now.Add(30*24*time.Hour)), now)
	require.Equal(t, 1, repo.clearCalls)
	require.NotContains(t, account.Extra, KiroQuotaRejectedAtExtraKey)
	_, cached := kiroQuotaRejections.Load(account.ID)
	require.False(t, cached)

	// 再次刷新时已无标记，不重复写库。
	svc.clearKiroQuotaRejectionAfterReset(context.Background(), account, prev, kiroRejectionSnapshot(now, 1, now.Add(30*24*time.Hour)), now)
	require.Equal(t, 1, repo.clearCalls)
}

func TestKiroQuotaRejectionFromExtra(t *testing.T) {
	at, reason := KiroQuotaRejectionFromExtra(map[string]any{KiroQuotaRejectedAtExtraKey: "2026-10-27T08:00:00Z", KiroQuotaRejectedReasonExtraKey: "402 x"})
	require.NotNil(t, at)
	require.Equal(t, time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC), at.UTC())
	require.Equal(t, "402 x", reason)
	at, _ = KiroQuotaRejectionFromExtra(map[string]any{KiroQuotaRejectedAtExtraKey: "garbage"})
	require.Nil(t, at)
}

//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type reminderKey struct {
	id int64
	at int64
}

// fakeExpiryReminderRepo 用内存模拟 accounts + account_expiry_reminders。
type fakeExpiryReminderRepo struct {
	accounts []AccountExpiryCandidate
	claimed  map[reminderKey]int64
}

func (r *fakeExpiryReminderRepo) ListExpiringAccounts(_ context.Context, from, to time.Time, limit int) ([]AccountExpiryCandidate, error) {
	out := []AccountExpiryCandidate{}
	for _, a := range r.accounts {
		if !a.ExpiresAt.After(from) || a.ExpiresAt.After(to) {
			continue
		}
		if _, ok := r.claimed[reminderKey{a.ID, a.ExpiresAt.Unix()}]; ok {
			continue
		}
		out = append(out, a)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *fakeExpiryReminderRepo) ClaimExpiryReminder(_ context.Context, id int64, at time.Time) (bool, error) {
	k := reminderKey{id, at.Unix()}
	if _, ok := r.claimed[k]; ok {
		return false, nil
	}
	r.claimed[k] = 0
	return true, nil
}

func (r *fakeExpiryReminderRepo) ReleaseExpiryReminder(_ context.Context, id int64, at time.Time) error {
	delete(r.claimed, reminderKey{id, at.Unix()})
	return nil
}

func (r *fakeExpiryReminderRepo) SetExpiryReminderAlertEvent(_ context.Context, id int64, at time.Time, eventID int64) error {
	r.claimed[reminderKey{id, at.Unix()}] = eventID
	return nil
}

type fakeExpiryAlertSink struct {
	enabled bool
	days    int
	fail    bool
	events  []*OpsAlertEvent
}

func (s *fakeExpiryAlertSink) IsMonitoringEnabled(context.Context) bool { return s.enabled }
func (s *fakeExpiryAlertSink) OpsAdvancedSettingsSnapshot() OpsAdvancedSettings {
	return OpsAdvancedSettings{AccountExpiryReminderDays: s.days}
}
func (s *fakeExpiryAlertSink) CreateAlertEvent(_ context.Context, e *OpsAlertEvent) (*OpsAlertEvent, error) {
	if s.fail {
		return nil, errors.New("boom")
	}
	cp := *e
	cp.ID = int64(len(s.events) + 1)
	s.events = append(s.events, &cp)
	return &cp, nil
}

func newTestExpiryReminder(now time.Time, accounts []AccountExpiryCandidate, days int) (*AccountExpiryReminder, *fakeExpiryReminderRepo, *fakeExpiryAlertSink) {
	repo := &fakeExpiryReminderRepo{accounts: accounts, claimed: map[reminderKey]int64{}}
	sink := &fakeExpiryAlertSink{enabled: true, days: days}
	r := NewAccountExpiryReminder(repo, sink)
	r.now = func() time.Time { return now }
	return r, repo, sink
}

func TestAccountExpiryReminder_WindowBoundary(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	accounts := []AccountExpiryCandidate{
		{ID: 1, Name: "exact-3d", Platform: PlatformOpenAI, ExpiresAt: now.Add(3 * day)},                     // 正好 N 天：提醒
		{ID: 2, Name: "3d-plus-1s", Platform: PlatformOpenAI, ExpiresAt: now.Add(3*day + time.Second)},       // 超出 N 天：不提醒
		{ID: 3, Name: "1h", Platform: PlatformKiro, ExpiresAt: now.Add(time.Hour), AutoPauseOnExpired: true}, // 不足 1 天：提醒
		{ID: 4, Name: "expired", Platform: PlatformOpenAI, ExpiresAt: now},                                   // 已到期：不提醒
		{ID: 5, Name: "past", Platform: PlatformOpenAI, ExpiresAt: now.Add(-day)},                            // 已过期：不提醒
	}
	r, repo, sink := newTestExpiryReminder(now, accounts, 3)

	sent, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, sent)
	require.Len(t, sink.events, 2)
	ids := []any{sink.events[0].Dimensions["account_id"], sink.events[1].Dimensions["account_id"]}
	require.ElementsMatch(t, []any{int64(1), int64(3)}, ids)
	require.Equal(t, "account_expiry", sink.events[0].Dimensions["source"])
	require.Equal(t, OpsAlertStatusFiring, sink.events[0].Status)
	require.Contains(t, sink.events[1].Description, "不足 1 天")
	require.Contains(t, sink.events[1].Description, "自动暂停")
	require.Equal(t, int64(1), repo.claimed[reminderKey{1, accounts[0].ExpiresAt.Unix()}])
}

func TestAccountExpiryReminder_OnlyOncePerExpiry(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	accounts := []AccountExpiryCandidate{{ID: 7, Name: "ollama", Platform: PlatformOpenAI, ExpiresAt: now.Add(48 * time.Hour)}}
	r, repo, sink := newTestExpiryReminder(now, accounts, 3)

	for i := 0; i < 3; i++ {
		_, err := r.RunOnce(context.Background())
		require.NoError(t, err)
		r.now = func() time.Time { return now.Add(time.Duration(i+1) * time.Hour) }
	}
	require.Len(t, sink.events, 1)

	// 即使列表查询没有过滤（例如多实例并发），占位也只成功一次。
	claimed, err := repo.ClaimExpiryReminder(context.Background(), 7, accounts[0].ExpiresAt)
	require.NoError(t, err)
	require.False(t, claimed)

	// 续费改了到期日后，按新到期日再提醒一次。
	repo.accounts[0].ExpiresAt = now.Add(60 * time.Hour)
	_, err = r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, sink.events, 2)
}

func TestAccountExpiryReminder_AlertFailureReleasesClaim(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	accounts := []AccountExpiryCandidate{{ID: 9, Name: "a", Platform: PlatformOpenAI, ExpiresAt: now.Add(time.Hour)}}
	r, repo, sink := newTestExpiryReminder(now, accounts, 3)
	sink.fail = true

	sent, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, sent)
	require.Empty(t, repo.claimed)

	sink.fail = false
	sent, err = r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, sent)
}

func TestAccountExpiryReminder_DisabledSkips(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	accounts := []AccountExpiryCandidate{{ID: 1, Name: "a", Platform: PlatformOpenAI, ExpiresAt: now.Add(time.Hour)}}

	r, repo, sink := newTestExpiryReminder(now, accounts, 0)
	sent, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, sent)
	require.Empty(t, repo.claimed)

	sink.days = 3
	sink.enabled = false
	sent, err = r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, sent)
	require.Empty(t, repo.claimed)
	require.Empty(t, sink.events)
}

func TestOpsAdvancedSettings_AccountExpiryReminderDays(t *testing.T) {
	require.Equal(t, AccountExpiryReminderDefaultDays, defaultOpsAdvancedSettings().AccountExpiryReminderDays)

	cfg := defaultOpsAdvancedSettings()
	cfg.AccountExpiryReminderDays = 91
	require.Error(t, validateOpsAdvancedSettings(cfg))
	cfg.AccountExpiryReminderDays = 0
	require.NoError(t, validateOpsAdvancedSettings(cfg))
	cfg.AccountExpiryReminderDays = -1
	normalizeOpsAdvancedSettings(cfg)
	require.Equal(t, AccountExpiryReminderDefaultDays, cfg.AccountExpiryReminderDays)
}

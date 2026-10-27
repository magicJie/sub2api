package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// 账号到期提醒（fork）：到期前 N 天（运维高级设置 account_expiry_reminder_days，默认 3，0 关闭）
// 写一条运维告警（ops_alert_events，走现有告警页面）。所有平台适用；已到期的不提醒（由自动暂停处理）。
// 每个账号每个到期日只提醒一次：先在 account_expiry_reminders 占位（唯一键），占到了才写告警，
// 写告警失败则释放占位，下一轮重试。

const (
	AccountExpiryReminderDefaultDays = 3
	accountExpiryReminderMaxDays     = 90
	accountExpiryReminderBatchLimit  = 500
)

// AccountExpiryCandidate 是即将到期、尚未提醒过的账号。
type AccountExpiryCandidate struct {
	ID                 int64
	Name               string
	Platform           string
	ExpiresAt          time.Time
	AutoPauseOnExpired bool
}

// AccountExpiryReminderRepository 是到期提醒的持久化。
type AccountExpiryReminderRepository interface {
	// ListExpiringAccounts 返回 from < expires_at <= to、且该到期日未提醒过的账号（不含已删除）。
	ListExpiringAccounts(ctx context.Context, from, to time.Time, limit int) ([]AccountExpiryCandidate, error)
	// ClaimExpiryReminder 为 (账号, 到期日) 占位；已存在返回 false。
	ClaimExpiryReminder(ctx context.Context, accountID int64, expiresAt time.Time) (bool, error)
	ReleaseExpiryReminder(ctx context.Context, accountID int64, expiresAt time.Time) error
	SetExpiryReminderAlertEvent(ctx context.Context, accountID int64, expiresAt time.Time, eventID int64) error
}

// accountExpiryAlertSink 由 OpsService 实现。
type accountExpiryAlertSink interface {
	IsMonitoringEnabled(ctx context.Context) bool
	OpsAdvancedSettingsSnapshot() OpsAdvancedSettings
	CreateAlertEvent(ctx context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error)
}

type AccountExpiryReminder struct {
	repo   AccountExpiryReminderRepository
	alerts accountExpiryAlertSink
	now    func() time.Time
}

func NewAccountExpiryReminder(repo AccountExpiryReminderRepository, alerts accountExpiryAlertSink) *AccountExpiryReminder {
	return &AccountExpiryReminder{repo: repo, alerts: alerts, now: time.Now}
}

// RunOnce 检查一轮，返回本轮写出的提醒数。运维监控关闭或天数为 0 时不检查（也不占位，打开后照常提醒）。
func (r *AccountExpiryReminder) RunOnce(ctx context.Context) (int, error) {
	if r == nil || r.repo == nil || r.alerts == nil || !r.alerts.IsMonitoringEnabled(ctx) {
		return 0, nil
	}
	days := r.alerts.OpsAdvancedSettingsSnapshot().AccountExpiryReminderDays
	if days <= 0 {
		return 0, nil
	}
	now := r.now()
	candidates, err := r.repo.ListExpiringAccounts(ctx, now, now.Add(time.Duration(days)*24*time.Hour), accountExpiryReminderBatchLimit)
	if err != nil {
		return 0, err
	}
	sent := 0
	for i := range candidates {
		c := &candidates[i]
		claimed, err := r.repo.ClaimExpiryReminder(ctx, c.ID, c.ExpiresAt)
		if err != nil {
			slog.Warn("account_expiry_reminder_claim_failed", "account_id", c.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		event, err := r.alerts.CreateAlertEvent(ctx, buildAccountExpiryAlertEvent(c, now))
		if err != nil {
			slog.Warn("account_expiry_reminder_alert_failed", "account_id", c.ID, "error", err)
			if rerr := r.repo.ReleaseExpiryReminder(ctx, c.ID, c.ExpiresAt); rerr != nil {
				slog.Warn("account_expiry_reminder_release_failed", "account_id", c.ID, "error", rerr)
			}
			continue
		}
		sent++
		slog.Warn("account_expiry_reminder", "account_id", c.ID, "account_name", c.Name, "platform", c.Platform,
			"expires_at", c.ExpiresAt.Format(time.RFC3339))
		if event != nil && event.ID > 0 {
			if err := r.repo.SetExpiryReminderAlertEvent(ctx, c.ID, c.ExpiresAt, event.ID); err != nil {
				slog.Warn("account_expiry_reminder_link_failed", "account_id", c.ID, "error", err)
			}
		}
	}
	return sent, nil
}

func buildAccountExpiryAlertEvent(c *AccountExpiryCandidate, now time.Time) *OpsAlertEvent {
	remaining := c.ExpiresAt.Sub(now)
	daysLeft := int(remaining / (24 * time.Hour))
	left := fmt.Sprintf("剩约 %d 天", daysLeft)
	if daysLeft < 1 {
		left = "不足 1 天"
	}
	after := "到期后需手动停用"
	if c.AutoPauseOnExpired {
		after = "到期后将自动暂停调度"
	}
	expires := c.ExpiresAt.In(time.Local).Format("2006-01-02 15:04 MST")
	return &OpsAlertEvent{
		Severity: "P2",
		Status:   OpsAlertStatusFiring,
		Title:    fmt.Sprintf("账号即将到期：%s（#%d）", c.Name, c.ID),
		Description: fmt.Sprintf("账号 %s（%s，#%d）将于 %s 到期（%s），%s。续费后请在账号编辑页更新到期日。",
			c.Name, c.Platform, c.ID, expires, left, after),
		Dimensions: map[string]any{
			"source":     "account_expiry",
			"account_id": c.ID,
			"platform":   c.Platform,
			"expires_at": c.ExpiresAt.UTC().Format(time.RFC3339),
		},
		FiredAt: now,
	}
}

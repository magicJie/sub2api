package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 账号到期提醒（fork）：account_expiry_reminders 的读写，见迁移 255 与 service/account_expiry_reminder.go。

func (r *accountRepository) ListExpiringAccounts(ctx context.Context, from, to time.Time, limit int) ([]service.AccountExpiryCandidate, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT a.id, a.name, a.platform, a.expires_at, a.auto_pause_on_expired
		FROM accounts a
		WHERE a.deleted_at IS NULL
			AND a.expires_at IS NOT NULL
			AND a.expires_at > $1
			AND a.expires_at <= $2
			AND NOT EXISTS (
				SELECT 1 FROM account_expiry_reminders r
				WHERE r.account_id = a.id AND r.expires_at = a.expires_at
			)
		ORDER BY a.expires_at, a.id
		LIMIT $3
	`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.AccountExpiryCandidate, 0)
	for rows.Next() {
		var c service.AccountExpiryCandidate
		if err := rows.Scan(&c.ID, &c.Name, &c.Platform, &c.ExpiresAt, &c.AutoPauseOnExpired); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *accountRepository) ClaimExpiryReminder(ctx context.Context, accountID int64, expiresAt time.Time) (bool, error) {
	res, err := r.sql.ExecContext(ctx, `
		INSERT INTO account_expiry_reminders (account_id, expires_at)
		VALUES ($1, $2)
		ON CONFLICT (account_id, expires_at) DO NOTHING
	`, accountID, expiresAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *accountRepository) ReleaseExpiryReminder(ctx context.Context, accountID int64, expiresAt time.Time) error {
	_, err := r.sql.ExecContext(ctx, `DELETE FROM account_expiry_reminders WHERE account_id = $1 AND expires_at = $2`, accountID, expiresAt)
	return err
}

func (r *accountRepository) SetExpiryReminderAlertEvent(ctx context.Context, accountID int64, expiresAt time.Time, eventID int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE account_expiry_reminders SET alert_event_id = $3 WHERE account_id = $1 AND expires_at = $2
	`, accountID, expiresAt, eventID)
	return err
}

var _ service.AccountExpiryReminderRepository = (*accountRepository)(nil)

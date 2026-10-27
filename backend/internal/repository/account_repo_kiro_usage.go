package repository

// Kiro（fork）额度快照的专用读写：只碰 extra.kiro_usage_snapshot 这一个键，
// 不改 updated_at、不发调度 outbox、不同步调度缓存，避免每次刷新引起缓存重建。

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const kiroUsageRefreshEligibleSQL = `
	deleted_at IS NULL
	AND platform = 'kiro'
	AND type = 'oauth'
	AND status = 'active'
`

// ListKiroUsageRefreshCandidates 返回需要后台刷新额度的 Kiro 账号（不加载凭据）。
func (r *accountRepository) ListKiroUsageRefreshCandidates(ctx context.Context) ([]service.KiroUsageRefreshCandidate, error) {
	if r == nil || r.sql == nil {
		return nil, errors.New("account repository SQL executor not configured")
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, last_used_at, COALESCE(extra -> 'kiro_usage_snapshot', 'null'::jsonb)::text
		FROM accounts
		WHERE `+kiroUsageRefreshEligibleSQL+`
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.KiroUsageRefreshCandidate, 0)
	for rows.Next() {
		var (
			candidate service.KiroUsageRefreshCandidate
			lastUsed  *time.Time
			raw       string
		)
		if err := rows.Scan(&candidate.ID, &lastUsed, &raw); err != nil {
			return nil, err
		}
		if lastUsed != nil {
			ts := lastUsed.UTC()
			candidate.LastUsedAt = &ts
		}
		candidate.Snapshot = service.DecodeKiroUsageSnapshotJSON(raw)
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateKiroUsageSnapshot 只替换 extra.kiro_usage_snapshot。expectedLastAttempt 做乐观并发控制：
// 快照在读出后被别的刷新覆盖、或被凭据变更清掉时返回 ErrKiroUsageSnapshotStale。
func (r *accountRepository) UpdateKiroUsageSnapshot(
	ctx context.Context,
	accountID int64,
	snapshot *service.KiroUsageSnapshot,
	expectedLastAttempt *time.Time,
) error {
	if snapshot == nil {
		return service.ErrAccountNilInput
	}
	if r == nil || r.sql == nil {
		return errors.New("account repository SQL executor not configured")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	var expected any
	if expectedLastAttempt != nil {
		expected = expectedLastAttempt.UTC().Format(time.RFC3339Nano)
	}
	result, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object('kiro_usage_snapshot', $1::jsonb)
		WHERE id = $2
			AND deleted_at IS NULL
			AND platform = 'kiro'
			AND type = 'oauth'
			AND CASE
				WHEN $3::text IS NULL THEN jsonb_typeof(extra -> 'kiro_usage_snapshot') IS DISTINCT FROM 'object'
				ELSE extra -> 'kiro_usage_snapshot' ->> 'last_attempt_at' = $3::text
			END
	`, string(encoded), accountID, expected)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrKiroUsageSnapshotStale
	}
	return nil
}

// RequestKiroUsageRefreshAll 给已有快照的候选账号打上 refresh_requested_at；
// 没有快照的账号本来就会被立即刷新，不需要标记。
func (r *accountRepository) RequestKiroUsageRefreshAll(ctx context.Context, at time.Time) (int64, error) {
	if r == nil || r.sql == nil {
		return 0, errors.New("account repository SQL executor not configured")
	}
	result, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET extra = jsonb_set(
			extra,
			'{kiro_usage_snapshot,refresh_requested_at}',
			to_jsonb($1::text),
			true
		)
		WHERE `+kiroUsageRefreshEligibleSQL+`
			AND jsonb_typeof(extra -> 'kiro_usage_snapshot') = 'object'
	`, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

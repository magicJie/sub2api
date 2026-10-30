//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func kiroRetireAccount(t *testing.T, ctx context.Context, tx sqlQueryer, platform, extra string, resetAt *time.Time, deleted bool) int64 {
	t.Helper()
	var id int64
	var deletedAt any
	if deleted {
		deletedAt = time.Now()
	}
	require.NoError(t, scanSingleRow(ctx, tx, `
		INSERT INTO accounts (name, platform, type, credentials, extra, status, rate_limited_at, rate_limit_reset_at, deleted_at, created_at, updated_at)
		VALUES ('kiro-retire', $1, 'oauth', '{}', $2::jsonb, 'active', $3, $4, $5, NOW(), NOW())
		RETURNING id`, []any{platform, extra, time.Date(2026, 10, 27, 4, 2, 0, 0, time.UTC), resetAt, deletedAt}, &id))
	return id
}

// TestKiroQuotaRejectedBackfill261 校验 261 号迁移的筛选条件与写入格式，以及可退役列表。
func TestKiroQuotaRejectedBackfill261(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	future := time.Now().Add(48 * time.Hour)
	past := time.Now().Add(-time.Hour)
	full := `{"kiro_usage_snapshot":{"kiro_credit":{"percentage_used":100}},"keep":1}`
	partial := `{"kiro_usage_snapshot":{"kiro_credit":{"percentage_used":99.5}}}`
	marked := `{"kiro_usage_snapshot":{"kiro_credit":{"percentage_used":100}},"kiro_quota_rejected_at":"2026-10-29T00:00:00Z","kiro_quota_rejected_reason":"402 x"}`

	target := kiroRetireAccount(t, ctx, tx, "kiro", full, &future, false)
	notFull := kiroRetireAccount(t, ctx, tx, "kiro", partial, &future, false)
	expired := kiroRetireAccount(t, ctx, tx, "kiro", full, &past, false)
	deleted := kiroRetireAccount(t, ctx, tx, "kiro", full, &future, true)
	otherPlatform := kiroRetireAccount(t, ctx, tx, "openai", full, &future, false)
	already := kiroRetireAccount(t, ctx, tx, "kiro", marked, &future, false)

	content, err := dbmigrations.FS.ReadFile("261_kiro_quota_rejected_backfill.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err)

	marker := func(id int64) (string, string, string) {
		var at, reason, keep string
		require.NoError(t, scanSingleRow(ctx, tx, `SELECT COALESCE(extra->>'kiro_quota_rejected_at',''), COALESCE(extra->>'kiro_quota_rejected_reason',''), COALESCE(extra->>'keep','')
			FROM accounts WHERE id = $1`, []any{id}, &at, &reason, &keep))
		return at, reason, keep
	}
	at, reason, keep := marker(target)
	require.Equal(t, "1", keep, "其他 extra 键保留")
	require.Contains(t, reason, "backfill 261")
	parsed, _ := service.KiroQuotaRejectionFromExtra(map[string]any{service.KiroQuotaRejectedAtExtraKey: at})
	require.NotNil(t, parsed, "格式须能被网关解析: %q", at)
	require.True(t, parsed.Equal(time.Date(2026, 10, 27, 4, 2, 0, 0, time.UTC)), "标记时间取 rate_limited_at")

	for _, id := range []int64{notFull, expired, deleted, otherPlatform} {
		at, _, _ := marker(id)
		require.Empty(t, at, "account %d 不应被补标记", id)
	}
	at, reason, _ = marker(already)
	require.Equal(t, "2026-10-29T00:00:00Z", at, "已有标记不覆盖")
	require.Equal(t, "402 x", reason)

	repo := &accountRepository{sql: tx}
	list, err := repo.ListKiroRetirableAccounts(ctx)
	require.NoError(t, err)
	ids := map[int64]bool{}
	for _, it := range list {
		ids[it.ID] = true
	}
	require.True(t, ids[target])
	require.True(t, ids[already])
	require.False(t, ids[deleted])
	require.False(t, ids[otherPlatform])
}

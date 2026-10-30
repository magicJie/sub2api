package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKiroQuotaRejectedBackfillMigration 校验 261 号迁移只补标记：限定 kiro、未删除、无标记、仍限流、用量满。
func TestKiroQuotaRejectedBackfillMigration(t *testing.T) {
	content, err := FS.ReadFile("261_kiro_quota_rejected_backfill.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "WHERE platform = 'kiro' AND deleted_at IS NULL")
	require.Contains(t, sql, "NOT (COALESCE(extra, '{}'::jsonb) ? 'kiro_quota_rejected_at')")
	require.Contains(t, sql, "rate_limit_reset_at > NOW()")
	require.Contains(t, sql, "::numeric >= 100")
	require.NotContains(t, sql, "updated_at =")
	require.NotContains(t, sql, "DELETE")
}

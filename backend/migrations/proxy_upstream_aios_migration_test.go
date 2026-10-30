package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProxyUpstreamAIOsMigration 校验 259 号迁移：端点 id 不变（槽位引用保持），按 host+protocol+凭据归并到 AIO。
func TestProxyUpstreamAIOsMigration(t *testing.T) {
	content, err := FS.ReadFile("259_proxy_upstream_aios.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS proxy_upstream_aios")
	require.Contains(t, sql, "ALTER TABLE proxy_upstream_endpoints ADD COLUMN IF NOT EXISTS aio_id BIGINT")
	require.Contains(t, sql, "GROUP BY protocol, host, username, password")
	require.Contains(t, sql, "a.protocol = e.protocol AND a.host = e.host AND a.username = e.username AND a.password = e.password")
	require.Contains(t, sql, "ALTER COLUMN aio_id SET NOT NULL")
	require.Contains(t, sql, "ON DELETE RESTRICT")
	// 不得改写槽位、端点 id 或删除冗余列（回滚需要）。
	require.NotContains(t, sql, "UPDATE proxy_upstream_slots")
	require.NotContains(t, sql, "DROP COLUMN")
	require.NotContains(t, sql, "DELETE FROM")
}

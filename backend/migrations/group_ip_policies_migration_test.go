package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGroupIPPoliciesMigration 校验 260 号迁移：分组策略单独建表，不改动平台策略表（旧版本回滚依赖其唯一约束）。
func TestGroupIPPoliciesMigration(t *testing.T) {
	content, err := FS.ReadFile("260_group_ip_policies.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS group_ip_policies")
	require.Contains(t, sql, "group_id BIGINT NOT NULL UNIQUE REFERENCES groups(id) ON DELETE CASCADE")
	require.NotContains(t, sql, "ALTER TABLE platform_ip_policies")
	require.NotContains(t, sql, "DROP INDEX")
}

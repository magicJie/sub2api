package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProxyHopGroupsLoadBalanceMigration 校验 253 号迁移可重入地放宽首跳组策略 CHECK。
func TestProxyHopGroupsLoadBalanceMigration(t *testing.T) {
	content, err := FS.ReadFile("253_proxy_hop_groups_load_balance.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS proxy_hop_groups_strategy_check")
	require.Contains(t, sql, "CHECK (strategy IN ('fallback', 'url-test', 'load-balance'))")
}

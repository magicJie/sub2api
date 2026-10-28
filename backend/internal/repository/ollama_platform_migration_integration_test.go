//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"testing/fstest"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const ollamaPlatformMigration = "256_ollama_platform.sql"

// These tests use the repository's real PostgreSQL/testcontainers harness. Its
// bootstrap runs every embedded migration, including the real shadow, proxy,
// scheduler and auth-cache triggers; no reduced schema or trigger stubs are used.
// Isolated setup + execution (from backend/, Docker required):
//
//	docker pull postgres:15-alpine
//	docker pull redis:8.4-alpine
//	CI=1 SUB2API_TEST_POSTGRES_IMAGE=postgres:15-alpine go test -tags integration ./internal/repository -run '^TestMigration256' -count=1 -v
//
// Repeat with postgres:18.1-alpine3.23 for the current harness default.
func migration256SQL(t *testing.T) string {
	t.Helper()
	content, err := dbmigrations.FS.ReadFile(ollamaPlatformMigration)
	require.NoError(t, err)
	return string(content)
}

func migration256Exec(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	_, err := tx.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

func migration256ID(t *testing.T, tx *sql.Tx, query string, args ...any) int64 {
	t.Helper()
	var id int64
	require.NoError(t, tx.QueryRowContext(context.Background(), query, args...).Scan(&id))
	return id
}

func migration256Account(t *testing.T, tx *sql.Tx, platform, key, baseURL, status string) int64 {
	t.Helper()
	credentials, err := json.Marshal(map[string]any{
		"api_key": key, "base_url": baseURL,
		"model_mapping":         map[string]string{"client-model": "upstream-model"},
		"openai_responses_mode": "force_chat_completions",
	})
	require.NoError(t, err)
	return migration256ID(t, tx, `INSERT INTO accounts (name,platform,type,status,credentials,extra)
VALUES ('migration-256-account',$1,'apikey',$2,$3::jsonb,'{"retained":{"custom":true}}') RETURNING id`, platform, status, string(credentials))
}

func migration256Group(t *testing.T, tx *sql.Tx, platform, suffix string, ids ...int64) int64 {
	t.Helper()
	id := migration256ID(t, tx, `INSERT INTO groups (name,platform,mcp_xml_inject)
VALUES ($1,$2,true) RETURNING id`, "migration-256-"+suffix, platform)
	for _, accountID := range ids {
		migration256Exec(t, tx, `INSERT INTO account_groups (account_id,group_id,priority) VALUES ($1,$2,17)`, accountID, id)
	}
	return id
}

func migration256String(t *testing.T, tx *sql.Tx, query string, args ...any) string {
	t.Helper()
	var value string
	require.NoError(t, tx.QueryRowContext(context.Background(), query, args...).Scan(&value))
	return value
}

func migration256Snapshot(t *testing.T, tx *sql.Tx) string {
	t.Helper()
	return migration256String(t, tx, `SELECT jsonb_build_object(
'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
'groups',(SELECT jsonb_agg(to_jsonb(g) ORDER BY id) FROM groups g),
'memberships',(SELECT jsonb_agg(to_jsonb(a) ORDER BY account_id,group_id) FROM account_groups a),
'bindings',(SELECT jsonb_agg(to_jsonb(b) ORDER BY id) FROM proxy_account_bindings b),
'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM platform_ip_policies p),
'channels',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM channels c),
'pricing',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_model_pricing p),
'cost_pricing',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_account_stats_model_pricing p),
'plugins',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM sub2api_plugin_bindings p),
'proxy_pools',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM proxy_pools p),
'default_grants',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM user_provider_default_grants p),
'routes',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM composite_model_routes r),
'quotas',(SELECT jsonb_agg(to_jsonb(q) ORDER BY id) FROM user_platform_quotas q),
'settings',(SELECT jsonb_agg(to_jsonb(s) ORDER BY key) FROM settings s),
'rules',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM error_passthrough_rules r),
'monitor',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM channel_monitor_v2_config c)
)::text`)
}

func migration256Reject(t *testing.T, tx *sql.Tx, message string, ids ...int64) {
	t.Helper()
	before := migration256Snapshot(t, tx)
	migration256Exec(t, tx, "SAVEPOINT migration_256_failure")
	_, err := tx.ExecContext(context.Background(), migration256SQL(t))
	require.ErrorContains(t, err, message)
	for _, id := range ids {
		require.Contains(t, err.Error(), fmt.Sprint(id))
	}
	migration256Exec(t, tx, "ROLLBACK TO SAVEPOINT migration_256_failure")
	require.JSONEq(t, before, migration256Snapshot(t, tx), "failed preflight must preserve all permanent data")
}

func TestMigration256PureGroupsRetainAccessProxyAndExtras(t *testing.T) {
	tx := testTx(t)
	migration256Exec(t, tx, `DELETE FROM platform_ip_policies WHERE platform IN ('openai','ollama')`)
	migration256Exec(t, tx, `INSERT INTO platform_ip_policies (platform,mode,pool_ids,max_lifetime_accounts,max_active_accounts,on_egress_failure)
VALUES ('openai','churn','[7,9]',5,2,'reassign')`)
	proxyID := migration256ID(t, tx, `INSERT INTO proxies (name,protocol,host,port) VALUES ('migration-256-proxy','http','127.0.0.1',18080) RETURNING id`)
	ids := []int64{
		migration256Account(t, tx, "openai", "fixture-key-58", "https://ollama.com", "active"),
		migration256Account(t, tx, "openai", "fixture-key-59", " https://www.OLLAMA.com:443/v1 ", "active"),
		migration256Account(t, tx, "openai", "fixture-key-66", "https://ollama.com/v1", "active"),
	}
	groupID := migration256Group(t, tx, "openai", "production-shape", ids...)
	userID := migration256ID(t, tx, `INSERT INTO users (email,password_hash) VALUES ('migration-256@example.invalid','fixture') RETURNING id`)
	migration256Exec(t, tx, `INSERT INTO user_allowed_groups (user_id,group_id) VALUES ($1,$2)`, userID, groupID)
	for i := range 2 {
		migration256Exec(t, tx, `INSERT INTO api_keys (user_id,key,name,group_id) VALUES ($1,$2,'fixture',$3)`, userID, fmt.Sprintf("migration-256-client-%d", i), groupID)
	}
	for _, id := range ids {
		migration256Exec(t, tx, `UPDATE accounts SET proxy_id=$1 WHERE id=$2`, proxyID, id)
		migration256Exec(t, tx, `UPDATE proxy_account_bindings SET locked=true,source='pool',pool_id=(SELECT id FROM proxy_pools WHERE is_default) WHERE account_id=$1`, id)
	}
	bindingsBefore := migration256String(t, tx, `SELECT jsonb_agg(to_jsonb(b)-'platform' ORDER BY id)::text FROM proxy_account_bindings b`)
	accountsBefore := migration256String(t, tx, `SELECT jsonb_agg((to_jsonb(a)-'platform'-'updated_at'-'credentials') || jsonb_build_object('credentials',a.credentials-'base_url') ORDER BY id)::text FROM accounts a`)
	migration256Exec(t, tx, migration256SQL(t))
	require.Equal(t, int64(3), migration256ID(t, tx, `SELECT count(*) FROM accounts WHERE platform='ollama'`))
	require.Equal(t, "ollama", migration256String(t, tx, `SELECT platform FROM groups WHERE id=$1`, groupID))
	require.Equal(t, int64(2), migration256ID(t, tx, `SELECT count(*) FROM api_keys WHERE group_id=$1 AND status='active'`, groupID))
	require.Equal(t, int64(1), migration256ID(t, tx, `SELECT count(*) FROM user_allowed_groups WHERE user_id=$1 AND group_id=$2`, userID, groupID))
	require.Equal(t, "true", migration256String(t, tx, `SELECT mcp_xml_inject::text FROM groups WHERE id=$1`, groupID))
	require.JSONEq(t, bindingsBefore, migration256String(t, tx, `SELECT jsonb_agg(to_jsonb(b)-'platform' ORDER BY id)::text FROM proxy_account_bindings b`))
	require.JSONEq(t, accountsBefore, migration256String(t, tx, `SELECT jsonb_agg((to_jsonb(a)-'platform'-'updated_at'-'credentials') || jsonb_build_object('credentials',a.credentials-'base_url') ORDER BY id)::text FROM accounts a`))
	require.Equal(t, int64(3), migration256ID(t, tx, `SELECT count(*) FROM accounts WHERE platform='ollama' AND credentials->>'base_url'='https://ollama.com/v1'`))
	require.Equal(t, int64(3), migration256ID(t, tx, `SELECT count(*) FROM proxy_account_bindings WHERE platform='ollama' AND locked`))
	require.JSONEq(t, `{"mode":"sticky","pool_ids":[7,9],"on_egress_failure":"pause","max_lifetime_accounts":5,"max_active_accounts":2}`,
		migration256String(t, tx, `SELECT (to_jsonb(p)-'id'-'platform'-'created_at'-'updated_at')::text FROM platform_ip_policies p WHERE platform='ollama'`))
	beforeRerun := migration256Snapshot(t, tx)
	migration256Exec(t, tx, migration256SQL(t))
	require.JSONEq(t, beforeRerun, migration256Snapshot(t, tx))
}

func TestMigration256SiblingWinnerAndMembershipUnion(t *testing.T) {
	tx := testTx(t)
	older := migration256Account(t, tx, "anthropic", "fixture-shared-key", "https://ollama.com", "error")
	winner := migration256Account(t, tx, "openai", "fixture-shared-key", "https://ollama.com/v1", "active")
	later := migration256Account(t, tx, "kimi", "fixture-shared-key", "https://ollama.com", "active")
	g1 := migration256Group(t, tx, "anthropic", "sibling-1", older)
	g2 := migration256Group(t, tx, "openai", "sibling-2", winner)
	g3 := migration256Group(t, tx, "kimi", "sibling-3", later, winner)
	migration256Exec(t, tx, `UPDATE account_groups SET priority=4 WHERE account_id=$1 AND group_id=$2`, later, g3)
	migration256Exec(t, tx, migration256SQL(t))
	for _, id := range []int64{older, later} {
		require.Equal(t, "disabled", migration256String(t, tx, `SELECT status FROM accounts WHERE id=$1`, id))
		require.Equal(t, fmt.Sprint(winner), migration256String(t, tx, `SELECT extra->>'migrated_to_account_id' FROM accounts WHERE id=$1`, id))
		require.Equal(t, "true", migration256String(t, tx, `SELECT extra#>>'{retained,custom}' FROM accounts WHERE id=$1`, id))
	}
	require.Equal(t, "active", migration256String(t, tx, `SELECT status FROM accounts WHERE id=$1`, winner))
	for _, groupID := range []int64{g1, g2, g3} {
		require.Equal(t, int64(1), migration256ID(t, tx, `SELECT count(*) FROM account_groups WHERE account_id=$1 AND group_id=$2`, winner, groupID))
	}
	require.Equal(t, int64(4), migration256ID(t, tx, `SELECT priority FROM account_groups WHERE account_id=$1 AND group_id=$2`, winner, g3))
	require.Equal(t, int64(5), migration256ID(t, tx, `SELECT count(*) FROM account_groups`), "original sibling relationships remain")
	beforeRerun := migration256Snapshot(t, tx)
	migration256Exec(t, tx, migration256SQL(t))
	require.JSONEq(t, beforeRerun, migration256Snapshot(t, tx))
}

func TestMigration256SelectionBoundaryAndDeletedMemberships(t *testing.T) {
	tx := testTx(t)
	candidate := migration256Account(t, tx, "deepseek", "selected", "HTTPS://WWW.OLLAMA.COM:443/v1", "active")
	for i, url := range []string{"https://ollama.com/", "http://ollama.com", "https://ollama.com.evil.invalid", "https://ollama.com/v1/chat/completions", "https://ollama.com:444"} {
		migration256Account(t, tx, "openai", fmt.Sprintf("unselected-%d", i), url, "active")
	}
	deleted := migration256Account(t, tx, "openai", "deleted", "https://example.invalid", "active")
	migration256Exec(t, tx, `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, deleted)
	groupID := migration256Group(t, tx, "deepseek", "deleted-membership", candidate, deleted)
	migration256Account(t, tx, "gemini", "unsupported", "https://ollama.com", "active")
	oauth := migration256Account(t, tx, "openai", "oauth", "https://ollama.com", "active")
	migration256Exec(t, tx, `UPDATE accounts SET type='oauth' WHERE id=$1`, oauth)
	migration256Exec(t, tx, migration256SQL(t))
	require.Equal(t, int64(1), migration256ID(t, tx, `SELECT count(*) FROM accounts WHERE platform='ollama'`))
	require.Equal(t, "ollama", migration256String(t, tx, `SELECT platform FROM groups WHERE id=$1`, groupID))
	require.Equal(t, "openai", migration256String(t, tx, `SELECT platform FROM accounts WHERE id=$1`, deleted))
}

func TestMigration256LinkedChannelDataRulesAndRoutes(t *testing.T) {
	tx := testTx(t)
	accountID := migration256Account(t, tx, "openai", "linked-key", "https://ollama.com", "active")
	groupID := migration256Group(t, tx, "openai", "channel", accountID)
	channelID := migration256ID(t, tx, `INSERT INTO channels (name,model_mapping) VALUES ('migration-256-channel','{"openai":{"client":"upstream"},"gemini":{"other":"retained"}}') RETURNING id`)
	migration256Exec(t, tx, `INSERT INTO channel_groups (channel_id,group_id) VALUES ($1,$2)`, channelID, groupID)
	pricingID := migration256ID(t, tx, `INSERT INTO channel_model_pricing (channel_id,platform,models,input_price) VALUES ($1,'openai','["model"]',0.000123) RETURNING id`, channelID)
	intervalID := migration256ID(t, tx, `INSERT INTO channel_pricing_intervals (pricing_id,min_tokens,max_tokens,input_price) VALUES ($1,0,4096,0.000456) RETURNING id`, pricingID)
	costRuleID := migration256ID(t, tx, `INSERT INTO channel_account_stats_pricing_rules (channel_id,group_ids) VALUES ($1,ARRAY[$2::bigint]) RETURNING id`, channelID, groupID)
	costPricingID := migration256ID(t, tx, `INSERT INTO channel_account_stats_model_pricing (rule_id,platform,models,input_price) VALUES ($1,'openai','["model"]',0.000789) RETURNING id`, costRuleID)
	accountCostRuleID := migration256ID(t, tx, `INSERT INTO channel_account_stats_pricing_rules (channel_id,account_ids) VALUES ($1,ARRAY[$2::bigint]) RETURNING id`, channelID, accountID)
	accountCostPricingID := migration256ID(t, tx, `INSERT INTO channel_account_stats_model_pricing (rule_id,platform,models,input_price) VALUES ($1,'openai','["model"]',0.000321) RETURNING id`, accountCostRuleID)
	routeID := migration256ID(t, tx, `INSERT INTO composite_model_routes (group_id,public_model,target_platform,upstream_model) VALUES ($1,'client','openai','upstream') RETURNING id`, groupID)
	ruleID := migration256ID(t, tx, `INSERT INTO error_passthrough_rules (name,platforms) VALUES ('migration-256-rule','["openai","gemini"]') RETURNING id`)
	migration256Exec(t, tx, `INSERT INTO error_passthrough_rules (name,platforms) VALUES ('migration-256-global','[]'),('migration-256-other','["gemini"]')`)
	migration256Exec(t, tx, `UPDATE channel_monitor_v2_config SET platforms='[{"platform":"openai","enabled":false,"models":["custom-model"]}]' WHERE id=1`)
	intervalBefore := migration256String(t, tx, `SELECT to_jsonb(p)::text FROM channel_pricing_intervals p WHERE id=$1`, intervalID)
	migration256Exec(t, tx, migration256SQL(t))
	require.Equal(t, "ollama", migration256String(t, tx, `SELECT platform FROM channel_model_pricing WHERE id=$1`, pricingID))
	require.Equal(t, "ollama", migration256String(t, tx, `SELECT platform FROM channel_account_stats_model_pricing WHERE id=$1`, costPricingID))
	require.Equal(t, "ollama", migration256String(t, tx, `SELECT platform FROM channel_account_stats_model_pricing WHERE id=$1`, accountCostPricingID))
	require.Equal(t, "true", migration256String(t, tx, `SELECT (input_price=0.000321)::text FROM channel_account_stats_model_pricing WHERE id=$1`, accountCostPricingID))
	require.JSONEq(t, intervalBefore, migration256String(t, tx, `SELECT to_jsonb(p)::text FROM channel_pricing_intervals p WHERE id=$1`, intervalID))
	require.JSONEq(t, `{"ollama":{"client":"upstream"},"gemini":{"other":"retained"}}`, migration256String(t, tx, `SELECT model_mapping::text FROM channels WHERE id=$1`, channelID))
	require.Equal(t, "ollama", migration256String(t, tx, `SELECT target_platform FROM composite_model_routes WHERE id=$1`, routeID))
	require.JSONEq(t, `["openai","gemini","ollama"]`, migration256String(t, tx, `SELECT platforms::text FROM error_passthrough_rules WHERE id=$1`, ruleID))
	require.JSONEq(t, `[{"platform":"openai","enabled":false,"models":["custom-model"]},{"platform":"ollama","enabled":false,"models":["custom-model"]}]`, migration256String(t, tx, `SELECT platforms::text FROM channel_monitor_v2_config WHERE id=1`))
	beforeRerun := migration256Snapshot(t, tx)
	migration256Exec(t, tx, migration256SQL(t))
	require.JSONEq(t, beforeRerun, migration256Snapshot(t, tx))
}

func TestMigration256WholePlatformQuotaRequiresCounterReconciliation(t *testing.T) {
	tx := testTx(t)
	accountID := migration256Account(t, tx, "minimax", "quota-key", "https://ollama.com", "active")
	migration256Group(t, tx, "minimax", "quota", accountID)
	userID := migration256ID(t, tx, `INSERT INTO users (email,password_hash) VALUES ('migration-256-quota@example.invalid','fixture') RETURNING id`)
	quotaID := migration256ID(t, tx, `INSERT INTO user_platform_quotas (user_id,platform,daily_limit_usd,daily_usage_usd,daily_window_start)
VALUES ($1,'minimax',10,7,'2026-10-28T00:00:00Z') RETURNING id`, userID)
	migration256Reject(t, tx, "platform quota IDs", quotaID)
}

func TestMigration256DefaultQuotaGrantMovesForExclusivePlatform(t *testing.T) {
	tx := testTx(t)
	accountID := migration256Account(t, tx, "minimax", "grant-key", "https://ollama.com", "active")
	migration256Group(t, tx, "minimax", "quota-grant", accountID)
	migration256Exec(t, tx, `INSERT INTO settings (key,value) VALUES ('default_platform_quotas','{"minimax":{"daily":10},"gemini":{"monthly":99}}'),
('auth_source_default_email_platform_quotas','{"minimax":{"monthly":25}}') ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`)
	migration256Exec(t, tx, migration256SQL(t))
	require.JSONEq(t, `{"ollama":{"daily":10},"gemini":{"monthly":99}}`, migration256String(t, tx, `SELECT value FROM settings WHERE key='default_platform_quotas'`))
	require.JSONEq(t, `{"ollama":{"monthly":25}}`, migration256String(t, tx, `SELECT value FROM settings WHERE key='auth_source_default_email_platform_quotas'`))
}

func TestMigration256PolicyDefaultAndExistingOverride(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			tx := testTx(t)
			migration256Exec(t, tx, `DELETE FROM platform_ip_policies WHERE platform IN ('openai','ollama')`)
			if existing {
				migration256Exec(t, tx, `INSERT INTO platform_ip_policies (platform,mode,pool_ids,max_active_accounts,on_egress_failure) VALUES ('ollama','sticky','[42]',3,'pause')`)
			}
			migration256Exec(t, tx, migration256SQL(t))
			if existing {
				require.Equal(t, "3", migration256String(t, tx, `SELECT max_active_accounts::text FROM platform_ip_policies WHERE platform='ollama'`))
				require.JSONEq(t, `[42]`, migration256String(t, tx, `SELECT pool_ids::text FROM platform_ip_policies WHERE platform='ollama'`))
			} else {
				require.Equal(t, "sticky", migration256String(t, tx, `SELECT mode FROM platform_ip_policies WHERE platform='ollama'`))
				require.JSONEq(t, `[]`, migration256String(t, tx, `SELECT pool_ids::text FROM platform_ip_policies WHERE platform='ollama'`))
			}
		})
	}
}

func TestMigration256UnrelatedConfigurationStaysIntact(t *testing.T) {
	tx := testTx(t)
	accountID := migration256Account(t, tx, "openai", "unrelated-key", "https://ollama.com", "active")
	groupID := migration256Group(t, tx, "openai", "no-config", accountID)
	// A shared channel with no applicable platform config has no ambiguity.
	otherGroup := migration256Group(t, tx, "openai", "no-config-other")
	channelID := migration256ID(t, tx, `INSERT INTO channels (name) VALUES ('migration-256-empty-channel') RETURNING id`)
	migration256Exec(t, tx, `INSERT INTO channel_groups (channel_id,group_id) VALUES ($1,$2),($1,$3)`, channelID, groupID, otherGroup)
	migration256Exec(t, tx, `UPDATE proxy_pools SET platform_limits='{"openai":3,"kiro":5}' WHERE is_default`)
	pluginID := migration256ID(t, tx, `INSERT INTO sub2api_plugin_installations (plugin_key,name,version,manifest,artifact_path,install_path,binary_path,binary_sha256)
VALUES ('fixture-oauth','fixture','1','{"capabilities":[{"id":"openai.oauth.outbound_transport.v1","platform":"openai","account_type":"oauth"}]}','/fixture','/fixture','/fixture','fixture') RETURNING id`)
	bindingID := migration256ID(t, tx, `INSERT INTO sub2api_plugin_bindings (plugin_id,capability,platform,account_type,enabled)
VALUES ($1,'openai.oauth.outbound_transport.v1','openai','oauth',true) RETURNING id`, pluginID)
	userID := migration256ID(t, tx, `INSERT INTO users (email,password_hash) VALUES ('migration-256-grant@example.invalid','fixture') RETURNING id`)
	grantID := migration256ID(t, tx, `INSERT INTO user_provider_default_grants (user_id,provider_type,grant_reason) VALUES ($1,'email','signup') RETURNING id`, userID)
	bindingBefore := migration256String(t, tx, `SELECT to_jsonb(b)::text FROM sub2api_plugin_bindings b WHERE id=$1`, bindingID)
	grantBefore := migration256String(t, tx, `SELECT to_jsonb(g)::text FROM user_provider_default_grants g WHERE id=$1`, grantID)
	migration256Exec(t, tx, migration256SQL(t))
	require.Equal(t, "ollama", migration256String(t, tx, `SELECT platform FROM groups WHERE id=$1`, groupID))
	require.Equal(t, "openai", migration256String(t, tx, `SELECT platform FROM groups WHERE id=$1`, otherGroup))
	require.JSONEq(t, `{"openai":3,"kiro":5}`, migration256String(t, tx, `SELECT platform_limits::text FROM proxy_pools WHERE is_default`))
	require.JSONEq(t, bindingBefore, migration256String(t, tx, `SELECT to_jsonb(b)::text FROM sub2api_plugin_bindings b WHERE id=$1`, bindingID))
	require.JSONEq(t, grantBefore, migration256String(t, tx, `SELECT to_jsonb(g)::text FROM user_provider_default_grants g WHERE id=$1`, grantID))
}

func TestMigration256PreflightFailuresAreAtomic(t *testing.T) {
	cases := []struct {
		name    string
		message string
		setup   func(*testing.T, *sql.Tx, int64, int64) []int64
	}{
		{"mixed_groups", "mixed group IDs", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			other := migration256Account(t, tx, "openai", "non-ollama", "https://api.openai.com", "disabled")
			migration256Exec(t, tx, `INSERT INTO account_groups (account_id,group_id) VALUES ($1,$2)`, other, groupID)
			second := migration256Group(t, tx, "openai", "mixed-second", accountID, other)
			return []int64{groupID, second}
		}},
		{"parent", "shadow/parent", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			migration256Exec(t, tx, `INSERT INTO accounts (name,platform,type,parent_account_id,quota_dimension) VALUES ('fixture-shadow','openai','apikey',$1,'spark')`, accountID)
			return []int64{accountID}
		}},
		{"candidate_shadow", "shadow/parent", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			parent := migration256Account(t, tx, "openai", "parent", "https://api.openai.com", "active")
			migration256Exec(t, tx, `UPDATE accounts SET parent_account_id=$1,quota_dimension='spark' WHERE id=$2`, parent, accountID)
			return []int64{accountID}
		}},
		{"different_mapping", "incompatible sibling", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			other := migration256Account(t, tx, "anthropic", "failure-key", "https://ollama.com", "active")
			migration256Exec(t, tx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}','{"client-model":"different"}') WHERE id=$1`, other)
			return []int64{other}
		}},
		{"different_proxy", "incompatible sibling", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			other := migration256Account(t, tx, "anthropic", "failure-key", "https://ollama.com", "active")
			proxyID := migration256ID(t, tx, `INSERT INTO proxies (name,protocol,host,port) VALUES ('fixture','http','127.0.0.1',18080) RETURNING id`)
			migration256Exec(t, tx, `UPDATE accounts SET proxy_id=$1 WHERE id=$2`, proxyID, other)
			return []int64{other}
		}},
		{"invalid_key", "invalid api_key/extra", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			migration256Exec(t, tx, `UPDATE accounts SET credentials=credentials-'api_key' WHERE id=$1`, accountID)
			return []int64{accountID}
		}},
		{"existing_ollama", "existing Ollama sibling", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			migration256Account(t, tx, "ollama", "failure-key", "https://ollama.com", "active")
			return []int64{accountID}
		}},
		{"shared_quota", "platform quota IDs", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			migration256Account(t, tx, "openai", "other-pool", "https://api.openai.com", "active")
			userID := migration256ID(t, tx, `INSERT INTO users (email,password_hash) VALUES ('fixture-quota@example.invalid','fixture') RETURNING id`)
			quotaID := migration256ID(t, tx, `INSERT INTO user_platform_quotas (user_id,platform,monthly_limit_usd,monthly_usage_usd) VALUES ($1,'openai',10,9) RETURNING id`, userID)
			return []int64{quotaID}
		}},
		{"default_grant_budget", "shared default quota setting auth_source_default_email_platform_quotas", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			migration256Account(t, tx, "openai", "other-pool", "https://api.openai.com", "active")
			migration256Exec(t, tx, `INSERT INTO settings (key,value) VALUES ('auth_source_default_email_platform_quotas','{"openai":{"daily":0}}') ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`)
			return nil
		}},
		{"shared_channel", "ambiguous channel IDs", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			channelID := migration256ID(t, tx, `INSERT INTO channels (name,model_mapping) VALUES ('fixture-channel','{"openai":{"client":"upstream"}}') RETURNING id`)
			otherGroup := migration256Group(t, tx, "openai", "other-channel-group")
			migration256Exec(t, tx, `INSERT INTO channel_groups (channel_id,group_id) VALUES ($1,$2),($1,$3)`, channelID, groupID, otherGroup)
			return []int64{channelID}
		}},
		{"account_cost_rule", "account-cost pricing rule IDs", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			channelID := migration256ID(t, tx, `INSERT INTO channels (name) VALUES ('fixture-stats-channel') RETURNING id`)
			migration256Exec(t, tx, `INSERT INTO channel_groups (channel_id,group_id) VALUES ($1,$2)`, channelID, groupID)
			otherGroup := migration256Group(t, tx, "openai", "shared-cost")
			migration256Exec(t, tx, `INSERT INTO channel_groups (channel_id,group_id) VALUES ($1,$2)`, channelID, otherGroup)
			ruleID := migration256ID(t, tx, `INSERT INTO channel_account_stats_pricing_rules (channel_id,group_ids) VALUES ($1,ARRAY[$2::bigint]) RETURNING id`, channelID, groupID)
			migration256Exec(t, tx, `INSERT INTO channel_account_stats_model_pricing (rule_id,platform,models,input_price) VALUES ($1,'openai','["model"]',1)`, ruleID)
			return []int64{ruleID}
		}},
		{"composite_pool", "ambiguous composite route IDs", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			composite := migration256Group(t, tx, "composite", "composite")
			routeID := migration256ID(t, tx, `INSERT INTO composite_model_routes (group_id,public_model,target_platform) VALUES ($1,'model','openai') RETURNING id`, composite)
			return []int64{routeID}
		}},
		{"unsupported_plugin", "enabled plugin binding IDs", func(t *testing.T, tx *sql.Tx, accountID, groupID int64) []int64 {
			pluginID := migration256ID(t, tx, `INSERT INTO sub2api_plugin_installations (plugin_key,name,version,artifact_path,install_path,binary_path,binary_sha256) VALUES ('fixture','fixture','1','/fixture','/fixture','/fixture','fixture') RETURNING id`)
			bindingID := migration256ID(t, tx, `INSERT INTO sub2api_plugin_bindings (plugin_id,capability,platform,account_type,enabled) VALUES ($1,'future.apikey.capability','openai','apikey',true) RETURNING id`, pluginID)
			return []int64{bindingID}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := testTx(t)
			accountID := migration256Account(t, tx, "openai", "failure-key", "https://ollama.com", "active")
			groupID := migration256Group(t, tx, "openai", "failure", accountID)
			ids := tc.setup(t, tx, accountID, groupID)
			migration256Reject(t, tx, tc.message, ids...)
		})
	}
}

func TestMigration256RunnerRecordsOnlySuccessfulTransaction(t *testing.T) {
	// Use committed fixtures only for exercising the real runner's independent
	// connection/transaction. Keep the shared test DB clean for all other suites.
	ctx := context.Background()
	var checksum string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE filename=$1`, ollamaPlatformMigration).Scan(&checksum))
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	candidate := migration256Account(t, tx, "openai", "runner-fixture", "https://ollama.com", "active")
	other := migration256Account(t, tx, "openai", "runner-other", "https://api.openai.com", "active")
	groupID := migration256Group(t, tx, "openai", "runner", candidate, other)
	migration256Exec(t, tx, `DELETE FROM schema_migrations WHERE filename=$1`, ollamaPlatformMigration)
	require.NoError(t, tx.Commit())
	t.Cleanup(func() {
		cleanup, cleanupErr := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, cleanupErr)
		defer func() { _ = cleanup.Rollback() }()
		migration256Exec(t, cleanup, `DELETE FROM account_groups WHERE account_id IN ($1,$2)`, candidate, other)
		migration256Exec(t, cleanup, `DELETE FROM accounts WHERE id IN ($1,$2)`, candidate, other)
		migration256Exec(t, cleanup, `DELETE FROM groups WHERE id=$1`, groupID)
		migration256Exec(t, cleanup, `INSERT INTO schema_migrations (filename,checksum) VALUES ($1,$2)
ON CONFLICT (filename) DO UPDATE SET checksum=EXCLUDED.checksum`, ollamaPlatformMigration, checksum)
		require.NoError(t, cleanup.Commit())
	})
	fsys := fstest.MapFS{ollamaPlatformMigration: &fstest.MapFile{Data: []byte(migration256SQL(t))}}
	err = applyMigrationsFS(ctx, integrationDB, fsys)
	require.ErrorContains(t, err, "apply migration "+ollamaPlatformMigration)
	require.ErrorContains(t, err, fmt.Sprintf("mixed group IDs [%d]", groupID))
	var recorded int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE filename=$1`, ollamaPlatformMigration).Scan(&recorded))
	require.Zero(t, recorded)
	var platform string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT platform FROM accounts WHERE id=$1`, candidate).Scan(&platform))
	require.Equal(t, "openai", platform)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, other, groupID)
	require.NoError(t, err)
	require.NoError(t, applyMigrationsFS(ctx, integrationDB, fsys))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE filename=$1`, ollamaPlatformMigration).Scan(&recorded))
	require.Equal(t, 1, recorded)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT platform FROM accounts WHERE id=$1`, candidate).Scan(&platform))
	require.Equal(t, "ollama", platform)
	require.NoError(t, applyMigrationsFS(ctx, integrationDB, fsys), "checksum-recorded migration is skipped on startup")
}

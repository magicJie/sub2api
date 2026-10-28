-- Ollama Cloud cutover. Run after 251 (IP policies) and 242 (platform CHECK removal).
-- The startup runner holds an advisory lock, executes this whole file in one
-- transaction, and records its checksum in that transaction. Any exception rolls
-- back this migration and stops startup; resolve the listed IDs, then restart.
-- For manual execution use psql --single-transaction -v ON_ERROR_STOP=1 -f ... .
-- Locks also exclude account/config writes by an older application instance.
--
-- Rollback: take a full database snapshot before deployment. This migration keeps
-- original account/group IDs, proxies, extras and sibling memberships. Only the
-- legacy base_url spelling is canonicalized; other credential keys stay intact.
-- Exact reverse SQL uses that snapshot restored as schema pre_256 (same schema):
-- BEGIN;
-- UPDATE accounts a SET platform=p.platform,status=p.status,extra=p.extra,
--   credentials=p.credentials,updated_at=p.updated_at FROM pre_256.accounts p WHERE a.id=p.id;
-- UPDATE groups g SET platform=p.platform,updated_at=p.updated_at
--   FROM pre_256.groups p WHERE g.id=p.id;
-- DELETE FROM account_groups ag WHERE NOT EXISTS (SELECT 1 FROM
--   pre_256.account_groups p WHERE (p.account_id,p.group_id)=(ag.account_id,ag.group_id));
-- UPDATE account_groups ag SET priority=p.priority FROM pre_256.account_groups p
--   WHERE (p.account_id,p.group_id)=(ag.account_id,ag.group_id);
-- UPDATE proxy_account_bindings b SET platform=p.platform
--   FROM pre_256.proxy_account_bindings p WHERE b.id=p.id;
-- UPDATE channels c SET model_mapping=p.model_mapping FROM pre_256.channels p WHERE c.id=p.id;
-- UPDATE channel_model_pricing c SET platform=p.platform
--   FROM pre_256.channel_model_pricing p WHERE c.id=p.id;
-- UPDATE channel_account_stats_model_pricing c SET platform=p.platform
--   FROM pre_256.channel_account_stats_model_pricing p WHERE c.id=p.id;
-- UPDATE composite_model_routes c SET target_platform=p.target_platform
--   FROM pre_256.composite_model_routes p WHERE c.id=p.id;
-- UPDATE settings s SET value=p.value FROM pre_256.settings p WHERE s.key=p.key;
-- UPDATE error_passthrough_rules r SET platforms=p.platforms
--   FROM pre_256.error_passthrough_rules p WHERE r.id=p.id;
-- UPDATE channel_monitor_v2_config c SET platforms=p.platforms,version=p.version,
--   updated_at=p.updated_at FROM pre_256.channel_monitor_v2_config p WHERE c.id=p.id;
-- DELETE FROM platform_ip_policies p WHERE p.platform='ollama' AND NOT EXISTS
--   (SELECT 1 FROM pre_256.platform_ip_policies b WHERE b.platform='ollama');
-- DELETE FROM schema_migrations WHERE filename='256_ollama_platform.sql';
-- COMMIT;
-- Run the reverse only during the same maintenance window, before accepting new
-- writes, with the previous binary. For rollback after traffic, restore the full
-- snapshot instead; historical usage/quota accounting cannot be reversed safely.
--
-- Audit: usage/ops/audit/monitor metric rows remain historical. Proxy binding
-- history follows the migrated account, retaining IDs, locks, pool and timestamps.
-- proxy_pools.platform_limits is rollback-only since 251 and remains unchanged.
-- API keys, grants/subscriptions, allowed groups and group settings are ID-based
-- and remain unchanged. user_provider_default_grants.provider_type is the login
-- provider (email/oidc/etc), not an upstream platform. Probe monitors/templates
-- use protocol providers and keep their existing provider. The current plugin
-- capability is pinned to openai/oauth, so those bindings remain unchanged.
-- Unexpected enabled APIKey bindings, shared quota budgets, account-cost rules,
-- ambiguous channels/routes and shadow relationships fail preflight with IDs.
DO $$
DECLARE
    bad_ids text;
    source_platforms text[];
    cfg record;
    quota_json jsonb;
BEGIN
    LOCK TABLE accounts, account_groups, groups, channels, channel_groups,
        channel_model_pricing, channel_account_stats_pricing_rules,
        channel_account_stats_model_pricing, composite_model_routes,
        user_platform_quotas, settings, sub2api_plugin_bindings,
        proxy_account_bindings, platform_ip_policies, error_passthrough_rules,
        channel_monitor_v2_config IN SHARE ROW EXCLUSIVE MODE;

    CREATE TEMP TABLE migration_256_accounts ON COMMIT DROP AS
    SELECT a.id, a.platform AS old_platform,
        first_value(a.id) OVER (
            PARTITION BY a.credentials->>'api_key'
            ORDER BY (a.status = 'active') DESC, a.id
        ) AS winner_id
    FROM accounts a
    WHERE a.deleted_at IS NULL AND a.type = 'apikey'
      AND a.platform IN ('openai','anthropic','kimi','zhipu','deepseek','minimax')
      -- Frozen copy of the pre-cutover SQL eligibility regex, including btrim.
      AND btrim(a.credentials->>'base_url') ~
          '^[hH][tT][tT][pP][sS]://([wW][wW][wW]\.)?[oO][lL][lL][aA][mM][aA]\.[cC][oO][mM](:443)?(/v1)?$';

    SELECT string_agg(a.id::text, ',' ORDER BY a.id) INTO bad_ids
    FROM accounts a JOIN migration_256_accounts m ON m.id=a.id
    WHERE jsonb_typeof(a.credentials->'api_key') IS DISTINCT FROM 'string'
       OR btrim(a.credentials->>'api_key') = ''
       OR jsonb_typeof(a.extra) IS DISTINCT FROM 'object';
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: invalid api_key/extra on account IDs [%]', bad_ids;
    END IF;

    CREATE TEMP TABLE migration_256_groups ON COMMIT DROP AS
    SELECT DISTINCT g.id, g.platform AS old_platform
    FROM groups g JOIN account_groups ag ON ag.group_id=g.id
    JOIN migration_256_accounts m ON m.id=ag.account_id;

    -- Deleted accounts remain attached for audit, but do not make a live group
    -- mixed. Disabled/error accounts are still live members and do make it mixed.
    SELECT string_agg(g.id::text, ',' ORDER BY g.id) INTO bad_ids
    FROM migration_256_groups g
    WHERE EXISTS (
        SELECT 1 FROM account_groups ag JOIN accounts a ON a.id=ag.account_id
        WHERE ag.group_id=g.id AND a.deleted_at IS NULL
          AND NOT EXISTS (SELECT 1 FROM migration_256_accounts m WHERE m.id=a.id)
    );
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: mixed group IDs [%]; separate Ollama members first', bad_ids;
    END IF;

    SELECT string_agg(a.id::text, ',' ORDER BY a.id) INTO bad_ids
    FROM accounts a JOIN migration_256_accounts m ON m.id=a.id
    WHERE a.parent_account_id IS NOT NULL
       OR EXISTS (SELECT 1 FROM accounts child WHERE child.parent_account_id=a.id)
       OR EXISTS (SELECT 1 FROM accounts o WHERE o.deleted_at IS NULL
            AND o.platform='ollama' AND o.credentials->>'api_key'=a.credentials->>'api_key');
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: shadow/parent or existing Ollama sibling account IDs [%]; resolve explicitly', bad_ids;
    END IF;

    -- A merged account must supply the same capabilities and IP to every sibling
    -- group. Divergent mappings, credentials, extras, billing or scheduling config
    -- need an explicit operator decision. Ignore only the OpenAI trigger's inert
    -- false default; every original extra remains stored on its original row.
    SELECT string_agg(a.id::text, ',' ORDER BY a.id) INTO bad_ids
    FROM migration_256_accounts m JOIN accounts a ON a.id=m.id
    JOIN accounts w ON w.id=m.winner_id
    WHERE m.id<>m.winner_id AND (
        (a.credentials-'base_url') IS DISTINCT FROM (w.credentials-'base_url')
        OR (CASE WHEN a.extra->'openai_long_context_billing_enabled'='false'::jsonb
                 THEN a.extra-'openai_long_context_billing_enabled' ELSE a.extra END)
           IS DISTINCT FROM
           (CASE WHEN w.extra->'openai_long_context_billing_enabled'='false'::jsonb
                 THEN w.extra-'openai_long_context_billing_enabled' ELSE w.extra END)
        OR (a.proxy_id,a.proxy_fallback_origin_id,a.concurrency,a.load_factor,
            a.priority,a.rate_multiplier,a.expires_at,a.auto_pause_on_expired,a.schedulable)
           IS DISTINCT FROM
           (w.proxy_id,w.proxy_fallback_origin_id,w.concurrency,w.load_factor,
            w.priority,w.rate_multiplier,w.expires_at,w.auto_pause_on_expired,w.schedulable)
    );
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: incompatible sibling account IDs [%]; reconcile capabilities/config first', bad_ids;
    END IF;

    SELECT array_agg(DISTINCT p) INTO source_platforms FROM (
        SELECT old_platform AS p FROM migration_256_accounts
        UNION SELECT old_platform FROM migration_256_groups
    ) sources;

    -- Default grants can move only when their entire source platform migrates.
    -- Live quota rows additionally have platform-keyed Redis counters: shutdown
    -- performs only a bounded flush, so even an exclusive pool cannot be renamed
    -- safely here. Keep those rows intact and require counter reconciliation.
    CREATE TEMP TABLE migration_256_quota_platforms ON COMMIT DROP AS
    SELECT p AS old_platform FROM unnest(source_platforms) p
    WHERE NOT EXISTS (SELECT 1 FROM accounts a WHERE a.platform=p AND a.deleted_at IS NULL
                      AND NOT EXISTS (SELECT 1 FROM migration_256_accounts m WHERE m.id=a.id))
      AND NOT EXISTS (SELECT 1 FROM groups g WHERE g.platform=p AND g.deleted_at IS NULL
                      AND NOT EXISTS (SELECT 1 FROM migration_256_groups m WHERE m.id=g.id));
    SELECT string_agg(q.id::text, ',' ORDER BY q.id) INTO bad_ids
    FROM user_platform_quotas q WHERE q.deleted_at IS NULL
      AND q.platform=ANY(source_platforms);
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: live platform quota IDs [%]; reconcile Redis counters and allocate Ollama budgets explicitly first', bad_ids;
    END IF;
    CREATE TEMP TABLE migration_256_quota_settings (key text, value jsonb) ON COMMIT DROP;
    FOR cfg IN SELECT key,value FROM settings
        WHERE cardinality(source_platforms)>0 AND (key='default_platform_quotas'
           OR key LIKE 'auth_source_default_%_platform_quotas')
    LOOP
        BEGIN
            quota_json := COALESCE(NULLIF(btrim(cfg.value),'')::jsonb,'{}'::jsonb);
        EXCEPTION WHEN invalid_text_representation THEN
            RAISE EXCEPTION 'Ollama migration: malformed quota setting %', cfg.key;
        END;
        IF jsonb_typeof(quota_json)<>'object' THEN
            RAISE EXCEPTION 'Ollama migration: malformed quota setting %', cfg.key;
        END IF;
        IF EXISTS (SELECT 1 FROM jsonb_each(quota_json) p
                   WHERE p.key=ANY(source_platforms) AND p.value<>'null'::jsonb
                     AND (p.value->>'daily' IS NOT NULL OR p.value->>'weekly' IS NOT NULL
                          OR p.value->>'monthly' IS NOT NULL)
                     AND (NOT EXISTS (SELECT 1 FROM migration_256_quota_platforms q WHERE q.old_platform=p.key)
                          OR quota_json ? 'ollama'
                          OR (SELECT count(*) FROM jsonb_each(quota_json) other
                              WHERE other.key=ANY(source_platforms) AND other.value<>'null'::jsonb
                                AND (other.value->>'daily' IS NOT NULL
                                     OR other.value->>'weekly' IS NOT NULL
                                     OR other.value->>'monthly' IS NOT NULL))>1)) THEN
            RAISE EXCEPTION 'Ollama migration: shared default quota setting %; allocate Ollama budgets explicitly first', cfg.key;
        END IF;
        INSERT INTO migration_256_quota_settings
        SELECT cfg.key,(quota_json-p.key) || jsonb_build_object('ollama',p.value)
        FROM jsonb_each(quota_json) p
        JOIN migration_256_quota_platforms q ON q.old_platform=p.key
        WHERE p.value<>'null'::jsonb AND (p.value->>'daily' IS NOT NULL
             OR p.value->>'weekly' IS NOT NULL OR p.value->>'monthly' IS NOT NULL);
    END LOOP;

    SELECT string_agg(b.id::text, ',' ORDER BY b.id) INTO bad_ids
    FROM sub2api_plugin_bindings b WHERE b.enabled
      AND b.platform=ANY(source_platforms) AND b.account_type IN ('apikey','*','');
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: enabled plugin binding IDs [%]; confirm manifest Ollama support and rebind explicitly', bad_ids;
    END IF;

    -- A channel with exactly one original group platform can move its rows in
    -- place, preserving every interval/time-pricing/multiplier child and ID.
    -- A shared channel needs cloning (and overlap precedence decisions), so fail.
    CREATE TEMP TABLE migration_256_channels ON COMMIT DROP AS
    SELECT cg.channel_id, min(g.old_platform) AS old_platform
    FROM channel_groups cg JOIN migration_256_groups g ON g.id=cg.group_id
    GROUP BY cg.channel_id;
    -- Channels without applicable source config are safe even when shared.
    DELETE FROM migration_256_channels c
    WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing p
                      WHERE p.channel_id=c.channel_id AND EXISTS (
                          SELECT 1 FROM channel_groups cg JOIN migration_256_groups g ON g.id=cg.group_id
                          WHERE cg.channel_id=c.channel_id AND g.old_platform=p.platform))
      AND NOT EXISTS (SELECT 1 FROM channels ch WHERE ch.id=c.channel_id AND EXISTS (
                          SELECT 1 FROM channel_groups cg JOIN migration_256_groups g ON g.id=cg.group_id
                          WHERE cg.channel_id=c.channel_id AND ch.model_mapping ? g.old_platform))
      AND NOT EXISTS (SELECT 1 FROM channel_model_pricing p
                      WHERE p.channel_id=c.channel_id AND p.platform='ollama')
      AND NOT EXISTS (SELECT 1 FROM channels ch WHERE ch.id=c.channel_id AND ch.model_mapping ? 'ollama');
    SELECT string_agg(c.channel_id::text, ',' ORDER BY c.channel_id) INTO bad_ids
    FROM migration_256_channels c
    WHERE EXISTS (SELECT 1 FROM channel_groups cg WHERE cg.channel_id=c.channel_id
                  AND NOT EXISTS (SELECT 1 FROM migration_256_groups g WHERE g.id=cg.group_id))
       OR (SELECT count(DISTINCT g.old_platform) FROM channel_groups cg
           JOIN migration_256_groups g ON g.id=cg.group_id WHERE cg.channel_id=c.channel_id)>1
       OR EXISTS (SELECT 1 FROM channel_model_pricing p
                  WHERE p.channel_id=c.channel_id AND p.platform='ollama')
       OR EXISTS (SELECT 1 FROM channels ch WHERE ch.id=c.channel_id
                  AND ch.model_mapping ? 'ollama');
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: ambiguous channel IDs [%]; separate migrated groups/config first', bad_ids;
    END IF;

    -- A source-exclusive channel's unambiguous account-cost rules move intact.
    -- Account-scoped rules on merged siblings cannot represent which original
    -- sibling was selected, so they require an explicit accounting decision.
    CREATE TEMP TABLE migration_256_cost_rules ON COMMIT DROP AS
    SELECT r.id,min(g.old_platform) AS old_platform
    FROM channel_account_stats_pricing_rules r
    JOIN channel_groups cg ON cg.channel_id=r.channel_id
    JOIN migration_256_groups g ON g.id=cg.group_id
    WHERE NOT EXISTS (
        SELECT 1 FROM unnest(r.account_ids) aid
        WHERE NOT EXISTS (SELECT 1 FROM migration_256_accounts m WHERE m.id=aid
                          AND NOT EXISTS (SELECT 1 FROM migration_256_accounts sibling
                                          WHERE sibling.winner_id=m.winner_id AND sibling.id<>m.id)
                          AND NOT EXISTS (SELECT 1 FROM channel_groups cg2
                                          JOIN migration_256_groups mg ON mg.id=cg2.group_id
                                          WHERE cg2.channel_id=r.channel_id
                                            AND mg.old_platform<>m.old_platform))
    )
      AND NOT EXISTS (SELECT 1 FROM unnest(r.group_ids) gid
                      WHERE NOT EXISTS (SELECT 1 FROM migration_256_groups m WHERE m.id=gid))
      AND NOT EXISTS (SELECT 1 FROM channel_groups other WHERE other.channel_id=r.channel_id
                      AND NOT EXISTS (SELECT 1 FROM migration_256_groups m WHERE m.id=other.group_id))
      AND NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing p
                      WHERE p.rule_id=r.id AND p.platform='ollama')
    GROUP BY r.id HAVING count(DISTINCT g.old_platform)=1;
    SELECT string_agg(DISTINCT r.id::text, ',' ORDER BY r.id::text) INTO bad_ids
    FROM channel_account_stats_pricing_rules r
    JOIN channel_account_stats_model_pricing p ON p.rule_id=r.id
    WHERE (
        (p.platform=ANY(source_platforms)
         AND NOT EXISTS (SELECT 1 FROM migration_256_cost_rules c
                         WHERE c.id=r.id AND c.old_platform=p.platform))
        OR EXISTS (SELECT 1 FROM migration_256_accounts m
                   WHERE m.id=ANY(r.account_ids) AND EXISTS (
                       SELECT 1 FROM migration_256_accounts sibling
                       WHERE sibling.winner_id=m.winner_id AND sibling.id<>m.id))
    ) AND (
        EXISTS (SELECT 1 FROM channel_groups cg JOIN migration_256_groups g ON g.id=cg.group_id
                WHERE cg.channel_id=r.channel_id)
        OR EXISTS (SELECT 1 FROM migration_256_accounts m WHERE m.id=ANY(r.account_ids))
        OR EXISTS (SELECT 1 FROM migration_256_groups g WHERE g.id=ANY(r.group_ids))
    );
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: account-cost pricing rule IDs [%]; split platform rules explicitly first', bad_ids;
    END IF;

    -- Composite routes select a platform pool, not an account. Move only routes
    -- in migrated groups; routes elsewhere pointing at a source pool are unsafe
    -- because the pool is being split. Deleted routes remain historical.
    SELECT string_agg(r.id::text, ',' ORDER BY r.id) INTO bad_ids
    FROM composite_model_routes r WHERE r.deleted_at IS NULL AND (
        (r.target_platform=ANY(source_platforms)
         AND NOT EXISTS (SELECT 1 FROM migration_256_groups g WHERE g.id=r.group_id))
        OR EXISTS (SELECT 1 FROM migration_256_groups g WHERE g.id=r.group_id
                   AND r.target_platform<>g.old_platform));
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Ollama migration: ambiguous composite route IDs [%]; choose target pools explicitly first', bad_ids;
    END IF;

    -- Permanent writes start only after every preflight succeeds.
    INSERT INTO account_groups (account_id,group_id,priority,created_at)
    SELECT m.winner_id,ag.group_id,min(ag.priority),min(ag.created_at)
    FROM account_groups ag JOIN migration_256_accounts m ON m.id=ag.account_id
    GROUP BY m.winner_id,ag.group_id
    ON CONFLICT (account_id,group_id) DO UPDATE
      SET priority=LEAST(account_groups.priority,EXCLUDED.priority);

    UPDATE accounts a SET platform='ollama',
        credentials=jsonb_set(a.credentials,'{base_url}','"https://ollama.com/v1"'::jsonb),
        status=CASE WHEN m.id=m.winner_id THEN a.status ELSE 'disabled' END,
        extra=CASE WHEN m.id=m.winner_id THEN a.extra ELSE
            a.extra || jsonb_build_object('migrated_to_account_id',m.winner_id) END,
        updated_at=NOW()
    FROM migration_256_accounts m WHERE a.id=m.id;
    -- The proxy trigger listens only to proxy_id/deleted_at; a platform change
    -- deliberately keeps every open/historical binding and its lock metadata.
    UPDATE proxy_account_bindings b SET platform='ollama'
    FROM migration_256_accounts m WHERE b.account_id=m.id;
    UPDATE groups g SET platform='ollama',updated_at=NOW()
    FROM migration_256_groups m WHERE g.id=m.id;
    UPDATE channel_model_pricing p SET platform='ollama'
    FROM migration_256_channels c WHERE p.channel_id=c.channel_id AND p.platform=c.old_platform;
    UPDATE channel_account_stats_model_pricing p SET platform='ollama'
    FROM migration_256_cost_rules r WHERE p.rule_id=r.id AND p.platform=r.old_platform;
    UPDATE channels ch SET model_mapping=(ch.model_mapping-c.old_platform)
        || jsonb_build_object('ollama',ch.model_mapping->c.old_platform)
    FROM migration_256_channels c WHERE ch.id=c.channel_id AND ch.model_mapping ? c.old_platform;
    UPDATE composite_model_routes r SET target_platform='ollama'
    FROM migration_256_groups g WHERE r.group_id=g.id AND r.deleted_at IS NULL
        AND r.target_platform=g.old_platform;
    UPDATE settings s SET value=q.value::text
    FROM migration_256_quota_settings q WHERE s.key=q.key;

    -- Source-scoped error rules keep applying to migrated traffic. Empty lists
    -- already mean all platforms. Append once; retain original order and scope.
    UPDATE error_passthrough_rules r SET platforms=r.platforms || '["ollama"]'::jsonb
    WHERE jsonb_typeof(r.platforms)='array' AND NOT r.platforms ? 'ollama'
      AND (r.platforms ? 'openai' OR r.platforms ?| COALESCE(source_platforms,ARRAY[]::text[]));
    UPDATE channel_monitor_v2_config c
    SET platforms=c.platforms || jsonb_build_array(src.entry || '{"platform":"ollama"}'::jsonb),
        version=c.version+1,updated_at=NOW()
    FROM LATERAL (SELECT e AS entry FROM jsonb_array_elements(
        (SELECT platforms FROM channel_monitor_v2_config WHERE id=1)) e
        WHERE e->>'platform'='openai' LIMIT 1) src
    WHERE c.id=1 AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(c.platforms) e
                                WHERE e->>'platform'='ollama');

    -- Preserve OpenAI pool selection and both account caps. Sticky accounts
    -- normalize failure handling to pause, matching normalizeProxyIPPolicy.
    -- An existing Ollama policy wins; absent OpenAI uses the 251 defaults.
    INSERT INTO platform_ip_policies
        (platform,mode,pool_ids,max_lifetime_accounts,max_active_accounts,on_egress_failure)
    VALUES ('ollama','sticky',
        COALESCE((SELECT pool_ids FROM platform_ip_policies WHERE platform='openai'),'[]'::jsonb),
        (SELECT max_lifetime_accounts FROM platform_ip_policies WHERE platform='openai'),
        (SELECT max_active_accounts FROM platform_ip_policies WHERE platform='openai'),'pause')
    ON CONFLICT (platform) DO NOTHING;

    DROP TABLE migration_256_channels, migration_256_groups, migration_256_accounts,
        migration_256_quota_platforms, migration_256_quota_settings, migration_256_cost_rules;
END $$;

-- 代理池（fork）：平台 IP 策略，替代 proxy_pools.platform_limits。代理池只作库存，规则按平台配置。
-- 没有策略的平台不能从代理池分配；本迁移为现有平台全部生成策略，保证升级后原有用法不受影响。
CREATE TABLE IF NOT EXISTS platform_ip_policies (
    id                    BIGSERIAL PRIMARY KEY,
    platform              VARCHAR(50) NOT NULL,
    -- churn：消耗型（如 kiro），账号用完即弃，按历史账号数退役；sticky：长期型，账号终身固定一个落地 IP。
    mode                  VARCHAR(20) NOT NULL DEFAULT 'sticky' CHECK (mode IN ('churn', 'sticky')),
    -- 可取 IP 的代理池 id 列表，空数组表示全部代理池。
    pool_ids              JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- 每个落地 IP 对该平台的历史累计账号上限（含已解绑、已删除），达到即对该平台永久退役；NULL 不限。
    max_lifetime_accounts INTEGER CHECK (max_lifetime_accounts IS NULL OR max_lifetime_accounts >= 1),
    -- 每个落地 IP 对该平台同时在用的账号上限；NULL 不限。
    max_active_accounts   INTEGER CHECK (max_active_accounts IS NULL OR max_active_accounts >= 1),
    -- 落地 IP 故障时：pause 暂停账号并告警；reassign 自动换 IP（仅 churn）。
    on_egress_failure     VARCHAR(20) NOT NULL DEFAULT 'pause' CHECK (on_egress_failure IN ('pause', 'reassign')),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_ip_policies_platform_key ON platform_ip_policies (platform);

-- 回填：原 platform_limits 中配置过上限的平台 → churn，上限取各池中的最小值（不放宽任何原有限制）。
INSERT INTO platform_ip_policies (platform, mode, max_lifetime_accounts, on_egress_failure)
SELECT l.key, 'churn', MIN(l.value::int), 'reassign'
FROM proxy_pools p, jsonb_each_text(p.platform_limits) l
WHERE btrim(l.key) <> '' AND l.value ~ '^[0-9]+$' AND l.value::int >= 1
GROUP BY l.key
ON CONFLICT (platform) DO NOTHING;

-- 其余现有平台（有账号或有绑定记录）：kiro 为 churn 不限上限，其他为 sticky 不限在用数，保持原有共用关系。
INSERT INTO platform_ip_policies (platform, mode, on_egress_failure)
SELECT x.platform,
       CASE WHEN x.platform = 'kiro' THEN 'churn' ELSE 'sticky' END,
       CASE WHEN x.platform = 'kiro' THEN 'reassign' ELSE 'pause' END
FROM (
    SELECT platform FROM accounts WHERE deleted_at IS NULL AND btrim(platform) <> ''
    UNION
    SELECT platform FROM proxy_account_bindings WHERE btrim(platform) <> ''
) x
ON CONFLICT (platform) DO NOTHING;

COMMENT ON COLUMN proxy_pools.platform_limits IS 'Deprecated since 251: replaced by platform_ip_policies, kept for rollback only.';

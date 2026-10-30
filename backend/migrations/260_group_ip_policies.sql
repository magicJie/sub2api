-- 代理池（fork）：分组 IP 策略，按分组覆盖平台 IP 策略（platform_ip_policies）。
-- 同平台账号按类型分组后 IP 管理方式可能不同（如 openai plus 消耗型、pro 长期型），分组策略优先于平台策略。
-- 字段含义与 platform_ip_policies 相同；平台取自分组。计数口径不变：仍按落地 IP + 平台统计。
-- 单独建表，不改 platform_ip_policies 的唯一约束，旧版本回滚后仍可正常读写平台策略。
CREATE TABLE IF NOT EXISTS group_ip_policies (
    id                    BIGSERIAL PRIMARY KEY,
    group_id              BIGINT NOT NULL UNIQUE REFERENCES groups(id) ON DELETE CASCADE,
    mode                  VARCHAR(20) NOT NULL DEFAULT 'sticky' CHECK (mode IN ('churn', 'sticky')),
    pool_ids              JSONB NOT NULL DEFAULT '[]'::jsonb,
    max_lifetime_accounts INTEGER CHECK (max_lifetime_accounts IS NULL OR max_lifetime_accounts >= 1),
    max_active_accounts   INTEGER CHECK (max_active_accounts IS NULL OR max_active_accounts >= 1),
    on_egress_failure     VARCHAR(20) NOT NULL DEFAULT 'pause' CHECK (on_egress_failure IN ('pause', 'reassign')),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

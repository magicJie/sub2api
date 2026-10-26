-- 代理池（fork）：代理池分组、代理归属、账号-代理绑定记录（用于按平台限制每个代理的历史账号数，实现代理退役）。

CREATE TABLE IF NOT EXISTS proxy_pools (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(100) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    -- 每个代理按平台允许绑定的历史账号数上限，如 {"kiro": 5}；未配置的平台不限。
    platform_limits JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS proxy_pools_name_key ON proxy_pools (name);
CREATE UNIQUE INDEX IF NOT EXISTS proxy_pools_single_default_key ON proxy_pools (is_default) WHERE is_default;

INSERT INTO proxy_pools (name, is_default)
SELECT 'default', TRUE
WHERE NOT EXISTS (SELECT 1 FROM proxy_pools WHERE is_default);

-- proxies.pool_id 为 NULL 表示属于默认代理池；删除代理池时其代理自动回到默认池。
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS pool_id BIGINT REFERENCES proxy_pools(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS proxies_pool_id_idx ON proxies (pool_id);

-- 账号与代理的绑定记录。一个账号同一时刻最多一条未解除（unbound_at IS NULL）的记录。
-- 由 accounts 触发器在 proxy_id 变化 / 账号删除时维护；从代理池分配的绑定由应用标记 source='pool' 并锁定。
CREATE TABLE IF NOT EXISTS proxy_account_bindings (
    id            BIGSERIAL PRIMARY KEY,
    proxy_id      BIGINT NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
    account_id    BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    platform      VARCHAR(50) NOT NULL,
    pool_id       BIGINT REFERENCES proxy_pools(id) ON DELETE SET NULL,
    source        VARCHAR(20) NOT NULL DEFAULT 'manual',
    locked        BOOLEAN NOT NULL DEFAULT FALSE,
    bound_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    unlocked_at   TIMESTAMPTZ,
    unbound_at    TIMESTAMPTZ,
    unbind_reason VARCHAR(30)
);

CREATE INDEX IF NOT EXISTS proxy_account_bindings_proxy_platform_idx ON proxy_account_bindings (proxy_id, platform);
CREATE UNIQUE INDEX IF NOT EXISTS proxy_account_bindings_open_account_key
    ON proxy_account_bindings (account_id) WHERE unbound_at IS NULL;

CREATE OR REPLACE FUNCTION record_proxy_account_binding()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    prev proxy_account_bindings%ROWTYPE;
    has_prev BOOLEAN;
BEGIN
    -- spark 影子账号继承母账号代理且共用凭据，不单独计数。
    IF NEW.parent_account_id IS NOT NULL THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE'
       AND OLD.proxy_id IS NOT DISTINCT FROM NEW.proxy_id
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    SELECT * INTO prev FROM proxy_account_bindings
    WHERE account_id = NEW.id AND unbound_at IS NULL
    ORDER BY id DESC LIMIT 1;
    has_prev := FOUND;

    IF has_prev AND (NEW.deleted_at IS NOT NULL OR prev.proxy_id IS DISTINCT FROM NEW.proxy_id) THEN
        UPDATE proxy_account_bindings
        SET unbound_at = NOW(),
            unbind_reason = CASE WHEN NEW.deleted_at IS NOT NULL THEN 'account_deleted' ELSE 'proxy_changed' END
        WHERE id = prev.id;
    END IF;

    IF NEW.deleted_at IS NULL AND NEW.proxy_id IS NOT NULL
       AND (NOT has_prev OR prev.proxy_id IS DISTINCT FROM NEW.proxy_id) THEN
        -- 系统改投（如代理过期回退）时沿用原绑定的池与锁定状态；管理员改代理前必须先解锁。
        IF has_prev AND prev.locked THEN
            INSERT INTO proxy_account_bindings (proxy_id, account_id, platform, pool_id, source, locked)
            VALUES (NEW.proxy_id, NEW.id, NEW.platform, prev.pool_id, prev.source, TRUE);
        ELSE
            INSERT INTO proxy_account_bindings (proxy_id, account_id, platform)
            VALUES (NEW.proxy_id, NEW.id, NEW.platform);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_accounts_proxy_binding ON accounts;
CREATE TRIGGER trg_accounts_proxy_binding
AFTER INSERT OR UPDATE OF proxy_id, deleted_at ON accounts
FOR EACH ROW EXECUTE FUNCTION record_proxy_account_binding();

-- 回填现有账号的当前绑定（迁移前的历史无从得知，只记录当前状态）。
INSERT INTO proxy_account_bindings (proxy_id, account_id, platform, bound_at)
SELECT a.proxy_id, a.id, a.platform, a.created_at
FROM accounts a
WHERE a.proxy_id IS NOT NULL
  AND a.deleted_at IS NULL
  AND a.parent_account_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM proxy_account_bindings b WHERE b.account_id = a.id AND b.unbound_at IS NULL);

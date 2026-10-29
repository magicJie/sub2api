-- 链式代理（fork）：前置组取代首跳组与 transport 代理，见 docs/PROXY_IP_MODEL.md 第 2.2、3.4、7 节。
-- 前置端点 = 外部 AIO 的一个 listener；前置组 = 槽位集合；槽位 = 有序端点列表（第一个为主）。
-- 升级原则：不改变任何落地当前实际走的路径；无法等价转换的数据直接拒绝迁移并列出，由人工先处理。
-- 旧列（proxies.role / upstream_proxy_id / upstream_hop_group_id）、proxy_hop_groups 表与默认前置 settings 保留供回滚，应用不再读写。

-- 预检：任一项不满足即中止，事务回滚，不改任何数据。
DO $$
DECLARE
    bad TEXT;
BEGIN
    -- 1. transport 代理只能是不再串前置的 socks5 / http（AIO listener）。
    SELECT string_agg(format('#%s %s (%s, upstream_mode=%s)', id, name, protocol, upstream_mode), '; ' ORDER BY id) INTO bad
    FROM proxies
    WHERE deleted_at IS NULL AND role = 'transport'
      AND (protocol NOT IN ('socks5', 'socks5h', 'http', 'https') OR upstream_mode <> 'direct');
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION '257 预检失败：以下 transport 代理不能转为前置端点（只支持 socks5/http 且不能再有前置）：%', bad;
    END IF;

    -- 2. 落地串在另一条落地（egress）后面，没有等价的前置组表示。
    SELECT string_agg(format('#%s %s -> #%s', p.id, p.name, p.upstream_proxy_id), '; ' ORDER BY p.id) INTO bad
    FROM proxies p
    LEFT JOIN proxies u ON u.id = p.upstream_proxy_id AND u.deleted_at IS NULL
    WHERE p.deleted_at IS NULL AND p.role = 'egress' AND p.upstream_mode = 'proxy'
      AND (u.id IS NULL OR u.role <> 'transport');
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION '257 预检失败：以下落地代理的前置不是 transport 代理（落地串落地或前置已删除），请先改为直连或 AIO 端点：%', bad;
    END IF;

    -- 3. 仍被引用、带订阅 URL 的首跳组（去掉 mihomo 后无法承载）；没有手工成员的组同理。
    SELECT string_agg(format('#%s %s', g.id, g.name), '; ' ORDER BY g.id) INTO bad
    FROM proxy_hop_groups g
    WHERE (jsonb_array_length(g.subscription_urls) > 0
           OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(g.member_proxy_ids) m
                          JOIN proxies x ON x.id = m::bigint AND x.deleted_at IS NULL AND x.role = 'transport'))
      AND (EXISTS (SELECT 1 FROM proxies p WHERE p.deleted_at IS NULL AND p.upstream_mode = 'hop_group' AND p.upstream_hop_group_id = g.id)
           OR EXISTS (SELECT 1 FROM settings s WHERE s.key = 'proxy_chain_default_upstream_hop_group_id' AND btrim(s.value) = g.id::text
                      AND EXISTS (SELECT 1 FROM proxies p WHERE p.deleted_at IS NULL AND p.upstream_mode = 'default')));
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION '257 预检失败：以下首跳组带订阅或没有可转换的 transport 成员，且仍被落地引用，请先把落地改到 AIO 端点：%', bad;
    END IF;

    -- 4. 首跳组成员必须都是 transport 代理（否则成员无法转成端点）。
    SELECT string_agg(format('group #%s member #%s', g.id, m), '; ') INTO bad
    FROM proxy_hop_groups g, jsonb_array_elements_text(g.member_proxy_ids) m
    LEFT JOIN proxies x ON x.id = m::bigint AND x.deleted_at IS NULL
    WHERE x.id IS NOT NULL AND x.role <> 'transport';
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION '257 预检失败：以下首跳组成员不是 transport 代理：%', bad;
    END IF;

    -- 5. 跟随默认前置的落地，默认值必须可转换：默认首跳组，或默认前置代理是 transport。
    IF EXISTS (SELECT 1 FROM proxies p WHERE p.deleted_at IS NULL AND p.upstream_mode = 'default')
       AND NOT EXISTS (SELECT 1 FROM settings s WHERE s.key = 'proxy_chain_default_upstream_hop_group_id' AND btrim(s.value) ~ '^[0-9]+$'
                       AND EXISTS (SELECT 1 FROM proxy_hop_groups g WHERE g.id = btrim(s.value)::bigint))
       AND NOT EXISTS (SELECT 1 FROM settings s JOIN proxies x ON x.id::text = btrim(s.value) AND x.deleted_at IS NULL AND x.role = 'transport'
                       WHERE s.key = 'proxy_chain_default_upstream_proxy_id') THEN
        SELECT string_agg(format('#%s %s', id, name), '; ' ORDER BY id) INTO bad
        FROM proxies WHERE deleted_at IS NULL AND upstream_mode = 'default';
        RAISE EXCEPTION '257 预检失败：以下落地跟随默认前置，但默认前置未配置或不是 transport 代理（原本即 fail-closed），请先显式设置前置：%', bad;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS proxy_upstream_endpoints (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(255) NOT NULL UNIQUE,
    protocol    VARCHAR(20) NOT NULL CHECK (protocol IN ('socks5', 'socks5h', 'http', 'https')),
    host        VARCHAR(255) NOT NULL,
    port        INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    username    VARCHAR(255) NOT NULL DEFAULT '',
    password    VARCHAR(255) NOT NULL DEFAULT '',
    status      VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    -- 迁移来源（proxies.id），只用于回滚与排查。
    legacy_proxy_id BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS proxy_upstream_groups (
    id                     BIGSERIAL PRIMARY KEY,
    name                   VARCHAR(255) NOT NULL UNIQUE,
    status                 VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    probe_url              TEXT NOT NULL DEFAULT 'https://www.gstatic.com/generate_204',
    probe_interval_seconds INTEGER NOT NULL DEFAULT 30 CHECK (probe_interval_seconds BETWEEN 5 AND 3600),
    fail_threshold         INTEGER NOT NULL DEFAULT 3 CHECK (fail_threshold BETWEEN 1 AND 20),
    recover_threshold      INTEGER NOT NULL DEFAULT 2 CHECK (recover_threshold BETWEEN 1 AND 20),
    failback_seconds       INTEGER NOT NULL DEFAULT 300 CHECK (failback_seconds BETWEEN 0 AND 86400),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS proxy_upstream_slots (
    id           BIGSERIAL PRIMARY KEY,
    group_id     BIGINT NOT NULL REFERENCES proxy_upstream_groups(id) ON DELETE CASCADE,
    name         VARCHAR(255) NOT NULL,
    position     INTEGER NOT NULL,
    -- 有序端点 id 数组，第一个为主；引用保护由应用层校验（JSONB 无法建外键）。
    endpoint_ids JSONB NOT NULL CHECK (jsonb_typeof(endpoint_ids) = 'array' AND jsonb_array_length(endpoint_ids) > 0),
    -- 槽位故障对应的运维告警（ops_alert_events.id）；巡检选主轮换时据此去重与恢复。
    alert_event_id BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS proxy_upstream_slots_group_idx ON proxy_upstream_slots (group_id, position);

ALTER TABLE proxies ADD COLUMN IF NOT EXISTS upstream_group_id BIGINT;
CREATE INDEX IF NOT EXISTS proxies_upstream_group_id_idx ON proxies (upstream_group_id);
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'proxies_upstream_group_id_fkey' AND conrelid = 'proxies'::regclass) THEN
        -- 活跃引用由应用层拒绝删除；软删除的代理不应永久钉住组。
        ALTER TABLE proxies ADD CONSTRAINT proxies_upstream_group_id_fkey
            FOREIGN KEY (upstream_group_id) REFERENCES proxy_upstream_groups(id) ON DELETE SET NULL;
    END IF;
END $$;

-- transport 代理 → 端点（同名；同名冲突时追加 id）。
INSERT INTO proxy_upstream_endpoints (name, protocol, host, port, username, password, status, legacy_proxy_id)
SELECT CASE WHEN EXISTS (SELECT 1 FROM proxies d WHERE d.deleted_at IS NULL AND d.role = 'transport' AND d.name = p.name AND d.id < p.id)
            THEN p.name || ' #' || p.id ELSE p.name END,
       p.protocol, p.host, p.port, COALESCE(p.username, ''), COALESCE(p.password, ''),
       CASE WHEN p.status = 'active' THEN 'active' ELSE 'inactive' END, p.id
FROM proxies p
WHERE p.deleted_at IS NULL AND p.role = 'transport'
  AND NOT EXISTS (SELECT 1 FROM proxy_upstream_endpoints e WHERE e.legacy_proxy_id = p.id)
ORDER BY p.id;

-- 首跳组 → 同 id 前置组。fallback / url-test：一个槽位，端点按成员顺序；load-balance：每成员一个单端点槽位。
INSERT INTO proxy_upstream_groups (id, name, status, probe_url, probe_interval_seconds)
SELECT g.id, g.name, g.status, g.test_url, LEAST(GREATEST(g.interval_seconds, 5), 3600)
FROM proxy_hop_groups g
WHERE EXISTS (SELECT 1 FROM jsonb_array_elements_text(g.member_proxy_ids) m
              JOIN proxy_upstream_endpoints e ON e.legacy_proxy_id = m::bigint)
ON CONFLICT (id) DO NOTHING;

INSERT INTO proxy_upstream_slots (group_id, name, position, endpoint_ids)
SELECT g.id, 'slot-1', 1,
       (SELECT jsonb_agg(e.id ORDER BY m.ord) FROM jsonb_array_elements_text(g.member_proxy_ids) WITH ORDINALITY m(v, ord)
        JOIN proxy_upstream_endpoints e ON e.legacy_proxy_id = m.v::bigint)
FROM proxy_hop_groups g
JOIN proxy_upstream_groups ug ON ug.id = g.id
WHERE g.strategy <> 'load-balance'
  AND NOT EXISTS (SELECT 1 FROM proxy_upstream_slots s WHERE s.group_id = g.id);

INSERT INTO proxy_upstream_slots (group_id, name, position, endpoint_ids)
SELECT g.id, 'slot-' || row_number() OVER (PARTITION BY g.id ORDER BY m.ord), row_number() OVER (PARTITION BY g.id ORDER BY m.ord), jsonb_build_array(e.id)
FROM proxy_hop_groups g
JOIN proxy_upstream_groups ug ON ug.id = g.id
CROSS JOIN LATERAL jsonb_array_elements_text(g.member_proxy_ids) WITH ORDINALITY m(v, ord)
JOIN proxy_upstream_endpoints e ON e.legacy_proxy_id = m.v::bigint
WHERE g.strategy = 'load-balance'
  AND NOT EXISTS (SELECT 1 FROM proxy_upstream_slots s WHERE s.group_id = g.id);

-- 显式 id 插入后推进序列。
SELECT setval(pg_get_serial_sequence('proxy_upstream_groups', 'id'), GREATEST((SELECT COALESCE(MAX(id), 0) FROM proxy_upstream_groups), 1));

-- 被 upstream_mode=proxy 引用或作为默认前置代理的 transport → 每个生成一个单槽位单端点前置组（同名冲突时追加后缀）。
CREATE TEMP TABLE _257_proxy_groups (proxy_id BIGINT PRIMARY KEY, group_id BIGINT NOT NULL) ON COMMIT DROP;
DO $$
DECLARE
    t RECORD;
    gid BIGINT;
    gname TEXT;
BEGIN
    FOR t IN
        SELECT x.id, x.name, e.id AS endpoint_id
        FROM proxies x
        JOIN proxy_upstream_endpoints e ON e.legacy_proxy_id = x.id
        WHERE x.deleted_at IS NULL AND x.role = 'transport'
          AND (EXISTS (SELECT 1 FROM proxies p WHERE p.deleted_at IS NULL AND p.upstream_mode = 'proxy' AND p.upstream_proxy_id = x.id)
               OR EXISTS (SELECT 1 FROM settings s WHERE s.key = 'proxy_chain_default_upstream_proxy_id' AND btrim(s.value) = x.id::text))
        ORDER BY x.id
    LOOP
        gname := t.name;
        IF EXISTS (SELECT 1 FROM proxy_upstream_groups WHERE name = gname) THEN
            gname := t.name || ' (proxy #' || t.id || ')';
        END IF;
        INSERT INTO proxy_upstream_groups (name) VALUES (gname) RETURNING id INTO gid;
        INSERT INTO proxy_upstream_slots (group_id, name, position, endpoint_ids) VALUES (gid, 'slot-1', 1, jsonb_build_array(t.endpoint_id));
        INSERT INTO _257_proxy_groups (proxy_id, group_id) VALUES (t.id, gid);
    END LOOP;
END $$;

-- 落地改写。顺序：default 先按旧 settings 解析，再改 proxy / hop_group。
UPDATE proxies p
SET upstream_mode = 'group',
    upstream_group_id = COALESCE(
        (SELECT ug.id FROM settings s JOIN proxy_upstream_groups ug ON ug.id::text = btrim(s.value)
         WHERE s.key = 'proxy_chain_default_upstream_hop_group_id'),
        (SELECT g.group_id FROM settings s JOIN _257_proxy_groups g ON g.proxy_id::text = btrim(s.value)
         WHERE s.key = 'proxy_chain_default_upstream_proxy_id')),
    updated_at = NOW()
WHERE p.deleted_at IS NULL AND p.upstream_mode = 'default';

UPDATE proxies p
SET upstream_mode = 'group', upstream_group_id = g.group_id, updated_at = NOW()
FROM _257_proxy_groups g
WHERE p.deleted_at IS NULL AND p.upstream_mode = 'proxy' AND p.upstream_proxy_id = g.proxy_id;

UPDATE proxies p
SET upstream_mode = 'group', upstream_group_id = p.upstream_hop_group_id, updated_at = NOW()
WHERE p.deleted_at IS NULL AND p.upstream_mode = 'hop_group';

-- 已软删除的代理没有可用前置，统一视为直连（不再出站）。
UPDATE proxies SET upstream_mode = 'direct', upstream_group_id = NULL
WHERE deleted_at IS NOT NULL AND upstream_mode NOT IN ('direct', 'group');

-- 后置检查：未删除的落地都必须是 direct，或指向存在的前置组。
DO $$
DECLARE
    bad TEXT;
BEGIN
    SELECT string_agg(format('#%s %s', p.id, p.name), '; ' ORDER BY p.id) INTO bad
    FROM proxies p
    WHERE p.deleted_at IS NULL AND p.role = 'egress'
      AND NOT (p.upstream_mode = 'direct'
               OR (p.upstream_mode = 'group' AND EXISTS (SELECT 1 FROM proxy_upstream_groups g WHERE g.id = p.upstream_group_id)));
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION '257 后置检查失败：以下落地前置未能转换：%', bad;
    END IF;
END $$;

-- transport 代理已转为端点：软删除（role 列保留供回滚）。
UPDATE proxies SET deleted_at = NOW(), updated_at = NOW()
WHERE deleted_at IS NULL AND role = 'transport';

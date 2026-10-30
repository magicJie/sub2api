-- 链式代理（fork）：前置端点改为「AIO 实例 + 监听端口」。AIO 存主机、协议与认证，端口只存名称、端口号与状态。
-- 升级原则：不改变任何落地当前实际走的路径。端点 id 不变（槽位 endpoint_ids 引用保持），只补 aio_id。
-- 端点表的 protocol / host / username / password 列保留，并由应用在修改 AIO 时同步，供回滚到旧版本读取。

CREATE TABLE IF NOT EXISTS proxy_upstream_aios (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(255) NOT NULL UNIQUE,
    protocol    VARCHAR(20) NOT NULL CHECK (protocol IN ('socks5', 'socks5h', 'http', 'https')),
    host        VARCHAR(255) NOT NULL,
    username    VARCHAR(255) NOT NULL DEFAULT '',
    password    VARCHAR(255) NOT NULL DEFAULT '',
    status      VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE proxy_upstream_endpoints ADD COLUMN IF NOT EXISTS aio_id BIGINT;

-- 已有端点按 host + protocol + 凭据归并，每组生成一个 AIO（状态启用；端点自身状态不变，可用性与升级前一致）。
-- 名称取 host；同一 host 有多组时按首个端点 id 顺序追加 " #序号"；与已有 AIO 重名时再追加首个端点 id。
CREATE TEMP TABLE _259_aio_keys ON COMMIT DROP AS
SELECT protocol, host, username, password, MIN(id) AS first_endpoint_id,
       row_number() OVER (PARTITION BY host ORDER BY MIN(id)) AS host_rank,
       COUNT(*) OVER (PARTITION BY host) AS host_count
FROM proxy_upstream_endpoints
WHERE aio_id IS NULL
GROUP BY protocol, host, username, password;

INSERT INTO proxy_upstream_aios (name, protocol, host, username, password, status)
SELECT CASE WHEN EXISTS (SELECT 1 FROM proxy_upstream_aios a WHERE a.name = n.name)
            THEN n.name || ' (#' || n.first_endpoint_id || ')' ELSE n.name END,
       n.protocol, n.host, n.username, n.password, 'active'
FROM (SELECT k.*, CASE WHEN k.host_count > 1 THEN k.host || ' #' || k.host_rank ELSE k.host END AS name
      FROM _259_aio_keys k) n
ORDER BY n.first_endpoint_id;

UPDATE proxy_upstream_endpoints e
SET aio_id = a.id
FROM proxy_upstream_aios a
WHERE e.aio_id IS NULL
  AND a.protocol = e.protocol AND a.host = e.host AND a.username = e.username AND a.password = e.password;

-- 后置检查：每个端点都必须归入 AIO。
DO $$
DECLARE
    bad TEXT;
BEGIN
    SELECT string_agg(format('#%s %s', id, name), '; ' ORDER BY id) INTO bad
    FROM proxy_upstream_endpoints WHERE aio_id IS NULL;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION '259 后置检查失败：以下前置端点未能归入 AIO：%', bad;
    END IF;
END $$;

ALTER TABLE proxy_upstream_endpoints ALTER COLUMN aio_id SET NOT NULL;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'proxy_upstream_endpoints_aio_id_fkey' AND conrelid = 'proxy_upstream_endpoints'::regclass) THEN
        -- 仍有端口的 AIO 不能删除。
        ALTER TABLE proxy_upstream_endpoints ADD CONSTRAINT proxy_upstream_endpoints_aio_id_fkey
            FOREIGN KEY (aio_id) REFERENCES proxy_upstream_aios(id) ON DELETE RESTRICT;
    END IF;
END $$;

-- 端口名在 AIO 内唯一（不同 AIO 下可都叫 xyy）。
ALTER TABLE proxy_upstream_endpoints DROP CONSTRAINT IF EXISTS proxy_upstream_endpoints_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS proxy_upstream_endpoints_aio_name_key ON proxy_upstream_endpoints (aio_id, name);

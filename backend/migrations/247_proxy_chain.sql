-- proxies: 链式代理（前置代理）与 Xray 协议参数
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS upstream_mode varchar(20) NOT NULL DEFAULT 'direct';
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS upstream_proxy_id BIGINT;
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS options JSONB;
CREATE INDEX IF NOT EXISTS proxies_upstream_proxy_id_idx ON proxies (upstream_proxy_id);

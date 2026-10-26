-- 链式代理（fork）：mihomo 自动切换首跳组。
CREATE TABLE IF NOT EXISTS proxy_hop_groups (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    strategy VARCHAR(20) NOT NULL DEFAULT 'fallback' CHECK (strategy IN ('fallback', 'url-test')),
    test_url TEXT NOT NULL DEFAULT 'https://www.gstatic.com/generate_204',
    interval_seconds INTEGER NOT NULL DEFAULT 60 CHECK (interval_seconds > 0),
    tolerance_ms INTEGER NOT NULL DEFAULT 50 CHECK (tolerance_ms >= 0),
    subscription_urls JSONB NOT NULL DEFAULT '[]'::jsonb,
    member_proxy_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    subscription_update_interval INTEGER NOT NULL DEFAULT 3600 CHECK (subscription_update_interval > 0),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS upstream_hop_group_id BIGINT;
CREATE INDEX IF NOT EXISTS proxies_upstream_hop_group_id_idx ON proxies (upstream_hop_group_id);
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'proxies_upstream_hop_group_id_fkey' AND conrelid = 'proxies'::regclass) THEN
        -- Active references are rejected by the service; soft-deleted proxies must not pin groups forever.
        -- A concurrent new reference retains hop_group mode with a NULL id and therefore fails closed.
        ALTER TABLE proxies ADD CONSTRAINT proxies_upstream_hop_group_id_fkey FOREIGN KEY (upstream_hop_group_id) REFERENCES proxy_hop_groups(id) ON DELETE SET NULL;
    END IF;
END $$;

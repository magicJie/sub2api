-- 代理池（fork）：落地 IP 健康巡检的故障记录与账号处置记录，见 docs/PROXY_IP_MODEL.md 第 5 节。
-- layer：egress = 传输层健康但经该落地代理不通（落地 IP 故障）；transport = 前置链路（首跳组/前置代理）不可用。
CREATE TABLE IF NOT EXISTS proxy_egress_incidents (
    id              BIGSERIAL PRIMARY KEY,
    proxy_id        BIGINT NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
    layer           VARCHAR(20) NOT NULL CHECK (layer IN ('egress', 'transport')),
    status          VARCHAR(20) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    last_error      TEXT NOT NULL DEFAULT '',
    -- 连续失败次数；达到阈值才处置账号（handled_at 非空表示已处置），避免网络抖动误伤。
    fail_count      INTEGER NOT NULL DEFAULT 1,
    handled_at      TIMESTAMPTZ,
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    -- 对应的运维告警事件（ops_alert_events.id），恢复时一并标记 resolved；运维监控关闭时为空。
    alert_event_id  BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS proxy_egress_incidents_open_proxy_key
    ON proxy_egress_incidents (proxy_id) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS proxy_egress_incidents_created_idx ON proxy_egress_incidents (created_at DESC);

-- 故障对账号的处置：paused 暂停调度（待人工恢复/换 IP）；reassigned 已自动换 IP；alert_only 只告警
-- （平台无策略，或账号原本就不可调度）。resumed_at 非空表示已人工恢复或换 IP。
CREATE TABLE IF NOT EXISTS proxy_egress_incident_accounts (
    id            BIGSERIAL PRIMARY KEY,
    incident_id   BIGINT NOT NULL REFERENCES proxy_egress_incidents(id) ON DELETE CASCADE,
    account_id    BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    action        VARCHAR(20) NOT NULL CHECK (action IN ('paused', 'reassigned', 'alert_only')),
    to_proxy_id   BIGINT REFERENCES proxies(id) ON DELETE SET NULL,
    message       TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resumed_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS proxy_egress_incident_accounts_key
    ON proxy_egress_incident_accounts (incident_id, account_id);
CREATE INDEX IF NOT EXISTS proxy_egress_incident_accounts_paused_idx
    ON proxy_egress_incident_accounts (account_id) WHERE action = 'paused' AND resumed_at IS NULL;

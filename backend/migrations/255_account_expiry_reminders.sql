-- 账号到期提醒（fork）：到期前 N 天写一条运维告警，每个账号每个到期日只提醒一次。
-- 唯一键 (account_id, expires_at)：续费改了到期日后会按新日期再提醒一次；多实例并发时由唯一键保证只发一条。
-- alert_event_id 是对应的 ops_alert_events.id（写告警失败时该行会被删除，下一轮重试）。
CREATE TABLE IF NOT EXISTS account_expiry_reminders (
    account_id     BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    expires_at     TIMESTAMPTZ NOT NULL,
    alert_event_id BIGINT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, expires_at)
);

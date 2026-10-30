-- Kiro（fork）：补「可退役」标记（extra.kiro_quota_rejected_at / kiro_quota_rejected_reason）。
-- 标记自 7765204da 起由网关在上游额度拒绝时写入。此前被拒绝的账号只设了限流到 reset，没有标记；
-- 限流期间调度不再给它发请求，新版本也就没机会补上。
-- 条件：未删除的 kiro 账号、没有标记、仍在限流中、额度快照主额度用量 >= 100%。
-- 标记时间取 rate_limited_at（没有则取当前时间）；新周期 reset 后仍由后台额度刷新自动清除。
-- 只改 extra 这两个键，不改 updated_at，不影响调度。
UPDATE accounts
SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object(
        'kiro_quota_rejected_at', to_char(COALESCE(rate_limited_at, NOW()) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        'kiro_quota_rejected_reason', 'backfill 261: rate-limited until reset with credit usage >= 100%')
WHERE platform = 'kiro'
  AND deleted_at IS NULL
  AND NOT (COALESCE(extra, '{}'::jsonb) ? 'kiro_quota_rejected_at')
  AND rate_limit_reset_at > NOW()
  AND jsonb_typeof(extra -> 'kiro_usage_snapshot' -> 'kiro_credit' -> 'percentage_used') = 'number'
  AND (extra -> 'kiro_usage_snapshot' -> 'kiro_credit' ->> 'percentage_used')::numeric >= 100;

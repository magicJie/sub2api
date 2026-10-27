-- 链式代理（fork）：首跳组新增 load-balance 策略（mihomo load-balance + round-robin）。
-- 249 的列级 CHECK 由 PostgreSQL 自动命名为 proxy_hop_groups_strategy_check。
-- DROP ... IF EXISTS 保证可重入；新约束是旧约束的超集，存量行瞬时校验通过。
ALTER TABLE proxy_hop_groups
    DROP CONSTRAINT IF EXISTS proxy_hop_groups_strategy_check;

ALTER TABLE proxy_hop_groups
    ADD CONSTRAINT proxy_hop_groups_strategy_check
    CHECK (strategy IN ('fallback', 'url-test', 'load-balance'));

-- 迁移 257（前置组）预览：在事务里执行同一份迁移、打印转换结果，然后 ROLLBACK，不留任何改动。
-- 用法（在 backend/ 下）：psql "$DSN" -v ON_ERROR_STOP=1 -f scripts/preview-257-proxy-upstream-groups.sql
-- 预检不通过时迁移里的 RAISE 会直接报出需要先处理的记录。
-- 注意：ALTER TABLE 会在事务期间短暂持有 proxies 表锁，生产上请在低峰执行。
BEGIN;
\ir ../migrations/257_proxy_upstream_groups.sql

\echo '== 前置端点（由 transport 代理转换）'
SELECT e.id, e.name, e.protocol, e.host, e.port, e.status, e.legacy_proxy_id FROM proxy_upstream_endpoints e ORDER BY e.id;
\echo '== 前置组与槽位'
SELECT g.id, g.name, g.status, s.position, s.name AS slot, s.endpoint_ids
FROM proxy_upstream_groups g LEFT JOIN proxy_upstream_slots s ON s.group_id = g.id ORDER BY g.id, s.position;
\echo '== 落地前置改写（未删除代理）'
SELECT p.id, p.name, p.upstream_mode, p.upstream_group_id, g.name AS group_name,
       (SELECT COUNT(*) FROM accounts a WHERE a.proxy_id = p.id AND a.deleted_at IS NULL) AS accounts
FROM proxies p LEFT JOIN proxy_upstream_groups g ON g.id = p.upstream_group_id
WHERE p.deleted_at IS NULL ORDER BY p.id;
\echo '== 将被软删除的 transport 代理'
SELECT p.id, p.name, p.host, p.port FROM proxies p WHERE p.role = 'transport' AND p.deleted_at >= NOW() - INTERVAL '1 minute' ORDER BY p.id;

ROLLBACK;

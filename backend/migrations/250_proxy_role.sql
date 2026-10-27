-- 代理池（fork）：代理角色。egress = 静态落地 IP（可进代理池、绑账号）；transport = 前置跳板 / 机场 / 自部署加密节点 / 动态代理，只做传输。
-- 升级原则：不改变任何账号当前的代理；有账号在用的代理一律保持 egress。
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS role VARCHAR(20) NOT NULL DEFAULT 'egress';

-- 回填 transport：当前被用作前置代理 / 首跳组成员 / 默认前置代理，且没有任何未删除账号直接使用；同时移出代理池。
UPDATE proxies p
SET role = 'transport', pool_id = NULL, updated_at = NOW()
WHERE p.deleted_at IS NULL
  AND p.role = 'egress'
  AND (
    EXISTS (SELECT 1 FROM proxies x
            WHERE x.deleted_at IS NULL AND x.upstream_mode = 'proxy' AND x.upstream_proxy_id = p.id)
    OR EXISTS (SELECT 1 FROM proxy_hop_groups g, jsonb_array_elements_text(g.member_proxy_ids) m
               WHERE m = p.id::text)
    OR EXISTS (SELECT 1 FROM settings s
               WHERE s.key = 'proxy_chain_default_upstream_proxy_id' AND btrim(s.value) = p.id::text)
  )
  AND NOT EXISTS (SELECT 1 FROM accounts a WHERE a.proxy_id = p.id AND a.deleted_at IS NULL);

-- 落地 IP 去重与按 host 合并计数用。
CREATE INDEX IF NOT EXISTS proxies_lower_host_idx ON proxies (lower(host));

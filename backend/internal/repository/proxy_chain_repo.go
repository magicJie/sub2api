package repository

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// enqueueProxyChainAccountChanges 让绑定该代理的账号的调度器缓存失效（链式代理，fork）。
//
// 调度器把账号连同 *Proxy 快照存进 Redis；代理的前置链/协议参数变化后，缓存中的旧值
// 不应继续参与出站。Proxy.URL() 已优先使用进程快照，这里是额外的兜底；
// 仅在代理（更新后）涉及链路时触发，普通直连代理的更新路径保持上游行为不变。
func enqueueProxyChainAccountChanges(ctx context.Context, client *dbent.Client, p *service.Proxy) error {
	if service.NormalizeUpstreamMode(p.UpstreamMode) == service.UpstreamModeDirect &&
		!proxychain.IsXrayOnlyProtocol(p.Protocol) && len(p.Options) == 0 {
		return nil
	}
	rows, err := client.QueryContext(ctx, `SELECT id FROM accounts WHERE proxy_id = $1 AND deleted_at IS NULL`, p.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return enqueueProxyProbeAccountChanges(ctx, client, ids)
}

package dto

import "github.com/Wei-Shaw/sub2api/internal/service"

// ProxyChainFields 是代理 DTO 上的链式代理字段（fork，嵌入 AdminProxy）。
type ProxyChainFields struct {
	UpstreamMode    string            `json:"upstream_mode"`
	UpstreamGroupID *int64            `json:"upstream_group_id"`
	Options         map[string]string `json:"options"`
	Chain           []ProxyChainHop   `json:"chain"`
	ChainError      string            `json:"chain_error,omitempty"`
	PoolID          *int64            `json:"pool_id"` // 代理池（fork）
}

// ProxyChainHop 是展示用的当前前置端点（不含代理自身），name 形如「组/槽位/端点」。
type ProxyChainHop struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

func proxyChainFieldsFromService(p *service.Proxy) ProxyChainFields {
	options := p.Options
	if options == nil {
		options = map[string]string{}
	}
	hops, chainErr := service.DescribeProxyChain(p)
	chain := make([]ProxyChainHop, 0, len(hops))
	for _, h := range hops {
		chain = append(chain, ProxyChainHop{ID: h.ID, Name: h.Name, Protocol: h.Protocol})
	}
	return ProxyChainFields{
		UpstreamMode:    service.NormalizeUpstreamMode(p.UpstreamMode),
		UpstreamGroupID: p.UpstreamGroupID,
		Options:         options,
		Chain:           chain,
		ChainError:      chainErr,
		PoolID:          p.PoolID,
	}
}

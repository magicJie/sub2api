package admin

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 链式代理（fork）：导入导出时携带前置组与协议参数，前置组按 name 跨实例反查。
// 前置端点与前置组本身不随代理导出（端点是各环境自己的 AIO listener），目标实例需先建好同名前置组。

// DataProxyChain 嵌入 DataProxy。
type DataProxyChain struct {
	UpstreamMode      string            `json:"upstream_mode,omitempty"`
	UpstreamGroupName string            `json:"upstream_group_name,omitempty"`
	Options           map[string]string `json:"options,omitempty"`
}

func dataProxyChainFromService(p *service.Proxy) DataProxyChain {
	out := DataProxyChain{Options: p.Options}
	if service.NormalizeUpstreamMode(p.UpstreamMode) == service.UpstreamModeGroup {
		out.UpstreamMode = service.UpstreamModeGroup
		out.UpstreamGroupName = service.ProxyUpstreamGroupName(p.UpstreamGroupID)
	}
	return out
}

// toInput 把导入项转为创建参数；前置组解析不到时 UpstreamGroupID 为 nil，
// 由 CreateProxy 的校验拒绝创建（不降级为直连，避免绕过前置暴露真实出口）。
func (d DataProxyChain) toInput() service.ProxyChainInput {
	in := service.ProxyChainInput{UpstreamMode: d.UpstreamMode, Options: d.Options}
	if service.NormalizeUpstreamMode(d.UpstreamMode) == service.UpstreamModeGroup {
		in.UpstreamGroupID = service.ResolveProxyUpstreamGroupName(d.UpstreamGroupName)
	}
	return in
}

// reuseProxyChain 在复用的已有代理前置配置与导入项不一致时返回提示（不改动已有代理）。
func reuseProxyChain(item DataProxy, existing *service.Proxy) string {
	want := service.NormalizeUpstreamMode(item.UpstreamMode)
	got := service.NormalizeUpstreamMode(existing.UpstreamMode)
	if want != got {
		return fmt.Sprintf("existing proxy reused but upstream_mode differs (existing=%s, import=%s); chain settings not changed", got, want)
	}
	if want == service.UpstreamModeGroup {
		if id := service.ResolveProxyUpstreamGroupName(item.UpstreamGroupName); id == nil || existing.UpstreamGroupID == nil || *id != *existing.UpstreamGroupID {
			return "existing proxy reused but upstream group differs; chain settings not changed"
		}
	}
	return ""
}

func reuseProxyChainByID(item DataProxy, id int64, existing []service.Proxy) string {
	for i := range existing {
		if existing[i].ID == id {
			return reuseProxyChain(item, &existing[i])
		}
	}
	return ""
}

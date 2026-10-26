package admin

import (
	"context"
	"fmt"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 链式代理（fork）：导入导出时携带前置代理与协议参数，前置代理按 name 跨实例反查。

// DataProxyChain 嵌入 DataProxy。前置代理优先按 proxy_key 引用（凭据级唯一），name 仅作兜底。
type DataProxyChain struct {
	UpstreamMode         string            `json:"upstream_mode,omitempty"`
	UpstreamProxyKey     string            `json:"upstream_proxy_key,omitempty"`
	UpstreamProxyName    string            `json:"upstream_proxy_name,omitempty"`
	UpstreamHopGroupName string            `json:"upstream_hop_group_name,omitempty"`
	Options              map[string]string `json:"options,omitempty"`
}

func dataProxyChainFromService(p *service.Proxy, exported []service.Proxy) DataProxyChain {
	out := DataProxyChain{Options: p.Options}
	mode := service.NormalizeUpstreamMode(p.UpstreamMode)
	if mode != service.UpstreamModeDirect {
		out.UpstreamMode = mode
	}
	groupID := p.UpstreamHopGroupID
	if mode == service.UpstreamModeDefault {
		groupID = service.DefaultUpstreamHopGroupID()
	}
	if groupID != nil {
		out.UpstreamHopGroupName = service.ProxyHopGroupName(groupID)
	}
	// 跟随默认时也记录导出时的默认前置代理，供目标实例未配置默认值时改为显式引用。
	ref := p.UpstreamProxyID
	if mode == service.UpstreamModeDefault {
		ref = service.DefaultUpstreamProxyID()
	}
	if mode != service.UpstreamModeDirect && ref != nil {
		for i := range exported {
			if exported[i].ID == *ref {
				u := exported[i]
				out.UpstreamProxyKey = buildProxyKey(u.Protocol, u.Host, u.Port, u.Username, u.Password)
				out.UpstreamProxyName = u.Name
				break
			}
		}
	}
	return out
}

// proxyChainRefs 记录导入过程中可被引用的代理（目标库已有 + 本批次新建/复用）。
type proxyChainRefs struct {
	byKey  map[string]int64
	byName map[string][]int64
}

func newProxyChainRefs(existing []service.Proxy) *proxyChainRefs {
	r := &proxyChainRefs{byKey: map[string]int64{}, byName: map[string][]int64{}}
	for i := range existing {
		p := existing[i]
		r.add(p.Name, buildProxyKey(p.Protocol, p.Host, p.Port, p.Username, p.Password), p.ID)
	}
	return r
}

func (r *proxyChainRefs) add(name, key string, id int64) {
	if key != "" {
		r.byKey[key] = id
	}
	if name == "" {
		return
	}
	for _, existing := range r.byName[name] {
		if existing == id {
			return
		}
	}
	r.byName[name] = append(r.byName[name], id)
}

// resolve 返回导入项引用的前置代理 id；按 name 兜底时同名多个视为无法解析（不猜）。
func (r *proxyChainRefs) resolve(d DataProxyChain) *int64 {
	if d.UpstreamProxyKey != "" {
		if id, ok := r.byKey[d.UpstreamProxyKey]; ok {
			return &id
		}
	}
	if ids := r.byName[d.UpstreamProxyName]; d.UpstreamProxyName != "" && len(ids) == 1 {
		id := ids[0]
		return &id
	}
	return nil
}

// toInput 把导入项转为创建参数；指定的前置代理解析不到时 UpstreamProxyID 为 nil，
// 由 CreateProxy 的校验拒绝创建（不降级为直连，避免绕过前置代理暴露真实出口）。
// 跟随默认但目标实例没有默认前置代理时，改为显式指定导出时的默认前置代理（仍不降级直连）。
func (d DataProxyChain) toInput(refs *proxyChainRefs) service.ProxyChainInput {
	in := service.ProxyChainInput{UpstreamMode: d.UpstreamMode, Options: d.Options}
	switch service.NormalizeUpstreamMode(d.UpstreamMode) {
	case service.UpstreamModeProxy:
		in.UpstreamProxyID = refs.resolve(d)
	case service.UpstreamModeHopGroup:
		in.UpstreamHopGroupID = service.ResolveProxyHopGroupName(d.UpstreamHopGroupName)
	case service.UpstreamModeDefault:
		if service.DefaultUpstreamProxyID() == nil && service.DefaultUpstreamHopGroupID() == nil {
			if d.UpstreamHopGroupName != "" {
				in.UpstreamMode = service.UpstreamModeHopGroup
				in.UpstreamHopGroupID = service.ResolveProxyHopGroupName(d.UpstreamHopGroupName)
				break
			}
			if id := refs.resolve(d); id != nil {
				in.UpstreamMode = service.UpstreamModeProxy
				in.UpstreamProxyID = id
			}
		}
	}
	return in
}

// reuse 登记被复用的已有代理，并在其链路配置与导入项不一致时返回提示（不改动已有代理）。
func (r *proxyChainRefs) reuse(item DataProxy, key string, existing *service.Proxy) string {
	r.add(item.Name, key, existing.ID)
	want := service.NormalizeUpstreamMode(item.UpstreamMode)
	got := service.NormalizeUpstreamMode(existing.UpstreamMode)
	if want != got {
		return fmt.Sprintf("existing proxy reused but upstream_mode differs (existing=%s, import=%s); chain settings not changed", got, want)
	}
	if want == service.UpstreamModeProxy {
		if id := r.resolve(item.DataProxyChain); id == nil || existing.UpstreamProxyID == nil || *id != *existing.UpstreamProxyID {
			return "existing proxy reused but upstream proxy differs; chain settings not changed"
		}
	}
	if want == service.UpstreamModeHopGroup {
		if id := service.ResolveProxyHopGroupName(item.UpstreamHopGroupName); id == nil || existing.UpstreamHopGroupID == nil || *id != *existing.UpstreamHopGroupID {
			return "existing proxy reused but first-hop group differs; chain settings not changed"
		}
	}
	return ""
}

func (r *proxyChainRefs) reuseID(item DataProxy, key string, id int64, existing []service.Proxy) string {
	for i := range existing {
		if existing[i].ID == id {
			return r.reuse(item, key, &existing[i])
		}
	}
	r.add(item.Name, key, id)
	return ""
}

// orderDataProxiesForImport 让被引用的前置代理先于引用者导入（批内按 name 引用）。
func orderDataProxiesForImport(items []DataProxy) []DataProxy {
	byKey := make(map[string]int, len(items))
	byName := make(map[string]int, len(items))
	for i := range items {
		key := items[i].ProxyKey
		if key == "" {
			key = buildProxyKey(items[i].Protocol, items[i].Host, items[i].Port, items[i].Username, items[i].Password)
		}
		byKey[key] = i
		if items[i].Name != "" {
			byName[items[i].Name] = i
		}
	}
	refOf := func(it DataProxy) (int, bool) {
		if it.UpstreamProxyKey != "" {
			if j, ok := byKey[it.UpstreamProxyKey]; ok {
				return j, true
			}
		}
		if it.UpstreamProxyName == "" {
			return 0, false
		}
		j, ok := byName[it.UpstreamProxyName]
		return j, ok
	}
	depth := make([]int, len(items))
	for i := range items {
		cur, d := i, 0
		for d <= len(items) {
			j, ok := refOf(items[cur])
			if !ok || j == cur {
				break
			}
			cur, d = j, d+1
		}
		depth[i] = d
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return depth[order[a]] < depth[order[b]] })
	out := make([]DataProxy, len(items))
	for i, j := range order {
		out[i] = items[j]
	}
	return out
}

// appendUpstreamProxies 把导出代理的前置代理（递归）一并加入导出集合；
// 跟随默认前置代理的代理会连同当前默认前置代理一起导出（导入方需自行设置默认前置代理）。
func appendUpstreamProxies(ctx context.Context, svc service.AdminService, proxies []service.Proxy) ([]service.Proxy, error) {
	seen := make(map[int64]struct{}, len(proxies))
	for i := range proxies {
		seen[proxies[i].ID] = struct{}{}
	}
	pending := proxies
	for depth := 0; depth < 8 && len(pending) > 0; depth++ {
		var ids []int64
		for i := range pending {
			p := pending[i]
			var ref *int64
			switch service.NormalizeUpstreamMode(p.UpstreamMode) {
			case service.UpstreamModeProxy:
				ref = p.UpstreamProxyID
			case service.UpstreamModeDefault:
				ref = service.DefaultUpstreamProxyID()
			}
			if ref == nil {
				continue
			}
			if _, ok := seen[*ref]; ok {
				continue
			}
			seen[*ref] = struct{}{}
			ids = append(ids, *ref)
		}
		if len(ids) == 0 {
			break
		}
		more, err := svc.GetProxiesByIDs(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("load upstream proxies: %w", err)
		}
		proxies = append(proxies, more...)
		pending = more
	}
	return proxies, nil
}

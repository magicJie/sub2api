//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func chainTestProxies() map[int64]Proxy {
	return map[int64]Proxy{
		1: {ID: 1, Name: "ss-front", Protocol: "ss", Host: "ss.example", Port: 8388, Password: "p",
			Options: map[string]string{"method": "aes-256-gcm"}, Status: StatusActive, UpstreamMode: UpstreamModeDirect},
		2: {ID: 2, Name: "exit-http", Protocol: "http", Host: "exit.example", Port: 8080, Status: StatusActive, UpstreamMode: UpstreamModeProxy, UpstreamProxyID: i64(1)},
		3: {ID: 3, Name: "follow-default", Protocol: "socks5", Host: "s.example", Port: 1080, Status: StatusActive, UpstreamMode: UpstreamModeDefault},
		4: {ID: 4, Name: "disabled", Protocol: "http", Host: "d.example", Port: 80, Status: "inactive"},
		5: {ID: 5, Name: "plain", Protocol: "http", Host: "plain.example", Port: 80, Status: StatusActive},
	}
}

func TestResolveProxyChain(t *testing.T) {
	byID := chainTestProxies()
	now := time.Now()

	p2 := byID[2]
	chain, err := resolveProxyChain(&p2, byID, nil, now)
	if err != nil || len(chain) != 2 || chain[0].ID != 1 || chain[1].ID != 2 {
		t.Fatalf("explicit upstream: chain=%v err=%v", chain, err)
	}

	p3 := byID[3]
	chain, err = resolveProxyChain(&p3, byID, i64(2), now)
	if err != nil || len(chain) != 3 || chain[0].ID != 1 || chain[1].ID != 2 || chain[2].ID != 3 {
		t.Fatalf("default upstream: chain=%v err=%v", chain, err)
	}
	// 未配置默认前置代理时 default 模式必须 fail-closed，不能悄悄直连。
	if _, err = resolveProxyChain(&p3, byID, nil, now); err == nil {
		t.Fatalf("default without setting must fail")
	}
	// 默认前置代理自身跟随默认时不形成自环。
	p1 := byID[1]
	p1.UpstreamMode = UpstreamModeDefault
	chain, err = resolveProxyChain(&p1, byID, i64(1), now)
	if err != nil || len(chain) != 1 {
		t.Fatalf("default self: chain=%v err=%v", chain, err)
	}
}

func TestResolveProxyChainErrors(t *testing.T) {
	now := time.Now()
	cases := map[string]func(map[int64]Proxy) Proxy{
		"loop": func(m map[int64]Proxy) Proxy {
			a := m[1]
			a.UpstreamMode, a.UpstreamProxyID = UpstreamModeProxy, i64(2)
			m[1] = a
			return m[2]
		},
		"inactive upstream": func(m map[int64]Proxy) Proxy {
			p := m[5]
			p.UpstreamMode, p.UpstreamProxyID = UpstreamModeProxy, i64(4)
			return p
		},
		"missing upstream": func(m map[int64]Proxy) Proxy {
			p := m[5]
			p.UpstreamMode, p.UpstreamProxyID = UpstreamModeProxy, i64(99)
			return p
		},
		"expired upstream": func(m map[int64]Proxy) Proxy {
			past := now.Add(-time.Hour)
			u := m[1]
			u.ExpiresAt = &past
			m[1] = u
			return m[2]
		},
		"too deep": func(m map[int64]Proxy) Proxy {
			for i := int64(10); i < 16; i++ {
				p := Proxy{ID: i, Name: "n", Protocol: "http", Host: "h", Port: 1, Status: StatusActive}
				if i > 10 {
					p.UpstreamMode, p.UpstreamProxyID = UpstreamModeProxy, i64(i-1)
				}
				m[i] = p
			}
			return m[15]
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			m := chainTestProxies()
			p := build(m)
			if _, err := resolveProxyChain(&p, m, nil, now); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestProxyURLUsesChain(t *testing.T) {
	byID := chainTestProxies()
	proxyChainState.Store(&proxyChainSnapshot{byID: byID, loaded: true})
	t.Cleanup(func() { proxyChainState.Store(nil) })

	// 快照未加载时，需要链路的代理 fail-closed。
	proxyChainState.Store(&proxyChainSnapshot{byID: byID})
	if u := (&Proxy{ID: 2, Protocol: "http", Host: "exit.example", Port: 8080, UpstreamMode: UpstreamModeProxy, UpstreamProxyID: i64(1)}).URL(); u != proxyChainInvalidURL {
		t.Fatalf("unloaded snapshot must fail closed, got %q", u)
	}
	proxyChainState.Store(&proxyChainSnapshot{byID: byID, loaded: true})

	// 调用方持有过期副本（如调度器缓存里仍是直连），以快照里的链配置为准。
	stale := Proxy{ID: 2, Name: "exit-http", Protocol: "http", Host: "exit.example", Port: 8080, Status: StatusActive}
	if u := stale.URL(); !strings.HasPrefix(u, "socks5h://") {
		t.Fatalf("stale copy must follow snapshot chain, got %q", u)
	}

	plain := byID[5]
	if got := plain.URL(); got != "http://plain.example:80" {
		t.Fatalf("plain proxy URL changed: %q", got)
	}

	exit := byID[2]
	got := exit.URL()
	if !strings.HasPrefix(got, "socks5h://") || !strings.Contains(got, "@127.0.0.1:") {
		t.Fatalf("chained proxy URL = %q", got)
	}
	ss := byID[1]
	if u := ss.URL(); !strings.HasPrefix(u, "socks5h://") || u == got {
		t.Fatalf("ss proxy URL = %q (chain %q)", u, got)
	}

	broken := byID[5]
	broken.ID = 6 // 不在快照中，按调用方传入的配置解析
	broken.UpstreamMode, broken.UpstreamProxyID = UpstreamModeProxy, i64(4)
	if u := broken.URL(); u != proxyChainInvalidURL {
		t.Fatalf("broken chain must fail closed, got %q", u)
	}

	hops, errMsg := DescribeProxyChain(&exit)
	if errMsg != "" || len(hops) != 1 || hops[0].Name != "ss-front" {
		t.Fatalf("describe: %v %q", hops, errMsg)
	}
}

func TestValidateDependentChains(t *testing.T) {
	byID := chainTestProxies()
	// 把 ss-front 改为经 exit-http 出站，会让 exit-http 成环。
	front := byID[1]
	front.UpstreamMode, front.UpstreamProxyID = UpstreamModeProxy, i64(2)
	byID[1] = front
	if err := validateDependentChains(byID, nil, 1); err == nil {
		t.Fatalf("expected loop error for dependent proxy")
	}

	// 清除默认前置代理时，跟随默认的代理会失效，必须拒绝。
	byID = chainTestProxies()
	if err := validateDependentChains(byID, nil, 0); err == nil {
		t.Fatalf("clearing default with dependents must fail")
	}
	if err := validateDependentChains(byID, i64(1), 0); err != nil {
		t.Fatalf("valid default: %v", err)
	}
}

func TestValidateAllowsDisablingUpstream(t *testing.T) {
	byID := chainTestProxies()
	repo := &chainListRepo{proxies: byID}
	front := byID[1]
	front.Status = "inactive"
	if err := validateProxyChainConfig(context.Background(), repo, nil, &front); err != nil {
		t.Fatalf("disabling an upstream proxy must be allowed: %v", err)
	}
	// 启用状态下制造环路仍然被拒绝。
	front = byID[1]
	front.UpstreamMode, front.UpstreamProxyID = UpstreamModeProxy, i64(2)
	if err := validateProxyChainConfig(context.Background(), repo, nil, &front); err == nil {
		t.Fatalf("loop must be rejected")
	}
}

type chainListRepo struct {
	ProxyRepository
	proxies map[int64]Proxy
}

func (r *chainListRepo) ListAllForFallback(context.Context) ([]Proxy, error) {
	out := make([]Proxy, 0, len(r.proxies))
	for _, p := range r.proxies {
		out = append(out, p)
	}
	return out, nil
}

//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestResolveFirstHopGroupChains(t *testing.T) {
	groupID := int64(7)
	groups := map[int64]ProxyHopGroup{7: {ID: 7, Name: "airport", Status: StatusActive}}
	front := Proxy{ID: 1, Name: "middle", Status: StatusActive, Protocol: "socks5", UpstreamMode: UpstreamModeHopGroup, UpstreamHopGroupID: &groupID}
	exit := Proxy{ID: 2, Name: "landing", Status: StatusActive, Protocol: "socks5", UpstreamMode: UpstreamModeProxy, UpstreamProxyID: i64(1)}
	proxies := map[int64]Proxy{1: front, 2: exit}
	chain, err := resolveProxyChainWithGroups(&exit, proxies, nil, groups, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 || chain[0].ID != -7 || chain[0].Protocol != UpstreamModeHopGroup || chain[1].ID != 1 || chain[2].ID != 2 {
		t.Fatalf("unexpected chain: %+v", chain)
	}
	exit.UpstreamMode = UpstreamModeDefault
	exit.UpstreamProxyID = nil
	chain, err = resolveProxyChainWithGroups(&exit, proxies, nil, groups, &groupID, time.Now())
	if err != nil || len(chain) != 2 || chain[0].ID != -7 {
		t.Fatalf("default group chain: %+v, %v", chain, err)
	}
	groups[7] = ProxyHopGroup{ID: 7, Status: "inactive"}
	if _, err := resolveProxyChainWithGroups(&exit, proxies, nil, groups, &groupID, time.Now()); err == nil {
		t.Fatal("inactive default group must fail closed")
	}
	delete(groups, 7)
	if _, err := resolveProxyChainWithGroups(&front, proxies, nil, groups, nil, time.Now()); err == nil {
		t.Fatal("missing explicit group must fail closed")
	}
}

func TestAuditRedactsSubscriptionURLs(t *testing.T) {
	body := []byte(`{"name":"airport","subscription_urls":["https://subscription.invalid/clash?token=unit-audit-secret"]}`)
	result := RedactAuditBody(body, "application/json")
	if strings.Contains(result, "unit-audit-secret") || strings.Contains(result, "subscription.invalid") {
		t.Fatal("audit body leaked subscription credentials")
	}
	if !strings.Contains(result, "airport") {
		t.Fatal("audit redaction unexpectedly removed nonsensitive group name")
	}
}

type hopGroupValidationProxyRepo struct{ ProxyRepository }

func (hopGroupValidationProxyRepo) ListAllForFallback(context.Context) ([]Proxy, error) {
	return nil, nil
}

func TestValidateHopGroupStrategy(t *testing.T) {
	svc := &ProxyChainService{proxyRepo: hopGroupValidationProxyRepo{}}
	for _, strategy := range []string{"", "fallback", "url-test", "load-balance"} {
		group := ProxyHopGroup{Name: "airport", Strategy: strategy, SubscriptionURLs: []string{"https://subscription.invalid/clash"}}
		if err := svc.validateHopGroup(context.Background(), &group); err != nil {
			t.Fatalf("strategy %q rejected: %v", strategy, err)
		}
	}
	for _, strategy := range []string{"round-robin", "consistent-hashing", "sticky-sessions", "select", "LOAD-BALANCE"} {
		group := ProxyHopGroup{Name: "airport", Strategy: strategy, SubscriptionURLs: []string{"https://subscription.invalid/clash"}}
		if err := svc.validateHopGroup(context.Background(), &group); err == nil {
			t.Fatalf("strategy %q accepted", strategy)
		}
	}
}

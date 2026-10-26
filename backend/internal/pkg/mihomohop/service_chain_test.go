//go:build unit

package mihomohop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/hopgroup"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type chainProxyRepo struct {
	service.ProxyRepository
	proxies []service.Proxy
}

func (r *chainProxyRepo) ListAllForFallback(context.Context) ([]service.Proxy, error) {
	return r.proxies, nil
}

type chainGroupRepo struct {
	service.ProxyHopGroupRepository
	groups []service.ProxyHopGroup
}

func (r *chainGroupRepo) List(context.Context) ([]service.ProxyHopGroup, error) { return r.groups, nil }

func TestServiceFirstHopGroupURLAndRetain(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(204)
			return
		}
		_, _ = io.WriteString(w, "service-chain-ok")
	}))
	defer target.Close()
	first := localMember(t).Endpoint()
	landingEndpoint := localMember(t).Endpoint()
	groupID := int64(7)
	landing := service.Proxy{ID: 2, Name: "landing", Protocol: "socks5", Host: landingEndpoint.Host, Port: landingEndpoint.Port, Username: landingEndpoint.Username, Password: landingEndpoint.Password, Status: service.StatusActive, UpstreamMode: service.UpstreamModeHopGroup, UpstreamHopGroupID: &groupID}
	proxies := &chainProxyRepo{proxies: []service.Proxy{{ID: 1, Name: "manual", Protocol: "socks5", Host: first.Host, Port: first.Port, Username: first.Username, Password: first.Password, Status: service.StatusActive, UpstreamMode: service.UpstreamModeDirect}, landing}}
	groups := &chainGroupRepo{groups: []service.ProxyHopGroup{{ID: groupID, Name: "airport", Strategy: "fallback", TestURL: target.URL + "/health", IntervalSeconds: 1, ToleranceMS: 50, SubscriptionUpdateInterval: 3600, MemberProxyIDs: []int64{1}, Status: service.StatusActive}}}
	manager := &Manager{}
	chainService := service.NewProxyChainService(proxies, nil, groups, manager)
	chainService.Start()
	defer chainService.Stop()
	waitStatus(t, manager, groupID, func(s hopgroup.GroupStatus) bool {
		return len(s.Nodes) == 1 && s.Nodes[0].Alive && s.Nodes[0].DelayMS != 65535
	})
	oldURL := landing.URL()
	parsedEntry, parseErr := url.Parse(oldURL)
	if parseErr != nil || parsedEntry.Scheme != "socks5h" || parsedEntry.Hostname() != "127.0.0.1" || parsedEntry.User == nil {
		t.Fatal("service did not resolve group into local Xray SOCKS entry")
	}
	get := func(raw string) (string, error) {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(parsed), DisableKeepAlives: true}}
		response, err := client.Get(target.URL + "/probe")
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return string(body), err
	}
	if body, err := get(oldURL); err != nil || body != "service-chain-ok" {
		t.Fatalf("service group chain failed: %q, %v", body, err)
	}
	groups.groups[0].Name = "renamed"
	if err := chainService.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	newURL := landing.URL()
	if oldURL == newURL {
		t.Fatal("group change did not replace cached Xray chain")
	}
	if _, err := get(oldURL); err == nil {
		t.Fatal("Retain left old Xray chain credentials usable")
	}
	waitStatus(t, manager, groupID, func(s hopgroup.GroupStatus) bool {
		return len(s.Nodes) == 1 && s.Nodes[0].Alive && s.Nodes[0].DelayMS != 65535
	})
	if body, err := get(newURL); err != nil || body != "service-chain-ok" {
		t.Fatalf("replacement service group chain failed: %q, %v", body, err)
	}
}

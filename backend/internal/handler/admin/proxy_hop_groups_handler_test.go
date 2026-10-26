//go:build unit

package admin

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strings"
	"testing"
)

func TestHopGroupResponsesDoNotExposeSubscriptionCredentials(t *testing.T) {
	secret := "https://subscription.invalid/clash?token=unit-secret"
	group := &service.ProxyHopGroup{ID: 1, Name: "first", SubscriptionURLs: []string{secret}}
	for name, value := range map[string]any{"admin": redactedHopGroup(group), "model": group} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) || strings.Contains(string(data), "unit-secret") {
			t.Fatalf("%s response leaked subscription credentials", name)
		}
	}
	response := redactedHopGroup(group)
	if response.SubscriptionCount != 1 || response.SubscriptionURLs[0] != "[REDACTED]" {
		t.Fatal("redacted response lost configured subscription count")
	}
}

func TestHopGroupUpdatePreservesOmittedSubscriptions(t *testing.T) {
	group := &service.ProxyHopGroup{SubscriptionURLs: []string{"http://subscription.invalid/unit"}}
	var req proxyHopGroupRequest
	if err := json.Unmarshal([]byte(`{"name":"renamed"}`), &req); err != nil {
		t.Fatal(err)
	}
	req.apply(group)
	if len(group.SubscriptionURLs) != 1 {
		t.Fatal("omitting subscription URLs cleared stored credentials")
	}
	if err := json.Unmarshal([]byte(`{"subscription_urls":[]}`), &req); err != nil {
		t.Fatal(err)
	}
	req.apply(group)
	if len(group.SubscriptionURLs) != 0 {
		t.Fatal("explicit empty subscription list did not clear stored credentials")
	}
}

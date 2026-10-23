package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/kiro/anthropictokenizer"
)

func TestKiroCacheEmulationRatioFromAccountExtra(t *testing.T) {
	cases := []struct {
		name    string
		account *Account
		want    float64
	}{
		{"nil", nil, 0},
		{"not kiro", &Account{Platform: PlatformAnthropic, Extra: map[string]any{"kiro_cache_emulation_enabled": true}}, 0},
		{"disabled", &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_ratio": 0.5}}, 0},
		{"default ratio", &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": true}}, 1},
		{"explicit ratio", &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": true, "kiro_cache_emulation_ratio": 0.5}}, 0.5},
		{"clamped", &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": true, "kiro_cache_emulation_ratio": 3.0}}, 1},
		{"non-positive", &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": true, "kiro_cache_emulation_ratio": -1.0}}, 0},
	}
	for _, tc := range cases {
		if got := kiroCacheEmulationRatio(tc.account); got != tc.want {
			t.Fatalf("%s: ratio = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestKiroCacheEmulationUsesSnapshotGroupWithoutRepo(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheEnabled(&Account{ID: 34, Platform: PlatformKiro}, 1)
	first := svc.buildKiroCacheEmulationUsage(account, nil, kiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 || first.CacheReadInputTokens != 0 || first.InputTokens != 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, nil, kiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if second == nil || second.CacheReadInputTokens != 2000 || second.CacheCreationInputTokens != 0 || second.InputTokens != 0 {
		t.Fatalf("unexpected second usage: %+v", second)
	}
}

func TestKiroCacheEmulationCreatesAutomaticBreakpointForOpenAICompatiblePrefix(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheEnabled(&Account{ID: 35, Platform: PlatformKiro}, 1)
	system := strings.Repeat("stable project instructions ", 300)
	firstBody := []byte(fmt.Sprintf(`{"model":"gpt-5.6-terra","system":"%s","messages":[{"role":"user","content":"first request"}]}`, system))
	secondBody := []byte(fmt.Sprintf(`{"model":"gpt-5.6-terra","system":"%s","messages":[{"role":"user","content":"first request"},{"role":"assistant","content":"first response"},{"role":"user","content":"follow up"}]}`, system))

	first := svc.buildKiroCacheEmulationUsage(account, nil, firstBody, "gpt-5.6-terra", 3000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected automatic cache creation, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, nil, secondBody, "gpt-5.6-terra", 3200)
	if second == nil || second.CacheReadInputTokens <= 0 {
		t.Fatalf("expected automatic cache read, got %+v", second)
	}
}

func TestKiroCacheEmulationRatioScalesTokens(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheEnabled(&Account{ID: 78, Platform: PlatformKiro}, 0.5)
	usage := svc.buildKiroCacheEmulationUsage(account, nil, kiroCacheRequestBody("ratio", false), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 1000 || usage.InputTokens != 1000 {
		t.Fatalf("unexpected scaled usage: %+v", usage)
	}
	disabled := &Account{ID: 79, Platform: PlatformKiro}
	if got := svc.buildKiroCacheEmulationUsage(disabled, nil, kiroCacheRequestBody("disabled", false), "claude-sonnet-4-6", 2000); got != nil {
		t.Fatalf("disabled account should skip cache emulation, got %+v", got)
	}
}

func TestKiroCacheEmulationAccountIsolation(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	body := kiroCacheRequestBody("account isolation", false)
	first := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(1, "refresh-a", "access-a"), nil, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	otherAccount := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(2, "refresh-b", "access-b"), nil, body, "claude-sonnet-4-6", 2000)
	if otherAccount == nil || otherAccount.CacheCreationInputTokens != 2000 || otherAccount.CacheReadInputTokens != 0 {
		t.Fatalf("cache should be isolated by account: %+v", otherAccount)
	}
}

func TestKiroCacheEmulationStableCredentialIsolation(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	body := kiroCacheRequestBody("credential isolation", false)
	first := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(7, "refresh-same", "access-a"), nil, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	rotatedAccessToken := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(7, "refresh-same", "access-b"), nil, body, "claude-sonnet-4-6", 2000)
	if rotatedAccessToken == nil || rotatedAccessToken.CacheReadInputTokens != 2000 || rotatedAccessToken.CacheCreationInputTokens != 0 {
		t.Fatalf("access token rotation should not break cache: %+v", rotatedAccessToken)
	}
	differentCredential := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(7, "refresh-other", "access-c"), nil, body, "claude-sonnet-4-6", 2000)
	if differentCredential == nil || differentCredential.CacheReadInputTokens != 0 || differentCredential.CacheCreationInputTokens != 2000 {
		t.Fatalf("different stable credential should not share cache: %+v", differentCredential)
	}
}

func TestKiroCacheEmulationContentChangeMisses(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheEnabled(&Account{ID: 3, Platform: PlatformKiro}, 1)
	_ = svc.buildKiroCacheEmulationUsage(account, nil, kiroCacheRequestBody("before", false), "claude-sonnet-4-6", 2000)
	changed := svc.buildKiroCacheEmulationUsage(account, nil, kiroCacheRequestBody("after", false), "claude-sonnet-4-6", 2000)
	if changed == nil || changed.CacheCreationInputTokens != 2000 || changed.CacheReadInputTokens != 0 {
		t.Fatalf("changed content should miss: %+v", changed)
	}
}

func TestKiroCacheEmulationTTLExpiry(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheEnabled(&Account{ID: 4, Platform: PlatformKiro}, 1)
	body := kiroCacheRequestBody("ttl", false)
	_ = svc.buildKiroCacheEmulationUsage(account, nil, body, "claude-sonnet-4-6", 2000)
	globalKiroCacheTracker.mu.Lock()
	for accountID, entries := range globalKiroCacheTracker.entries {
		for fp, entry := range entries {
			entry.expiresAt = time.Now().Add(-time.Second)
			globalKiroCacheTracker.entries[accountID][fp] = entry
		}
	}
	globalKiroCacheTracker.mu.Unlock()
	afterExpiry := svc.buildKiroCacheEmulationUsage(account, nil, body, "claude-sonnet-4-6", 2000)
	if afterExpiry == nil || afterExpiry.CacheCreationInputTokens != 2000 || afterExpiry.CacheReadInputTokens != 0 {
		t.Fatalf("expired cache should be recreated: %+v", afterExpiry)
	}
}

func TestKiroCacheEmulationOneHourBucket(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	usage := svc.buildKiroCacheEmulationUsage(kiroCacheEnabled(&Account{ID: 5, Platform: PlatformKiro}, 1), nil, kiroCacheRequestBody("1h", true), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 2000 || usage.CacheCreation1hInputTokens != 2000 || usage.CacheCreation5mInputTokens != 0 {
		t.Fatalf("unexpected 1h bucket usage: %+v", usage)
	}
}

func TestKiroCacheEmulationPrefixPartialHit(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheEnabled(&Account{ID: 6, Platform: PlatformKiro}, 1)
	firstBody := kiroCacheMultiMessageBody("cached prefix", "tail one")
	secondBody := kiroCacheMultiMessageBody("cached prefix", "tail two")
	first := svc.buildKiroCacheEmulationUsage(account, nil, firstBody, "claude-sonnet-4-6", 6000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, nil, secondBody, "claude-sonnet-4-6", 6000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheReadInputTokens >= first.CacheCreationInputTokens || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected partial prefix hit: %+v", second)
	}
}

func TestKiroInputTokenEstimateIgnoresClientMetadata(t *testing.T) {
	bodyWithoutMetadata := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello world"}]}`)
	bodyWithMetadata := []byte(`{"model":"claude-sonnet-4-6","metadata":{"input_tokens":999999},"messages":[{"role":"user","content":"hello world"}]}`)
	withoutMetadata := estimateKiroInputTokens(bodyWithoutMetadata)
	withMetadata := estimateKiroInputTokens(bodyWithMetadata)
	if withMetadata == 999999 {
		t.Fatal("client metadata.input_tokens must not be trusted")
	}
	if withMetadata <= 0 || withoutMetadata <= 0 || withMetadata > withoutMetadata*2 {
		t.Fatalf("unexpected estimates without=%d with=%d", withoutMetadata, withMetadata)
	}
}

func TestKiroTokenCountersMatchReferenceRules(t *testing.T) {
	if got := anthropictokenizer.CountTokens("abc def"); got != 1 {
		t.Fatalf("english tokens = %d, want 1", got)
	}
	if got := anthropictokenizer.CountTokens("你好世界"); got != 1 {
		t.Fatalf("cjk tokens = %d, want 1", got)
	}
	if kiroTokensPerTool != 150 {
		t.Fatalf("tool tokens = %d, want 150", kiroTokensPerTool)
	}
	if got := countKiroMessageContentTokens(map[string]any{"thinking": "abc def"}); got != 1 {
		t.Fatalf("thinking tokens = %d, want 1", got)
	}
	if got := countKiroMessageContentTokens(map[string]any{"input": map[string]any{"path": "/tmp/a.txt"}}); got <= 0 {
		t.Fatalf("tool input tokens should be positive, got %d", got)
	}
	if got := countKiroMessageContentTokens(map[string]any{"content": []any{map[string]any{"text": "abc"}, map[string]any{"text": "你好"}}}); got != 2 {
		t.Fatalf("tool result content tokens = %d, want 2", got)
	}
}

func resetKiroCacheTracker() {
	globalKiroCacheTracker = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
}

func kiroCacheEnabled(account *Account, ratio float64) *Account {
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra["kiro_cache_emulation_enabled"] = true
	account.Extra["kiro_cache_emulation_ratio"] = ratio
	return account
}

func kiroCacheAccount(id int64, refreshToken string, accessToken string) *Account {
	return kiroCacheEnabled(&Account{ID: id, Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"client_id":     "client-id",
		"refresh_token": refreshToken,
		"access_token":  accessToken,
	}}, 1)
}

func kiroCacheRequestBody(label string, oneHour bool) []byte {
	ttl := ""
	if oneHour {
		ttl = `,"ttl":"1h"`
	}
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"%s}}]}]}`, strings.Repeat("cacheable prompt chunk "+label+" ", 512), ttl))
}

func kiroCacheMultiMessageBody(prefixLabel, tailLabel string) []byte {
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	tail := strings.Repeat("conversation growth chunk "+tailLabel+" ", 160)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":%q}]}]}`, prefix, tail))
}

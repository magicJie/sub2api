package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
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
		{"default on without extra", &Account{Platform: PlatformKiro}, 1},
		{"default on with ratio only", &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_ratio": 0.5}}, 0.5},
		{"explicitly disabled", &Account{Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": false, "kiro_cache_emulation_ratio": 0.5}}, 0},
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
	disabled := &Account{ID: 79, Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": false}}
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
	tool := map[string]any{"name": "Bash", "description": strings.Repeat("Run a shell command. ", 200), "input_schema": map[string]any{"type": "object"}}
	if got := countKiroToolDefinitionTokens(tool); got < 500 {
		t.Fatalf("tool tokens should follow the definition size, got %d", got)
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

// Claude Code 形态的多轮请求：大工具定义、带 signature 的 thinking、截图、tool_result。
func kiroClaudeCodeLikeBody(t *testing.T, turns int) []byte {
	t.Helper()
	var png bytes.Buffer
	if err := pngEncodeBlank(&png, 1200, 800); err != nil {
		t.Fatal(err)
	}
	screenshot := base64.StdEncoding.EncodeToString(png.Bytes()) + strings.Repeat("A", 200000)
	tools := []any{}
	for i := 0; i < 20; i++ {
		tools = append(tools, map[string]any{"name": fmt.Sprintf("tool_%d", i), "description": strings.Repeat("Detailed tool usage guidance. ", 80), "input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}})
	}
	messages := []any{}
	for i := 0; i < turns; i++ {
		id := fmt.Sprintf("toolu_%d", i)
		userContent := []any{map[string]any{"type": "text", "text": fmt.Sprintf("step %d: %s", i, strings.Repeat("please continue the work ", 20))}}
		if i > 0 {
			userContent = []any{
				map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprintf("toolu_%d", i-1), "content": []any{
					map[string]any{"type": "text", "text": strings.Repeat("line of tool output with \"quotes\" and\ttabs\n", 120)},
					map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": screenshot}},
				}},
			}
		}
		messages = append(messages,
			map[string]any{"role": "user", "content": userContent},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": strings.Repeat("reasoning ", 50), "signature": strings.Repeat("c2lnbmF0dXJl", 4000)},
				map[string]any{"type": "tool_use", "id": id, "name": "tool_1", "input": map[string]any{"path": fmt.Sprintf("/tmp/%d", i)}},
			}},
		)
	}
	messages = append(messages, map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprintf("toolu_%d", turns-1), "content": "done"},
	}})
	last := messages[len(messages)-1].(map[string]any)["content"].([]any)
	last[len(last)-1].(map[string]any)["cache_control"] = map[string]any{"type": "ephemeral"}
	body, err := json.Marshal(map[string]any{
		"model":    "claude-opus-5-5",
		"tools":    tools,
		"system":   []any{map[string]any{"type": "text", "text": strings.Repeat("You are Claude Code. ", 400), "cache_control": map[string]any{"type": "ephemeral"}}},
		"messages": messages,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func pngEncodeBlank(buf *bytes.Buffer, w, h int) error {
	return png.Encode(buf, image.NewGray(image.Rect(0, 0, w, h)))
}

func TestKiroCacheEmulationClaudeCodeConversationIsMostlyCached(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := kiroCacheAccount(501, "refresh-cc", "access-cc")

	prevBody := kiroClaudeCodeLikeBody(t, 30)
	prevTotal := estimateKiroInputTokens(prevBody)
	profile, ok := buildKiroCacheProfile(prevBody, "claude-opus-5-5", prevTotal)
	if !ok || profile.blocks[len(profile.blocks)-1].cumulativeTokens != prevTotal {
		t.Fatalf("total %d must equal cumulative prefix tokens %d", prevTotal, profile.blocks[len(profile.blocks)-1].cumulativeTokens)
	}
	// 截图和 signature 不能按 base64 文本计：30 轮里每轮都有，按文本计会远超 100 万。
	if prevTotal > 200000 {
		t.Fatalf("estimate inflated by base64 payloads: %d", prevTotal)
	}
	first := svc.buildKiroCacheEmulationUsage(account, nil, prevBody, "claude-opus-5-5", prevTotal)
	if first == nil || first.CacheCreationInputTokens != prevTotal || first.InputTokens != 0 {
		t.Fatalf("first turn should be all cache creation: total=%d usage=%+v", prevTotal, first)
	}

	nextBody := kiroClaudeCodeLikeBody(t, 31)
	nextTotal := estimateKiroInputTokens(nextBody)
	second := svc.buildKiroCacheEmulationUsage(account, nil, nextBody, "claude-opus-5-5", nextTotal)
	if second == nil {
		t.Fatal("second turn should hit the cache")
	}
	if second.InputTokens+second.CacheReadInputTokens+second.CacheCreationInputTokens != nextTotal {
		t.Fatalf("usage parts must add up to total %d: %+v", nextTotal, second)
	}
	// 上一轮的完整前缀（除了最后那条被替换的 user 消息）都应命中；新增部分记为 creation，普通 input 为 0。
	if second.CacheReadInputTokens < prevTotal*95/100 || second.InputTokens != 0 {
		t.Fatalf("expected ~%d cache read, got %+v (total %d)", prevTotal, second, nextTotal)
	}
}

func TestEstimateKiroImageTokens(t *testing.T) {
	var small bytes.Buffer
	if err := pngEncodeBlank(&small, 200, 150); err != nil {
		t.Fatal(err)
	}
	block := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(small.Bytes())}}
	if got := estimateKiroImageTokens(block); got != 40 {
		t.Fatalf("200x150 image tokens = %d, want 40", got)
	}
	if got := estimateKiroImageTokens(map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/a.png"}}); got != kiroTokensPerImageMax {
		t.Fatalf("undecodable image should use the cap, got %d", got)
	}
}

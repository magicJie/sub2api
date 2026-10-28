package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newOllamaCloudAnthropicAuthAccount constructs an Ollama API key account.
func newOllamaCloudAnthropicAuthAccount(baseURL string, extra map[string]any) *Account {
	if extra == nil {
		extra = map[string]any{}
	}
	return &Account{
		ID:          901,
		Name:        "ollama-cloud-anthropic-auth",
		Platform:    PlatformOllama,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "ollama-cloud-key",
			"base_url": baseURL,
		},
		Extra:       extra,
		Status:      StatusActive,
		Schedulable: true,
	}
}

func TestSetAnthropicAPIKeyAuthHeader_OllamaCloudForcesBearer(t *testing.T) {
	for _, extra := range []map[string]any{nil, {anthropicAPIKeyAuthSchemeExtraKey: AnthropicAPIKeyAuthSchemeXAPIKey}} {
		account := newOllamaCloudAnthropicAuthAccount("", extra)
		header := http.Header{}
		setAnthropicAPIKeyAuthHeader(header, account, "ollama-cloud-key")
		require.Equal(t, "Bearer ollama-cloud-key", header.Get("Authorization"))
		require.Empty(t, header.Get("x-api-key"))
	}
}

// Other platforms keep their configured scheme regardless of endpoint hostname.
func TestSetAnthropicAPIKeyAuthHeader_CNAdaptiveBaseURL(t *testing.T) {
	adaptiveOllamaAnthropic := &Account{
		ID:       902,
		Name:     "adaptive-ollama-anthropic",
		Platform: PlatformKimi,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "kimi-key",
			"api_protocol": APIProtocolAdaptive,
			"api_base_urls": map[string]any{
				APIProtocolAnthropic:       "https://ollama.com",
				APIProtocolChatCompletions: "https://api.moonshot.cn",
			},
		},
	}
	anthropicBase := adaptiveOllamaAnthropic.GetCNProtocolBaseURL(APIProtocolAnthropic)
	require.Equal(t, "https://ollama.com", anthropicBase)
	header := http.Header{}
	setAnthropicAPIKeyAuthHeader(header, adaptiveOllamaAnthropic, "kimi-key")
	require.Empty(t, header.Get("Authorization"))
	require.Equal(t, "kimi-key", header.Get("x-api-key"))

	// 反向：CC 地址挂 ollama.com，anthropic 分协议地址非 ollama → 不误判，
	// 保持 x-api-key。
	adaptiveOllamaCC := &Account{
		ID:       903,
		Name:     "adaptive-ollama-cc",
		Platform: PlatformKimi,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "kimi-key",
			"api_protocol": APIProtocolAdaptive,
			"api_base_urls": map[string]any{
				APIProtocolAnthropic:       "https://api.moonshot.cn/anthropic",
				APIProtocolChatCompletions: "https://ollama.com/v1",
			},
		},
	}
	anthropicBase = adaptiveOllamaCC.GetCNProtocolBaseURL(APIProtocolAnthropic)
	require.Equal(t, "https://api.moonshot.cn/anthropic", anthropicBase)
	header = http.Header{}
	setAnthropicAPIKeyAuthHeader(header, adaptiveOllamaCC, "kimi-key")
	require.Empty(t, header.Get("Authorization"))
	require.Equal(t, "kimi-key", header.Get("x-api-key"))

	// A Kimi Anthropic-protocol account retains its default authentication.
	anthropicProtocolOllama := &Account{
		ID:       904,
		Name:     "anthropic-protocol-ollama",
		Platform: PlatformKimi,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "kimi-key",
			"api_protocol": APIProtocolAnthropic,
			"base_url":     "https://ollama.com",
		},
	}
	anthropicBase = anthropicProtocolOllama.GetAnthropicProtocolBaseURL()
	require.Equal(t, "https://ollama.com", anthropicBase)
	header = http.Header{}
	setAnthropicAPIKeyAuthHeader(header, anthropicProtocolOllama, "kimi-key")
	require.Empty(t, header.Get("Authorization"))
	require.Equal(t, "kimi-key", header.Get("x-api-key"))

	// 同协议但 base 为 kimi 官方 → 保持 x-api-key。
	anthropicProtocolKimi := &Account{
		ID:       905,
		Name:     "anthropic-protocol-kimi",
		Platform: PlatformKimi,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "kimi-key",
			"api_protocol": APIProtocolAnthropic,
			"base_url":     "https://api.moonshot.cn/anthropic",
		},
	}
	header = http.Header{}
	setAnthropicAPIKeyAuthHeader(header, anthropicProtocolKimi, "kimi-key")
	require.Empty(t, header.Get("Authorization"))
	require.Equal(t, "kimi-key", header.Get("x-api-key"))
}

// TestGatewayService_AnthropicPassthrough_OllamaCloudBearer：经 passthrough
// builder 真实构造 http.Request 验证最终 header——Ollama Cloud 强制 Bearer 且
// 不泄漏客户端入站认证；非 Ollama 上游保持 x-api-key。
func TestGatewayService_AnthropicPassthrough_OllamaCloudBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	buildReq := func(t *testing.T, baseURL string) *http.Request {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		// 入站认证残留：正确上游只发一种认证头，不得泄漏客户端凭证
		c.Request.Header.Set("Authorization", "Bearer inbound-token")
		c.Request.Header.Set("X-Api-Key", "inbound-api-key")

		svc := &GatewayService{cfg: &config.Config{}}
		account := newOllamaCloudAnthropicAuthAccount(baseURL, nil)
		if baseURL == "https://api.anthropic.com" {
			account.Platform = PlatformAnthropic
		}
		body := []byte(`{"model":"claude-3-7-sonnet-20250219","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
		req, _, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "ollama-cloud-key")
		require.NoError(t, err)
		return req
	}

	ollamaReq := buildReq(t, "https://ollama.com/")
	require.Equal(t, "Bearer ollama-cloud-key", ollamaReq.Header.Get("Authorization"))
	require.Empty(t, ollamaReq.Header.Get("x-api-key"), "Ollama Cloud 上游不得再发 x-api-key")
	require.NotContains(t, ollamaReq.Header.Get("Authorization"), "inbound-token")
	require.Empty(t, ollamaReq.Header.Get("x-inbound"), "sanity")

	anthropicReq := buildReq(t, "https://api.anthropic.com")
	require.Empty(t, anthropicReq.Header.Get("Authorization"))
	require.Equal(t, "ollama-cloud-key", anthropicReq.Header.Get("x-api-key"))
	require.NotContains(t, anthropicReq.Header.Get("x-api-key"), "inbound-api-key")
}

// TestGatewayService_BuildUpstreamRequest_OllamaCloudBearer：经 Anthropic 原生
// GetBaseURL builder 真实构造 http.Request 验证最终 header。
func TestGatewayService_BuildUpstreamRequest_OllamaCloudBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	buildReq := func(t *testing.T, baseURL string) *http.Request {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		c.Request.Header.Set("Authorization", "Bearer inbound-token")
		c.Request.Header.Set("X-Api-Key", "inbound-api-key")

		svc := &GatewayService{cfg: &config.Config{}}
		account := newOllamaCloudAnthropicAuthAccount(baseURL, nil)
		if baseURL == "https://api.anthropic.com" {
			account.Platform = PlatformAnthropic
		}
		body := []byte(`{"model":"claude-3-7-sonnet-20250219","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
		req, _, err := svc.buildUpstreamRequest(context.Background(), c, account, body, "ollama-cloud-key", "apikey", "claude-3-7-sonnet-20250219", false, false)
		require.NoError(t, err)
		return req
	}

	ollamaReq := buildReq(t, "https://ollama.com")
	require.Equal(t, "https://ollama.com/v1/messages?beta=true", ollamaReq.URL.String())
	require.Equal(t, "Bearer ollama-cloud-key", ollamaReq.Header.Get("Authorization"))
	require.Empty(t, ollamaReq.Header.Get("x-api-key"))

	anthropicReq := buildReq(t, "https://api.anthropic.com")
	require.Empty(t, anthropicReq.Header.Get("Authorization"))
	require.Equal(t, "ollama-cloud-key", anthropicReq.Header.Get("x-api-key"))
}

// A native Anthropic bridge uses the account platform's authentication scheme.
func TestOpenAIGatewayService_NativeAnthropicBridge_OllamaCloudBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := &Account{
		ID:       906,
		Name:     "anthropic-protocol-ollama-bridge",
		Platform: PlatformKimi,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "ollama-cloud-key",
			"api_protocol": APIProtocolAnthropic,
			"base_url":     "https://ollama.com",
		},
	}
	targetURL, err := svc.nativeAnthropicTargetURL(account)
	require.NoError(t, err)
	require.Equal(t, "https://ollama.com/v1/messages", targetURL)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"kimi-k2-thinking","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	req, _, err := svc.buildNativeAnthropicUpstreamRequest(context.Background(), c, account, body, "ollama-cloud-key", targetURL)
	require.NoError(t, err)
	require.Empty(t, req.Header.Get("Authorization"))
	require.Equal(t, "ollama-cloud-key", req.Header.Get("x-api-key"))

	// 非 Ollama 上游：保持 x-api-key，无 Authorization。
	account.Credentials["base_url"] = "https://api.moonshot.cn/anthropic"
	targetURL, err = svc.nativeAnthropicTargetURL(account)
	require.NoError(t, err)
	req, _, err = svc.buildNativeAnthropicUpstreamRequest(context.Background(), c, account, body, "ollama-cloud-key", targetURL)
	require.NoError(t, err)
	require.Empty(t, req.Header.Get("Authorization"))
	require.Equal(t, "ollama-cloud-key", req.Header.Get("x-api-key"))
}

//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type ollamaPlatformHTTPUpstream struct{ client *http.Client }

func (u ollamaPlatformHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(req)
}

func (u ollamaPlatformHTTPUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

// Exercise real HTTP conversion, authentication and response decoding for each
// public protocol. DNS/dialing alone is redirected to the isolated upstream.
func TestOllamaPlatformThreeIngressesUseChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, legacyExtra := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
				t.Run(fmt.Sprintf("%s/stream=%t/legacy=%t", endpoint, stream, legacyExtra), func(t *testing.T) {
					captured := make(chan []byte, 1)
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						assert.Equal(t, "ollama.com", r.Host)
						assert.Equal(t, "/v1/chat/completions", r.URL.Path)
						assert.Equal(t, "Bearer ollama-test-key", r.Header.Get("Authorization"))
						assert.Empty(t, r.Header.Get("X-Api-Key"))
						outbound, err := io.ReadAll(r.Body)
						assert.NoError(t, err)
						captured <- outbound
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"model\":\"deepseek-v4.1-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\",\"reasoning\":\"think\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5,\"total_tokens\":8}}\n\ndata: [DONE]\n\n")
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, `{"id":"chatcmpl_test","model":"deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","content":"OK","reasoning":"think"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)
						}
					}))
					defer server.Close()
					client := server.Client()
					transport := client.Transport.(*http.Transport)
					transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
					transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
					}
					account := &Account{ID: 58, Platform: PlatformOllama, Type: AccountTypeAPIKey,
						Credentials: map[string]any{"api_key": "ollama-test-key", "base_url": "https://ollama.com/v1", "model_mapping": map[string]any{"public-model": "deepseek-v4.1-flash"}},
					}
					if legacyExtra {
						account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true, openai_compat.ExtraKeyResponsesMode: "force_chat_completions"}
					}
					var body []byte
					switch endpoint {
					case "/v1/responses":
						body = []byte(fmt.Sprintf(`{"model":"public-model","input":"hello","max_output_tokens":100000,"stream":%t,"tools":[{"type":"custom","name":"exec_command","format":{"type":"text"}}]}`, stream))
					case "/v1/messages":
						body = []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"max_tokens":100000,"stream":%t}`, stream))
					default:
						body = []byte(fmt.Sprintf(`{"model":"public-model","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"prior","reasoning_content":"history"},{"role":"user","content":"next"}],"max_tokens":100000,"stream":%t}`, stream))
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
					c.Request.Header.Set("User-Agent", "codex_cli_rs/0.55.0")
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: ollamaPlatformHTTPUpstream{client}}
					var result *OpenAIForwardResult
					var err error
					switch endpoint {
					case "/v1/responses":
						result, err = svc.Forward(context.Background(), c, account, body)
					case "/v1/messages":
						group := &Group{Platform: PlatformOllama, AllowMessagesDispatch: true, MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{SonnetMappedModel: "deepseek-v4.1-flash"}}
						result, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", group.ResolveMessagesDispatchModel("claude-sonnet-4-5"))
					default:
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
					}
					require.NoError(t, err)
					outbound := <-captured
					require.Equal(t, http.StatusOK, recorder.Code)
					require.Contains(t, recorder.Body.String(), "OK")
					require.Equal(t, "deepseek-v4.1-flash", gjson.GetBytes(outbound, "model").String())
					require.Equal(t, 3, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens)
					require.Equal(t, "deepseek-v4.1-flash", result.BillingModel)
					cap := gjson.GetBytes(outbound, "max_tokens")
					if !cap.Exists() {
						cap = gjson.GetBytes(outbound, "max_completion_tokens")
					}
					require.Equal(t, int64(65535), cap.Int())
					if endpoint == "/v1/responses" {
						require.Equal(t, "function", gjson.GetBytes(outbound, "tools.0.type").String())
						require.Equal(t, "exec_command", gjson.GetBytes(outbound, "tools.0.function.name").String())
					}
					if endpoint == "/v1/chat/completions" {
						require.Equal(t, "history", gjson.GetBytes(outbound, "messages.1.reasoning").String())
						require.Contains(t, recorder.Body.String(), "reasoning_content")
					}
				})
			}
		}
	}
}

func TestOllamaPlatformRoutingOverrides(t *testing.T) {
	account := &Account{Platform: PlatformOllama, Type: AccountTypeAPIKey, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	for _, mode := range []string{"", "auto", "force_chat_completions", "force_responses"} {
		account.Extra[openai_compat.ExtraKeyResponsesMode] = mode
		want := APIProtocolChatCompletions
		if mode == "force_responses" {
			want = APIProtocolResponses
		}
		require.Equal(t, want == APIProtocolResponses, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
		for _, inbound := range []string{APIProtocolResponses, APIProtocolAnthropic, APIProtocolChatCompletions} {
			require.Equal(t, want, resolveUpstreamProtocol(account, inbound, "", nil))
		}
	}
	legacy := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://ollama.com/v1"}}
	body := []byte(`{"model":"deepseek-v4.1-flash","max_tokens":100000}`)
	require.Equal(t, body, clampOllamaCloudUpstreamMaxTokens(legacy, body))
}

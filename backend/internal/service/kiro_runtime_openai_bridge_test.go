//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// buildKiroBridgeEventStreamFrame constructs a single AWS event-stream framed
// message in the same binary shape Kiro's upstream uses, so tests can feed a
// canned response through httpUpstreamRecorder without touching the network.
// This mirrors internal/kiro/translator_test.go's buildEventStreamFrame (kept
// as a separate copy here because it is unexported in another package).
func TestKiroClaude55RejectsForcedToolsAcrossProtocols(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5-5", "claude-opus-5-5"} {
		for _, protocol := range []string{"messages", "responses", "chat_completions"} {
			t.Run(model+"/"+protocol, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				ginContext, _ := gin.CreateTestContext(recorder)
				ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				schema := map[string]any{"type": "object", "properties": map[string]any{}}
				request := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hello"}}}
				switch protocol {
				case "messages":
					request["tool_choice"] = map[string]string{"type": "any"}
					request["tools"] = []map[string]any{{"name": "lookup", "input_schema": schema}}
				case "responses":
					request["input"] = "hello"
					request["tool_choice"] = "required"
					request["tools"] = []map[string]any{{"type": "function", "name": "lookup", "parameters": schema}}
				case "chat_completions":
					request["tool_choice"] = "required"
					request["tools"] = []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "parameters": schema}}}
				}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				svc := &GatewayService{}
				account := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth}
				switch protocol {
				case "messages":
					parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
					require.NoError(t, parseErr)
					_, err = svc.forwardKiroMessages(t.Context(), ginContext, account, parsed, time.Now())
				case "responses":
					_, err = svc.forwardKiroAsResponses(t.Context(), ginContext, account, body, nil, time.Now())
				case "chat_completions":
					_, err = svc.forwardKiroAsChatCompletions(t.Context(), ginContext, account, body, nil, time.Now())
				}
				require.Error(t, err)
				require.Contains(t, err.Error(), "forced tool_choice")
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				errorField := "error.type"
				if protocol == "responses" {
					errorField = "error.code"
				}
				require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), errorField).String())
			})
		}
	}
}

func buildKiroBridgeEventStreamFrame(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	headers := bytes.NewBuffer(nil)
	_ = headers.WriteByte(byte(len(":event-type")))
	_, _ = headers.WriteString(":event-type")
	_ = headers.WriteByte(7)
	require.NoError(t, binary.Write(headers, binary.BigEndian, uint16(len(eventType))))
	_, _ = headers.WriteString(eventType)

	totalLength := uint32(12 + headers.Len() + len(payloadBytes) + 4)
	frame := bytes.NewBuffer(nil)
	require.NoError(t, binary.Write(frame, binary.BigEndian, totalLength))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payloadBytes)
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	return frame.Bytes()
}

func kiroBridgeTestAccount() *Account {
	return &Account{
		ID:       9001,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "kiro-test-access-token",
			"auth_method":  "social",
			"provider":     "Github",
		},
	}
}

func kiroBridgeSuccessEventStream(t *testing.T) []byte {
	t.Helper()
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildKiroBridgeEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "Hello from Kiro GPT"},
	}))
	_, _ = stream.Write(buildKiroBridgeEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"outputTokens":        5,
				"totalTokens":         15,
			},
		},
	}))
	_, _ = stream.Write(buildKiroBridgeEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	return stream.Bytes()
}

func TestOpenKiroAnthropicStreamResponseReplaysHistoryForNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildKiroBridgeEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"conversationId":      "kiro-server-conversation",
			"agentContinuationId": "kiro-server-continuation",
		},
	}))
	_, _ = stream.Write(buildKiroBridgeEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "I will continue the task."},
	}))
	_, _ = stream.Write(buildKiroBridgeEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}}
	continuationStore := &memoryKiroContinuationStore{}
	svc := &GatewayService{
		httpUpstream:          upstream,
		kiroCooldownStore:     &kiroUsageCooldownStore{},
		kiroContinuationStore: continuationStore,
	}
	account := kiroBridgeTestAccount()
	body := []byte(`{"model":"claude-opus-5","metadata":{"user_id":"{\"session_id\":\"desktop-session-bridge\",\"device_id\":\"device-bridge\"}"},"messages":[{"role":"user","content":"inspect the project"}],"stream":true}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
	require.NoError(t, err)
	groupID := int64(3)
	parsed.GroupID = &groupID
	parsed.SessionContext = &SessionContext{APIKeyID: 77, ClientIP: "10.0.0.1", UserAgent: "Claude/1.0"}

	resp, _, err := svc.openKiroAnthropicStreamResponse(context.Background(), account, parsed, body, "claude-opus-5", "claude-opus-5", nil, nil)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	nextPayload, err := svc.buildKiroPayloadForAccount(context.Background(), account, parsed, body, "claude-opus-5", "token", "claude-opus-5", nil)
	require.NoError(t, err)
	require.NotEqual(t, "kiro-server-conversation", gjson.GetBytes(nextPayload.Payload, "conversationState.conversationId").String())
	require.False(t, gjson.GetBytes(nextPayload.Payload, "conversationState.agentContinuationId").Exists())
	require.NotEmpty(t, gjson.GetBytes(nextPayload.Payload, "conversationState.history").Array())
}

// TestForwardKiroAsResponses_StreamingSuccess verifies that a Kiro account can
// serve an OpenAI Responses API client end-to-end: request converted to
// Anthropic format, forwarded through Kiro's existing upstream machinery, and
// the (synthetic) Anthropic SSE response converted back to Responses format.
func TestForwardKiroAsResponses_StreamingSuccess(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(kiroBridgeSuccessEventStream(t))),
	}}
	svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &kiroUsageCooldownStore{}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "responses")
	require.NoError(t, err)

	result, err := svc.forwardKiroAsResponses(context.Background(), c, kiroBridgeTestAccount(), body, parsed, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)

	require.Contains(t, rec.Body.String(), "Hello from Kiro GPT")
	require.Contains(t, rec.Body.String(), "response.completed")

	require.NotNil(t, upstream.lastReq)
	require.Contains(t, upstream.lastReq.URL.Path, "generateAssistantResponse")
}

// TestForwardKiroAsChatCompletions_StreamingSuccess mirrors the Responses test
// above but for the Chat Completions API entry point.
func TestForwardKiroAsChatCompletions_StreamingSuccess(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(kiroBridgeSuccessEventStream(t))),
	}}
	svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &kiroUsageCooldownStore{}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "chat_completions")
	require.NoError(t, err)

	result, err := svc.forwardKiroAsChatCompletions(context.Background(), c, kiroBridgeTestAccount(), body, parsed, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)

	require.Contains(t, rec.Body.String(), "Hello from Kiro GPT")
	require.Contains(t, rec.Body.String(), `"object":"chat.completion.chunk"`)

	require.NotNil(t, upstream.lastReq)
	require.Contains(t, upstream.lastReq.URL.Path, "generateAssistantResponse")
}

func TestKiroBridgeMapsAliasBeforeReasoningConversion(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
				Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(kiroBridgeSuccessEventStream(t)))}}
			svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &kiroUsageCooldownStore{}}
			account := kiroBridgeTestAccount()
			account.Credentials["model_mapping"] = map[string]any{"my-alias": "claude-sonnet-5.5"}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
			body := []byte(`{"model":"my-alias","stream":true,"input":"hello","reasoning":{"effort":"high"}}`)
			var result *ForwardResult
			var err error
			accepted := 0
			parsed := &ParsedRequest{OnUpstreamAccepted: func() {
				accepted++
				require.Empty(t, rec.Body.String())
			}}
			if protocol == "responses" {
				result, err = svc.forwardKiroAsResponses(t.Context(), c, account, body, parsed, time.Now())
			} else {
				body = []byte(`{"model":"my-alias","stream":true,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`)
				result, err = svc.forwardKiroAsChatCompletions(t.Context(), c, account, body, parsed, time.Now())
			}
			require.NoError(t, err)
			require.Equal(t, 1, accepted)
			require.Equal(t, "my-alias", result.Model)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), `"model":"my-alias"`)
		})
	}
}

func TestPrepareKiroPayloadPreservesExplicitThinking(t *testing.T) {
	for _, thinking := range []string{`{"type":"disabled"}`, `{"type":"enabled","budget_tokens":2048}`, `{"type":"adaptive"}`} {
		body := []byte(`{"model":"claude-sonnet-5.5","thinking":` + thinking + `,"messages":[]}`)
		require.Equal(t, body, prepareKiroPayloadBodyForRequestModel(body, "claude-sonnet-5.5-thinking"))
	}
}

// TestForwardKiroAsResponses_UpstreamErrorWritesResponsesShapedError verifies
// that a non-failover Kiro upstream HTTP error is rendered using the Responses
// API error shape (not Kiro/Anthropic's own {"type":"error",...} shape), since
// the client here is an OpenAI Responses API client.
func TestForwardKiroAsResponses_UpstreamErrorWritesResponsesShapedError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"message":"improperly formed request"}`))),
	}}
	svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &kiroUsageCooldownStore{}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "responses")
	require.NoError(t, err)

	_, err = svc.forwardKiroAsResponses(context.Background(), c, kiroBridgeTestAccount(), body, parsed, time.Now())
	require.Error(t, err)

	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "422 should not be treated as a failover-eligible error")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), `"error"`)
	require.NotContains(t, rec.Body.String(), `"type":"upstream_error"`, "response must use Responses API error shape, not Kiro/Anthropic shape")
}

func TestKiroUnsupportedDocumentReturns400BeforeTokenLookup(t *testing.T) {
	for _, protocol := range []string{"messages", "responses", "chat_completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				request := map[string]any{"model": "claude-sonnet-4.6", "stream": stream}
				switch protocol {
				case "messages":
					request["messages"] = []any{map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": "JVBERg=="}}}}}
				case "responses":
					request["input"] = []any{map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "input_file", "filename": "doc.pdf", "file_data": "data:application/pdf;base64,JVBERg=="}}}}
				case "chat_completions":
					request["messages"] = []any{map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "file", "file": map[string]any{"filename": "doc.pdf", "file_data": "data:application/pdf;base64,JVBERg=="}}}}}
				}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				svc := &GatewayService{}
				account := &Account{ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth}
				accepted := 0
				parsed := &ParsedRequest{}
				if protocol == "messages" {
					parsed, err = ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
					require.NoError(t, err)
				}
				parsed.OnUpstreamAccepted = func() { accepted++ }
				switch protocol {
				case "messages":
					_, err = svc.forwardKiroMessages(t.Context(), c, account, parsed, time.Now())
				case "responses":
					_, err = svc.forwardKiroAsResponses(t.Context(), c, account, body, parsed, time.Now())
				case "chat_completions":
					_, err = svc.forwardKiroAsChatCompletions(t.Context(), c, account, body, parsed, time.Now())
				}
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Contains(t, rec.Body.String(), "invalid_request_error")
				require.NotContains(t, rec.Body.String(), "access_token")
				require.Zero(t, accepted)
			})
		}
	}
}

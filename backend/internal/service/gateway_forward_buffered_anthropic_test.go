//go:build unit

package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBufferedAnthropicAdapters_CompletionAndFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	partial := "\n: keepalive\n\nevent:message_start\nid: msg_1\n: comment between fields\ndata: {\ndata: \"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":12,\"cache_read_input_tokens\":3}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial answer\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n"
	stopReason := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n"
	messageStop := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	upstreamError := `{"type":"error","error":{"type":"overloaded_error","message":"Upstream busy"}}`
	readErr := errors.New("upstream connection interrupted")
	adapters := []struct {
		name      string
		errorPath string
		run       func(*http.Response, *gin.Context) (*ForwardResult, error)
	}{
		{
			name:      "responses",
			errorPath: "error.code",
			run: func(resp *http.Response, c *gin.Context) (*ForwardResult, error) {
				return (&GatewayService{}).handleResponsesBufferedStreamingResponse(resp, c, "public-model", "claude", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			},
		},
		{
			name:      "chat_completions",
			errorPath: "error.type",
			run: func(resp *http.Response, c *gin.Context) (*ForwardResult, error) {
				return (&GatewayService{}).handleCCBufferedFromAnthropic(resp, c, "public-model", "claude", nil, time.Now())
			},
		},
	}
	cases := []struct {
		name       string
		payload    string
		readError  error
		status     int
		errorType  string
		ssePayload string
		usage      bool
	}{
		{name: "complete", payload: partial + stopReason + messageStop, status: http.StatusOK, usage: true},
		{name: "error_after_partial", payload: partial + "event:error\ndata: " + upstreamError + "\n\n", status: http.StatusServiceUnavailable, errorType: "overloaded_error", ssePayload: upstreamError, usage: true},
		{name: "typed_error_without_event_field", payload: partial + "data: " + upstreamError + "\n\n", status: http.StatusServiceUnavailable, errorType: "overloaded_error", ssePayload: upstreamError, usage: true},
		{name: "error_before_message", payload: "event:error\ndata: " + upstreamError + "\n\n", status: http.StatusServiceUnavailable, errorType: "overloaded_error", ssePayload: upstreamError},
		{name: "reader_close_with_error", payload: partial, readError: readErr, status: http.StatusBadGateway, errorType: "server_error", usage: true},
		{name: "eof_without_terminal", payload: partial, status: http.StatusBadGateway, errorType: "server_error", usage: true},
		{name: "stop_reason_without_message_stop", payload: partial + stopReason, status: http.StatusBadGateway, errorType: "server_error", usage: true},
		{name: "message_stop_without_stop_reason", payload: partial + messageStop, status: http.StatusBadGateway, errorType: "server_error", usage: true},
		{name: "empty_stream", status: http.StatusBadGateway, errorType: "server_error"},
	}
	for _, adapter := range adapters {
		t.Run(adapter.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
					var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.payload))
					if tc.readError != nil {
						reader, writer := io.Pipe()
						body = reader
						go func() {
							_, _ = io.WriteString(writer, tc.payload)
							_ = writer.CloseWithError(tc.readError)
						}()
					}
					defer body.Close()
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"rid-buffered"}}, Body: body}
					result, err := adapter.run(resp, c)
					require.Equal(t, tc.status, rec.Code)
					if tc.status == http.StatusOK {
						require.NoError(t, err)
						require.False(t, gjson.GetBytes(rec.Body.Bytes(), "error").Exists())
						require.Contains(t, rec.Body.String(), "partial answer")
						if adapter.name == "responses" {
							require.Equal(t, "completed", gjson.GetBytes(rec.Body.Bytes(), "status").String())
						} else {
							require.Equal(t, "stop", gjson.GetBytes(rec.Body.Bytes(), "choices.0.finish_reason").String())
						}
					} else {
						require.Error(t, err)
						require.Equal(t, tc.errorType, gjson.GetBytes(rec.Body.Bytes(), adapter.errorPath).String())
						require.NotEmpty(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
						require.NotContains(t, rec.Body.String(), "partial answer")
						var failover *UpstreamFailoverError
						require.False(t, errors.As(err, &failover), "committed errors must not cause replay")
						if tc.ssePayload != "" {
							var streamErr *sseStreamErrorEventError
							require.ErrorAs(t, err, &streamErr)
							require.Equal(t, tc.ssePayload, streamErr.RawData)
							require.Equal(t, "Upstream busy", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
						} else if tc.readError != nil {
							require.ErrorIs(t, err, tc.readError)
						} else {
							require.ErrorIs(t, err, io.ErrUnexpectedEOF)
						}
					}
					if tc.usage {
						require.NotNil(t, result)
						require.Equal(t, "rid-buffered", result.RequestID)
						require.Equal(t, 12, result.Usage.InputTokens)
						require.Equal(t, 3, result.Usage.CacheReadInputTokens)
						require.GreaterOrEqual(t, result.Usage.OutputTokens, 5)
						require.False(t, result.Stream)
					} else {
						require.Nil(t, result)
					}
				})
			}
		})
	}
}

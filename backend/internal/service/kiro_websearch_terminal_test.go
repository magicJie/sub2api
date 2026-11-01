//go:build unit

package service

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestKiroWebSearchFinalRoundRetainsTerminalUsageAndThinking(t *testing.T) {
	var body bytes.Buffer
	body.Write(buildKiroBridgeEventStreamFrame(t, "reasoningContentEvent", map[string]any{"text": "reason"}))
	body.Write(buildKiroBridgeEventStreamFrame(t, "assistantResponseEvent", map[string]any{"content": "answer"}))
	body.Write(buildKiroBridgeEventStreamFrame(t, "metadataEvent", map[string]any{"inputTokens": 12, "outputTokens": 7}))
	chunks, _, err := bufferKiroAnthropicStream(t.Context(), &body, "claude-sonnet-4.5", 50, kiropkg.KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)
	var output bytes.Buffer
	for _, chunk := range chunks {
		adjusted, forward := kiropkg.AdjustSSEChunk(chunk, 2)
		if forward {
			output.Write(adjusted)
		}
	}
	var terminal []string
	var thinking string
	var inputTokens, outputTokens int64
	for _, line := range strings.Split(output.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		event := gjson.Parse(payload)
		switch event.Get("type").String() {
		case "message_start":
			t.Fatal("inner response must not start a second client message")
		case "message_delta":
			terminal = append(terminal, "message_delta")
			inputTokens = event.Get("usage.input_tokens").Int()
			outputTokens = event.Get("usage.output_tokens").Int()
		case "message_stop":
			terminal = append(terminal, "message_stop")
		case "content_block_delta":
			thinking += event.Get("delta.thinking").String()
		}
	}
	require.Equal(t, []string{"message_delta", "message_stop"}, terminal)
	require.Equal(t, int64(12), inputTokens)
	require.Equal(t, int64(7), outputTokens)
	require.Equal(t, "reason", thinking)
}

func TestKiroWebSearchInterruptedRoundRetainsTypedErrorAndUsage(t *testing.T) {
	var body bytes.Buffer
	body.Write(buildKiroBridgeEventStreamFrame(t, "assistantResponseEvent", map[string]any{"content": "partial"}))
	body.Write(buildKiroBridgeEventStreamFrame(t, "metadataEvent", map[string]any{"inputTokens": 12, "outputTokens": 7}))
	body.Write(buildKiroBridgeEventStreamFrame(t, "throttlingException", map[string]any{"message": "Too many requests"}))
	chunks, result, err := bufferKiroAnthropicStream(t.Context(), &body, "claude-sonnet-4.5", 50, kiropkg.KiroRequestContext{})
	var streamErr *kiropkg.StreamError
	require.ErrorAs(t, err, &streamErr)
	require.Equal(t, http.StatusTooManyRequests, streamErr.StatusCode)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	var output bytes.Buffer
	require.NoError(t, writeAdjustedKiroSSEChunks(&output, chunks, 2))
	var errorType string
	var outputTokens int64
	for _, line := range strings.Split(output.String(), "\n") {
		if payload, ok := strings.CutPrefix(line, "data: "); ok {
			event := gjson.Parse(payload)
			switch event.Get("type").String() {
			case "error":
				errorType = event.Get("error.type").String()
			case "message_delta":
				outputTokens = event.Get("usage.output_tokens").Int()
			case "message_stop":
				t.Fatal("an interrupted search round must not finish successfully")
			}
		}
	}
	require.Equal(t, "rate_limit_error", errorType)
	require.Equal(t, int64(7), outputTokens)
}

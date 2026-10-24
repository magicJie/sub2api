package kiro

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildExceptionFrame 构造 :message-type=exception 的 eventstream 帧。
func buildExceptionFrame(t *testing.T, exceptionType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	headers := bytes.NewBuffer(nil)
	for _, kv := range [][2]string{{":message-type", "exception"}, {":exception-type", exceptionType}} {
		_ = headers.WriteByte(byte(len(kv[0])))
		_, _ = headers.WriteString(kv[0])
		_ = headers.WriteByte(7)
		require.NoError(t, binary.Write(headers, binary.BigEndian, uint16(len(kv[1]))))
		_, _ = headers.WriteString(kv[1])
	}
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

func TestKiroStreamExceptionBeforeOutputReturnsTypedError(t *testing.T) {
	stream := bytes.NewBuffer(buildExceptionFrame(t, "ThrottlingException", map[string]any{"message": "Too many requests", "reason": "CONCURRENCY"}))
	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 10, KiroRequestContext{})
	streamErr, ok := AsStreamError(err)
	require.True(t, ok, "err=%v", err)
	require.Equal(t, http.StatusTooManyRequests, streamErr.StatusCode)
	require.Equal(t, "ThrottlingException", streamErr.ExceptionType)
	require.Contains(t, streamErr.Message, "Too many requests (CONCURRENCY)")
	require.Zero(t, out.Len(), "未输出任何内容，调用方可以按 HTTP 错误处理并切换账号")
}

func TestKiroStreamExceptionAfterOutputSendsSSEError(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "partial "}}))
	_, _ = stream.Write(buildExceptionFrame(t, "InternalServerException", map[string]any{"message": "boom"}))
	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 10, KiroRequestContext{})
	_, ok := AsStreamError(err)
	require.True(t, ok)
	output := out.String()
	require.Contains(t, output, "partial ")
	require.Contains(t, output, "event: error")
	require.Contains(t, output, `"type":"api_error"`)
	require.NotContains(t, output, "message_stop", "不能伪装成正常结束")
}

func TestKiroNonStreamingExceptionReturnsTypedError(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "partial"}}))
	_, _ = stream.Write(buildExceptionFrame(t, "ValidationException", map[string]any{"message": "Input is too long", "reason": "CONTENT_LENGTH_EXCEEDS_THRESHOLD"}))
	_, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	streamErr, ok := AsStreamError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, streamErr.StatusCode)
	require.True(t, strings.Contains(streamErr.Message, "CONTENT_LENGTH_EXCEEDS_THRESHOLD"))
}

func TestKiroExceptionEventTypeWithoutMessageType(t *testing.T) {
	stream := bytes.NewBuffer(buildEventStreamFrame(t, "throttlingException", map[string]any{"message": "slow down"}))
	_, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	streamErr, ok := AsStreamError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, streamErr.StatusCode)
}

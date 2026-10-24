package kiro

// 流中途的异常帧。
//
// Kiro 的 eventstream 在出错时会发 :message-type=exception（或 error）的帧，
// :exception-type 给出类型（ThrottlingException、ValidationException 等），payload 是
// {"message": "...", "reason": "..."}。以前这些帧被当成普通事件吞掉，客户端看到的是
// 截断但“正常结束”的回答。现在读帧时返回 *StreamError，由调用方决定：
// 还没向客户端输出内容就按 HTTP 错误处理（可切换账号重试），已经输出则发 SSE error。
// 参考 magpie-community/plugins packages/kiro（MIT）。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// StreamError 是 Kiro 流中途返回的异常。
type StreamError struct {
	ExceptionType string // 上游的 :exception-type，如 ThrottlingException
	Message       string // payload 里的 message（附带 reason）
	StatusCode    int    // 等价的 HTTP 状态码，用于错误分类与 failover
}

func (e *StreamError) Error() string {
	if e.ExceptionType == "" {
		return "kiro stream exception: " + e.Message
	}
	return "kiro stream exception " + e.ExceptionType + ": " + e.Message
}

// AsStreamError 从错误链中取出 *StreamError。
func AsStreamError(err error) (*StreamError, bool) {
	var streamErr *StreamError
	if errors.As(err, &streamErr) && streamErr != nil {
		return streamErr, true
	}
	return nil, false
}

// kiroExceptionEventTypes 是以 :event-type 形式出现的异常事件（部分版本不带 :message-type）。
var kiroExceptionEventTypes = map[string]bool{
	"error":                       true,
	"exception":                   true,
	"throttlingException":         true,
	"throttlingError":             true,
	"validationException":         true,
	"validationError":             true,
	"serviceUnavailableError":     true,
	"serviceUnavailableException": true,
	"internalServerException":     true,
	"accessDeniedException":       true,
	"invalidStateEvent":           true,
}

func isKiroExceptionEventType(eventType string) bool {
	return kiroExceptionEventTypes[eventType]
}

func newKiroStreamError(exceptionType string, payload []byte) *StreamError {
	message := strings.TrimSpace(string(payload))
	var body struct {
		Message      string `json:"message"`
		MessageUpper string `json:"Message"`
		Reason       string `json:"reason"`
	}
	if json.Unmarshal(payload, &body) == nil {
		if m := firstNonEmpty(body.Message, body.MessageUpper); m != "" {
			message = m
		}
		if body.Reason != "" && !strings.Contains(message, body.Reason) {
			message += " (" + body.Reason + ")"
		}
	}
	return &StreamError{
		ExceptionType: exceptionType,
		Message:       message,
		StatusCode:    kiroStreamExceptionStatus(exceptionType, message),
	}
}

// kiroStreamExceptionStatus 把异常类型映射到等价的 HTTP 状态码。
func kiroStreamExceptionStatus(exceptionType, message string) int {
	lowerType := strings.ToLower(exceptionType)
	lowerMessage := strings.ToLower(message)
	switch {
	case strings.Contains(lowerType, "throttl"):
		return http.StatusTooManyRequests
	case strings.Contains(lowerType, "accessdenied"):
		return http.StatusForbidden
	case strings.Contains(lowerType, "validation"), strings.Contains(lowerType, "invalidstate"):
		return http.StatusBadRequest
	case strings.Contains(lowerType, "serviceunavailable"), strings.Contains(lowerMessage, "insufficient_model_capacity"):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// anthropicStreamErrorEvent 构造 Anthropic SSE error 事件的 data。
func anthropicStreamErrorEvent(e *StreamError) map[string]any {
	return map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    anthropicErrorType(e.StatusCode),
			"message": e.Error(),
		},
	}
}

// anthropicErrorType 把 HTTP 状态码映射到 Anthropic 的错误类型。
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable, 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

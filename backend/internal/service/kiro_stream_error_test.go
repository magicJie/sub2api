//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandleKiroHTTPErrorPromptTooLongUsesAnthropicShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"message":"Input is too long for requested model.","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`,
		`{"message":"Improperly formed request","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`,
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		account := &Account{ID: 7, Platform: PlatformKiro, Type: AccountTypeOAuth, Name: "kiro"}
		repo := &recordingKiroTempUnschedRepo{}
		svc := &GatewayService{accountRepo: repo}

		err := svc.handleKiroHTTPError(context.Background(), newJSONResponse(http.StatusBadRequest, body), c, account, "claude-sonnet-4.6", []byte(`{}`))
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.NotErrorAs(t, err, &failoverErr, "超长换账号也没用，不应 failover")
		require.False(t, repo.called)
		require.False(t, repo.rateCalled)

		require.Equal(t, http.StatusBadRequest, rec.Code)
		var resp struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, "error", resp.Type)
		require.Equal(t, "invalid_request_error", resp.Error.Type)
		require.Contains(t, resp.Error.Message, "prompt is too long")
	}
}

func TestHandleKiroHTTPErrorModelCapacityFailsOverWithoutPenalty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	account := &Account{ID: 8, Platform: PlatformKiro, Type: AccountTypeOAuth, Name: "kiro"}
	repo := &recordingKiroTempUnschedRepo{}
	svc := &GatewayService{accountRepo: repo}

	resp := newJSONResponse(http.StatusInternalServerError, `{"message":"Insufficient model capacity","reason":"INSUFFICIENT_MODEL_CAPACITY"}`)
	err := svc.handleKiroHTTPError(context.Background(), resp, c, account, "claude-opus-4.6", []byte(`{}`))
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, 529, failoverErr.StatusCode)
	require.True(t, failoverErr.RequestScopedTransient)
	require.False(t, repo.called)
	require.False(t, repo.rateCalled)
}

func TestKiroStreamResponseOrExceptionConvertsEarlyException(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		_ = pw.CloseWithError(&kiropkg.StreamError{ExceptionType: "ThrottlingException", Message: "slow down", StatusCode: http.StatusTooManyRequests})
	}()
	resp := kiroStreamResponseOrException(pr, http.StatusOK, http.Header{"Content-Type": {"text/event-stream"}})
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "slow down")
}

func TestKiroStreamResponseOrExceptionPassesThroughNormalStream(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.WriteString(pw, "event: message_start\ndata: {}\n\n")
		_ = pw.Close()
	}()
	resp := kiroStreamResponseOrException(pr, http.StatusOK, http.Header{"Content-Type": {"text/event-stream"}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, "event: message_start\ndata: {}\n\n", string(body), "预读的字节不能丢")
}

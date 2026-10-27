//go:build unit

package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func kiroOpsEvents(t *testing.T, c *gin.Context) []*OpsUpstreamErrorEvent {
	t.Helper()
	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	return events
}

func TestForwardKiroMessagesMonthlyRequestCount402RecordsUpstreamDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const upstreamBody = `{"message":"You have reached the limit.","reason":"MONTHLY_REQUEST_COUNT"}`
	account := kiroBridgeTestAccount()
	repo := &recordingKiroTempUnschedRepo{}
	svc := &GatewayService{
		accountRepo:         repo,
		httpUpstream:        &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusPaymentRequired, upstreamBody)}},
		kiroCooldownStore:   &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	body, err := json.Marshal(map[string]any{
		"model":    "claude-sonnet-4-6",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	require.NoError(t, err)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
	require.NoError(t, err)

	_, err = svc.forwardKiroMessages(t.Context(), c, account, parsed, time.Now())
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusPaymentRequired, failoverErr.StatusCode)
	require.True(t, repo.rateCalled)

	events := kiroOpsEvents(t, c)
	require.Len(t, events, 1)
	require.Equal(t, http.StatusPaymentRequired, events[0].UpstreamStatusCode)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, upstreamBody, events[0].Detail)
	require.NotContains(t, events[0].Detail, "kiro-test-access-token")

	// 入库前的 sanitize 应保留 detail（ops_error_logs.upstream_error_detail 取最后一个事件的 Detail）。
	entry := &OpsInsertErrorLogInput{UpstreamErrors: events}
	require.NoError(t, sanitizeOpsUpstreamErrors(entry))
	require.Contains(t, string(*entry.UpstreamErrorsJSON), "MONTHLY_REQUEST_COUNT")
}

func TestClassifyKiroHTTPError429RecordsUpstreamDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const upstreamBody = `{"message":"Too many requests","reason":"THROTTLING"}`
	svc := &GatewayService{accountRepo: &recordingKiroTempUnschedRepo{}}
	resp := newJSONResponse(http.StatusTooManyRequests, upstreamBody)

	_, err := svc.classifyKiroHTTPErrorAndRecordOps(t.Context(), resp, c, kiroBridgeTestAccount(), "claude-sonnet-4.6", nil)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)

	events := kiroOpsEvents(t, c)
	require.Len(t, events, 1)
	require.Equal(t, http.StatusTooManyRequests, events[0].UpstreamStatusCode)
	require.Equal(t, upstreamBody, events[0].Detail)
}

func TestKiroUpstreamErrorDetailRespectsConfig(t *testing.T) {
	long := []byte(strings.Repeat("x", 5000))

	require.Len(t, (&GatewayService{}).kiroUpstreamErrorDetail(long), kiroUpstreamErrorDetailMaxBytes)
	require.Empty(t, (&GatewayService{}).kiroUpstreamErrorDetail([]byte("   ")))

	disabled := &GatewayService{cfg: &config.Config{}}
	require.Empty(t, disabled.kiroUpstreamErrorDetail([]byte(`{"reason":"MONTHLY_REQUEST_COUNT"}`)))

	limited := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{LogUpstreamErrorBody: true, LogUpstreamErrorBodyMaxBytes: 100}}}
	require.Len(t, limited.kiroUpstreamErrorDetail(long), 100)
}

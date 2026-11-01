//go:build unit

package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroForwardRefreshInProgressDoesNotPenalizeAccount(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "buffered"
		if stream {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			account := kiroBridgeTestAccount()
			account.Status = StatusActive
			account.Credentials["expires_at"] = time.Now().Add(time.Hour).Format(time.RFC3339)
			repo := &kiroRuntimeStateRepo{persisted: *account}
			cache := newOpenAITokenCacheStub()
			cache.lockAcquired = false
			require.NoError(t, cache.SetAccessToken(t.Context(), KiroTokenCacheKey(account), account.GetCredential("access_token"), time.Minute))
			provider := NewKiroTokenProvider(repo, cache, nil)
			provider.kiroOAuthService = &stubKiroAccountTokenRefresher{}
			svc := &GatewayService{
				accountRepo:       repo,
				httpUpstream:      &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusUnauthorized, `{"message":"token expired"}`)}},
				kiroTokenProvider: provider,
				kiroCooldownStore: &kiroUsageCooldownStore{},
				rateLimitService:  NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
			}
			body := []byte(`{"model":"claude-sonnet-4.6","messages":[{"role":"user","content":"hello"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
			require.NoError(t, err)
			parsed.Stream = stream
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			_, err = svc.Forward(t.Context(), c, account, parsed)
			require.Error(t, err)
			require.Equal(t, StatusActive, repo.persisted.Status)
			require.Nil(t, repo.persisted.TempUnschedulableUntil, "another worker refreshing credentials must not disable this account")
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.RequestScopedTransient)
			require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
			require.False(t, c.Writer.Written())
		})
	}
}

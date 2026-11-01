//go:build unit

package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	kirocooldown "github.com/Wei-Shaw/sub2api/internal/kiro/cooldown"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestKiroForwardModelCapacityPreservesAccountAvailability(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, firstFrame := range []bool{false, true} {
			name := "http429"
			if firstFrame {
				name = "first_frame_throttling"
			}
			if stream {
				name += "/stream"
			}
			t.Run(name, func(t *testing.T) {
				response := newJSONResponse(http.StatusTooManyRequests, `{"reason":"INSUFFICIENT_MODEL_CAPACITY"}`)
				if firstFrame {
					response = &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
						Body: io.NopCloser(bytes.NewReader(buildKiroBridgeEventStreamFrame(t, "throttlingException", map[string]any{
							"message": "INSUFFICIENT_MODEL_CAPACITY",
						})))}
				}
				mr := miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = rdb.Close() })
				store := kirocooldown.NewStore(rdb)
				account := kiroBridgeTestAccount()
				account.Status = StatusActive
				repo := &recordingKiroErrorRepo{}
				svc := &GatewayService{
					httpUpstream:      &queuedHTTPUpstream{responses: []*http.Response{response}},
					accountRepo:       repo,
					kiroCooldownStore: store,
					rateLimitService:  NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
				}
				body := []byte(`{"model":"claude-sonnet-4.6","messages":[{"role":"user","content":"hello"}],"stream":false}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
				require.NoError(t, err)
				parsed.Stream = stream
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				_, err = svc.Forward(t.Context(), c, account, parsed)
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Equal(t, 529, failover.StatusCode)
				require.True(t, failover.RequestScopedTransient)
				state, err := store.GetState(t.Context(), buildKiroAccountKey(account))
				require.NoError(t, err)
				if state != nil {
					require.False(t, state.Active)
				}
				require.Equal(t, StatusActive, account.Status)
				require.False(t, repo.called)
				require.False(t, repo.rateCalled)
				require.Empty(t, repo.errorMsg)
			})
		}
	}
}

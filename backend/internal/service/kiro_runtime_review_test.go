//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

type kiroRuntimeStateRepo struct {
	mockAccountRepoForGemini
	persisted Account
}

func (r *kiroRuntimeStateRepo) GetByID(context.Context, int64) (*Account, error) {
	snapshot := r.persisted
	return &snapshot, nil
}

func (r *kiroRuntimeStateRepo) SetError(_ context.Context, _ int64, message string) error {
	r.persisted.Status = StatusError
	r.persisted.ErrorMessage = message
	return nil
}

func (r *kiroRuntimeStateRepo) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, reason string) error {
	r.persisted.TempUnschedulableUntil = &until
	r.persisted.TempUnschedulableReason = reason
	return nil
}

func (r *kiroRuntimeStateRepo) SetRateLimited(_ context.Context, _ int64, until time.Time) error {
	r.persisted.RateLimitResetAt = &until
	return nil
}

func TestKiroForwardPermanentRefreshErrorOwnsAccountState(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, stream := range []bool{false, true} {
			name := http.StatusText(status)
			if stream {
				name += "/stream"
			}
			t.Run(name, func(t *testing.T) {
				account := kiroBridgeTestAccount()
				account.Status = StatusActive
				account.Credentials["refresh_token"] = "revoked-refresh"
				account.Credentials["expires_at"] = time.Now().Add(time.Hour).Format(time.RFC3339)
				repo := &kiroRuntimeStateRepo{persisted: *account}
				provider := NewKiroTokenProvider(repo, nil, nil)
				provider.kiroOAuthService = &stubKiroAccountTokenRefresher{err: errors.New("invalid_grant")}
				svc := &GatewayService{
					accountRepo: repo,
					httpUpstream: &queuedHTTPUpstream{responses: []*http.Response{
						newJSONResponse(status, `{"message":"token expired"}`),
					}},
					kiroTokenProvider: provider,
					kiroCooldownStore: &kiroUsageCooldownStore{},
					rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
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
				require.Equal(t, StatusError, account.Status)
				require.Equal(t, StatusError, repo.persisted.Status)
				require.NotEmpty(t, repo.persisted.ErrorMessage)
				require.Nil(t, repo.persisted.TempUnschedulableUntil)
				require.Empty(t, repo.persisted.TempUnschedulableReason)
				require.Nil(t, repo.persisted.RateLimitResetAt)
			})
		}
	}
}

func TestKiroForwardGenericForbiddenTemporarilyUnavailable(t *testing.T) {
	account := kiroBridgeTestAccount()
	account.Status = StatusActive
	repo := &kiroRuntimeStateRepo{persisted: *account}
	svc := &GatewayService{
		accountRepo: repo,
		httpUpstream: &queuedHTTPUpstream{responses: []*http.Response{
			newJSONResponse(http.StatusForbidden, `{"message":"permission denied"}`),
		}},
		kiroCooldownStore: &kiroUsageCooldownStore{},
		rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
	}
	body := []byte(`{"model":"claude-sonnet-4.6","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	_, err = svc.Forward(t.Context(), c, account, parsed)
	require.Error(t, err)
	require.Equal(t, StatusActive, repo.persisted.Status)
	require.Empty(t, repo.persisted.ErrorMessage)
	require.NotNil(t, repo.persisted.TempUnschedulableUntil)
	require.True(t, repo.persisted.TempUnschedulableUntil.After(time.Now()))
	require.True(t, repo.persisted.TempUnschedulableUntil.Before(time.Now().Add(11*time.Minute)))
	require.Nil(t, repo.persisted.RateLimitResetAt)
}

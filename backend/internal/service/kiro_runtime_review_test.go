//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
	kirocooldown "github.com/Wei-Shaw/sub2api/internal/kiro/cooldown"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

func TestKiroForwardAliasesKeepClientModelAndMappedUsage(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4.6", "gpt-5.6-sol"} {
		for _, nativeSearch := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/search=%t", model, nativeSearch), func(t *testing.T) {
				account := kiroBridgeTestAccount()
				account.Credentials["model_mapping"] = map[string]any{"client-alias": model}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(bytes.NewReader(kiroBridgeSuccessEventStream(t))),
				}}
				tools := ""
				if nativeSearch {
					tools = `,"tools":[{"type":"web_search_20250305","name":"web_search"}]`
					endpoint := kiropkg.BuildMcpEndpoint(kiroAPIRegion(account))
					previous, existed := kiroWebSearchDescCache.Load(endpoint)
					kiroWebSearchDescCache.Store(endpoint, "")
					t.Cleanup(func() {
						if existed {
							kiroWebSearchDescCache.Store(endpoint, previous)
						} else {
							kiroWebSearchDescCache.Delete(endpoint)
						}
					})
					upstream.responses = []*http.Response{newJSONResponse(http.StatusOK, `{"jsonrpc":"2.0","result":{"content":[]}}`)}
				}
				svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &kiroUsageCooldownStore{}}
				body := []byte(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}]` + tools + `}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				result, err := svc.Forward(t.Context(), c, account, parsed)
				require.NoError(t, err)
				require.Equal(t, "client-alias", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
				require.Equal(t, "client-alias", result.Model)
				require.Equal(t, normalizeModelNameForPricing(kiropkg.MapModel(model)), result.UpstreamModel)
				require.Equal(t, kiropkg.MapModel(model), gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
				require.Equal(t, 10, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.Equal(t, int64(result.Usage.InputTokens), gjson.GetBytes(recorder.Body.Bytes(), "usage.input_tokens").Int())
				require.Equal(t, int64(result.Usage.OutputTokens), gjson.GetBytes(recorder.Body.Bytes(), "usage.output_tokens").Int())
			})
		}
	}
}

func TestKiroBridgeAliasPreservesMappedMaxReasoningUsage(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				account := kiroBridgeTestAccount()
				account.Credentials["model_mapping"] = map[string]any{"client-alias": "gpt-5.6-sol"}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(bytes.NewReader(kiroBridgeSuccessEventStream(t))),
				}}
				svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &kiroUsageCooldownStore{}}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				var result *ForwardResult
				var err error
				if protocol == "responses" {
					body := []byte(fmt.Sprintf(`{"model":"client-alias","input":"hello","reasoning":{"effort":"max"},"stream":%t}`, stream))
					result, err = svc.forwardKiroAsResponses(t.Context(), c, account, body, &ParsedRequest{}, time.Now())
				} else {
					body := []byte(fmt.Sprintf(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max","stream":%t}`, stream))
					result, err = svc.forwardKiroAsChatCompletions(t.Context(), c, account, body, &ParsedRequest{}, time.Now())
				}
				require.NoError(t, err)
				require.Equal(t, "client-alias", result.Model)
				require.Equal(t, "gpt-5.6-sol", result.UpstreamModel)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "max", *result.ReasoningEffort)
				require.Equal(t, 10, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.Contains(t, recorder.Body.String(), `"model":"client-alias"`)
				require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
			})
		}
	}
}

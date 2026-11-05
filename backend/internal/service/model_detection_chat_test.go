package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type modelDetectionHTTPClient struct{ HTTPUpstream }

func (modelDetectionHTTPClient) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

func TestModelQualityStrictChatUpstreamGradesInsteadOfFailingTransport(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "text content must be a string", http.StatusBadRequest)
			return
		}
		if len(request.Messages) != 1 || request.Messages[0].Content == "" {
			http.Error(w, "missing prompt", http.StatusBadRequest)
			return
		}
		// A valid text-only Chat Completions upstream returns a usable, incorrect
		// answer. The result must be a quality finding, rather than an HTTP failure.
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"arithmetic\\\":\\\"wrong\\\",\\\"code\\\":\\\"wrong\\\",\\\"logic\\\":\\\"wrong\\\",\\\"instruction\\\":\\\"wrong\\\",\\\"data\\\":\\\"wrong\\\"}\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "test-key", "base_url": upstream.URL}, Extra: map[string]any{"openai_responses_supported": false}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &AccountTestService{accountRepo: &upstreamModelMetadataRepoStub{account: account}, httpUpstream: modelDetectionHTTPClient{}, cfg: cfg}
	result, err := svc.runModelDetection(context.Background(), &ScheduledTestPlan{AccountID: 1, ModelID: "gpt-5.5", TestType: "quality", QualityThreshold: 80})
	require.NoError(t, err)
	require.Equal(t, "warning", result.Status)
	require.Empty(t, result.ErrorMessage)
	require.Equal(t, "degraded", result.Diagnostics.Quality.Status)
	require.Equal(t, float64(0), result.Diagnostics.Quality.Score)
	require.Equal(t, 5, result.Diagnostics.Quality.Total)
}

//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const kiroListModelsFixture = `{"models":[
	{"modelId":"auto","supportedInputTypes":["TEXT","IMAGE"],"tokenLimits":{"maxInputTokens":1000000,"maxOutputTokens":64000}},
	{"modelId":"claude-opus-4.8","supportedInputTypes":["TEXT","IMAGE"],"tokenLimits":{"maxInputTokens":1000000,"maxOutputTokens":128000}},
	{"modelId":"claude-haiku-5.5","supportedInputTypes":["TEXT","IMAGE"],"tokenLimits":{"maxInputTokens":1000000,"maxOutputTokens":128000}},
	{"modelId":"gpt-5.6-sol","supportedInputTypes":["TEXT","IMAGE"],"tokenLimits":{"maxInputTokens":922000,"maxOutputTokens":128000}},
	{"modelId":"deepseek-3.2","supportedInputTypes":["TEXT"],"tokenLimits":{"maxInputTokens":164000,"maxOutputTokens":64000}}
],"nextToken":null}`

func useKiroManagementServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	prev := resolveKiroManagementEndpoint
	resolveKiroManagementEndpoint = func(string) string { return server.URL }
	t.Cleanup(func() { resolveKiroManagementEndpoint = prev })
}

func TestSyncUpstreamModelCatalogListsKiroOAuthModels(t *testing.T) {
	useKiroManagementServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/List-Available-Models", r.URL.Path)
		require.Equal(t, "KIRO_CLI", r.URL.Query().Get("origin"))
		require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST", r.URL.Query().Get("profileArn"))
		require.Equal(t, "Bearer kiro-access", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, kiroListModelsFixture)
	})

	account := &Account{
		ID:       0,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "kiro-access",
			"expires_at":   time.Now().Add(time.Hour).Format(time.RFC3339),
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	svc := &AccountTestService{httpUpstream: &queuedHTTPUpstream{}, kiroTokenProvider: NewKiroTokenProvider(nil, nil, nil)}

	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	// 已知 Claude 模型换成网关 ID（含 -thinking 变体）；auto 等无法直通的模型被丢弃。
	require.Contains(t, catalog.Models, "claude-opus-4-8")
	require.Contains(t, catalog.Models, "claude-opus-4-8-thinking")
	require.Contains(t, catalog.Models, "claude-haiku-5.5")
	require.Contains(t, catalog.Models, "gpt-5.6-sol")
	require.NotContains(t, catalog.Models, "auto")
	require.NotContains(t, catalog.Models, "deepseek-3.2")

	meta := catalog.Metadata["claude-opus-4-8"]
	require.Equal(t, int64(1000000), meta.ContextWindow)
	require.Equal(t, int64(128000), meta.MaxOutputTokens)
	require.Contains(t, meta.InputModalities, "image")
	// 上游点号 ID 只补元数据，不出现在可选模型里。
	require.NotContains(t, catalog.Models, "claude-opus-4.8")
	require.Equal(t, int64(1000000), catalog.Metadata["claude-opus-4.8"].ContextWindow)
}

func TestListKiroAvailableModelsUsesControlPlaneForAPIKey(t *testing.T) {
	useKiroManagementServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "KiroControlPlaneBearerService.ListAvailableModels", r.Header.Get("X-Amz-Target"))
		require.Equal(t, "API_KEY", r.Header.Get("TokenType"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "KIRO_CLI", body["origin"])
		_, _ = io.WriteString(w, kiroListModelsFixture)
	})

	account := &Account{Platform: PlatformKiro, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "ksk-test"}}
	models, err := listKiroAvailableModels(context.Background(), account, kiroDefaultRegion, "", "ksk-test")
	require.NoError(t, err)
	require.Len(t, models, 5)
}

func TestListKiroAvailableModelsReportsUpstreamStatus(t *testing.T) {
	useKiroManagementServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"denied"}`, http.StatusForbidden)
	})

	account := &Account{Platform: PlatformKiro, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "ksk-test"}}
	_, err := listKiroAvailableModels(context.Background(), account, kiroDefaultRegion, "", "ksk-test")
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, http.StatusForbidden, syncErr.StatusCode)
}

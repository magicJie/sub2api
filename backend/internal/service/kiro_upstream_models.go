package service

// Kiro（fork）从上游同步模型列表（借鉴 magpie-community/plugins packages/kiro，MIT）。
//
// OAuth 账号调 Kiro 管理面 List-Available-Models（带 profileArn）；直连 API Key 账号按 kiro-cli
// 的方式调控制面 KiroControlPlaneBearerService.ListAvailableModels。上游返回点号 ID
// （claude-opus-4.8），这里换成网关对外的模型 ID：已知 Claude 模型展开为目录里的短横线写法
// 及其 -thinking 变体，其它原样保留（由 MapModel 直通）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	kiroListModelsOrigin   = "KIRO_CLI"
	kiroListModelsMaxPages = 20
)

// resolveKiroManagementEndpoint 可在测试中替换。
var resolveKiroManagementEndpoint = kiroManagementEndpoint

func kiroManagementEndpoint(region string) string {
	if region != kiroEURegion {
		region = kiroDefaultRegion
	}
	return "https://management." + region + ".kiro.dev"
}

type kiroAvailableModel struct {
	ModelID             string   `json:"modelId"`
	ModelName           string   `json:"modelName"`
	SupportedInputTypes []string `json:"supportedInputTypes"`
	TokenLimits         *struct {
		MaxInputTokens  int64 `json:"maxInputTokens"`
		MaxOutputTokens int64 `json:"maxOutputTokens"`
	} `json:"tokenLimits"`
}

type kiroListModelsResponse struct {
	Models    []kiroAvailableModel `json:"models"`
	NextToken string               `json:"nextToken"`
}

// fetchKiroUpstreamModels 返回网关模型 ID 与一份可被 extractUpstreamModelCatalog 解析的能力元数据。
func (s *AccountTestService) fetchKiroUpstreamModels(ctx context.Context, account *Account) ([]string, []byte, error) {
	token, err := s.kiroModelListToken(ctx, account)
	if err != nil {
		return nil, nil, err
	}
	region := kiroAPIRegion(account)
	profileArn := ""
	if account.Type == AccountTypeOAuth {
		profileArn = strings.TrimSpace(account.GetCredential("profile_arn"))
		if profileArn == "" {
			profileArn, _ = resolveKiroProfileArn(ctx, s.accountRepo, account, region, token, false)
		}
	}

	upstream, err := listKiroAvailableModels(ctx, account, region, profileArn, token)
	if err != nil {
		return nil, nil, err
	}
	models, entries := kiroGatewayModelCatalog(upstream)
	if len(models) == 0 {
		return nil, nil, newUpstreamModelSyncUpstreamError("Upstream returned no supported models", nil)
	}
	body, err := json.Marshal(map[string]any{"data": entries})
	if err != nil {
		return nil, nil, newUpstreamModelSyncInternalError("Failed to encode Kiro model catalog", err)
	}
	return models, body, nil
}

func (s *AccountTestService) kiroModelListToken(ctx context.Context, account *Account) (string, error) {
	switch account.Type {
	case AccountTypeOAuth:
		if s.kiroTokenProvider == nil {
			return "", newUpstreamModelSyncConfigError("Kiro token provider is not configured", nil)
		}
		token, err := s.kiroTokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return "", newUpstreamModelSyncUpstreamError("Failed to get Kiro access token", err)
		}
		return token, nil
	case AccountTypeAPIKey:
		token := firstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key")
		if token == "" {
			return "", newUpstreamModelSyncConfigError("Kiro API key is missing", nil)
		}
		return token, nil
	default:
		return "", newUpstreamModelSyncUnsupportedError("Unsupported Kiro account type", nil)
	}
}

func listKiroAvailableModels(ctx context.Context, account *Account, region, profileArn, token string) ([]kiroAvailableModel, error) {
	endpoint := resolveKiroManagementEndpoint(region)
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:           accountProxyURL(account),
		Timeout:            30 * time.Second,
		ValidateResolvedIP: true,
		AllowPrivateHosts:  isLoopbackEndpoint(endpoint),
	})
	if err != nil {
		return nil, newUpstreamModelSyncInternalError("Failed to create Kiro model list client", err)
	}

	var models []kiroAvailableModel
	nextToken := ""
	for page := 0; page < kiroListModelsMaxPages; page++ {
		req, err := newKiroListModelsRequest(ctx, account, endpoint, profileArn, token, nextToken)
		if err != nil {
			return nil, newUpstreamModelSyncInternalError("Failed to build Kiro model list request", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, newUpstreamModelSyncUpstreamError("Failed to request upstream model list", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, newUpstreamModelSyncUpstreamError("Failed to read upstream model list", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &UpstreamModelSyncError{
				Kind:       UpstreamModelSyncErrorUpstream,
				Message:    fmt.Sprintf("Upstream model list request failed with status %d", resp.StatusCode),
				StatusCode: resp.StatusCode,
				Err:        fmt.Errorf("%s", sanitizeUpstreamErrorMessage(strings.TrimSpace(string(body)))),
			}
		}
		var parsed kiroListModelsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, newUpstreamModelSyncUpstreamError("Upstream model list response was not valid JSON", err)
		}
		models = append(models, parsed.Models...)
		nextToken = strings.TrimSpace(parsed.NextToken)
		if nextToken == "" {
			break
		}
	}
	return models, nil
}

func newKiroListModelsRequest(ctx context.Context, account *Account, endpoint, profileArn, token, nextToken string) (*http.Request, error) {
	var req *http.Request
	var err error
	if account.Type == AccountTypeAPIKey {
		// kiro-cli 对 API Key 走控制面：AWS JSON 1.0，操作名放在 X-Amz-Target。
		payload := map[string]any{"origin": kiroListModelsOrigin}
		if nextToken != "" {
			payload["nextToken"] = nextToken
		}
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, marshalErr
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/?origin="+kiroListModelsOrigin, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("X-Amz-Target", "KiroControlPlaneBearerService.ListAvailableModels")
	} else {
		query := url.Values{"origin": {kiroListModelsOrigin}}
		if profileArn != "" {
			query.Set("profileArn", profileArn)
		}
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/List-Available-Models?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
	}
	setKiroRuntimeHeaders(req, account, token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// kiroGatewayModelCatalog 把上游模型换成网关模型 ID，并生成对应的能力条目（条目可多于模型）。
func kiroGatewayModelCatalog(upstream []kiroAvailableModel) ([]string, []map[string]any) {
	aliases := make(map[string][]string, len(kiropkg.DefaultModels))
	for _, model := range kiropkg.DefaultModels {
		if target := kiropkg.MapModel(model.ID); target != "" {
			aliases[target] = append(aliases[target], model.ID)
		}
	}

	seen := make(map[string]bool)
	var models []string
	var entries []map[string]any
	for _, item := range upstream {
		upstreamID := strings.ToLower(strings.TrimSpace(item.ModelID))
		if upstreamID == "" {
			continue
		}
		ids := aliases[upstreamID]
		if len(ids) == 0 {
			ids = []string{upstreamID}
		} else if !seen[upstreamID] && !slices.Contains(ids, upstreamID) {
			// 映射目标是上游点号 ID（如 claude-opus-4.8），也给它一份能力元数据，但不列入可选模型。
			seen[upstreamID] = true
			entries = append(entries, kiroModelCapabilityEntry(upstreamID, item))
		}
		for _, id := range ids {
			if seen[id] || kiropkg.MapModel(id) == "" {
				continue
			}
			seen[id] = true
			models = append(models, id)
			entries = append(entries, kiroModelCapabilityEntry(id, item))
		}
	}
	return models, entries
}

func kiroModelCapabilityEntry(id string, item kiroAvailableModel) map[string]any {
	modalities := []string{"text"}
	for _, inputType := range item.SupportedInputTypes {
		if strings.EqualFold(strings.TrimSpace(inputType), "IMAGE") {
			modalities = append(modalities, "image")
			break
		}
	}
	// 网关只对 Claude 家族做 thinking 转换，其它模型不声明推理能力。
	reasoning := strings.HasPrefix(id, "claude-")
	entry := map[string]any{
		"id":               id,
		"display_name":     id,
		"input_modalities": modalities,
		"reasoning":        reasoning,
	}
	if reasoning {
		entry["supported_reasoning_levels"] = []string{"low", "medium", "high"}
	}
	if item.TokenLimits != nil {
		if item.TokenLimits.MaxInputTokens > 0 {
			entry["context_window"] = item.TokenLimits.MaxInputTokens
		}
		if item.TokenLimits.MaxOutputTokens > 0 {
			entry["max_output_tokens"] = item.TokenLimits.MaxOutputTokens
		}
	}
	return entry
}

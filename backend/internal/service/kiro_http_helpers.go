package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
	"github.com/google/uuid"
)

func buildKiroAccountKey(account *Account) string {
	if account == nil {
		return ""
	}
	return kiropkg.BuildAccountKey(
		account.GetCredential("client_id"),
		account.GetCredential("client_id_hash"),
		account.GetCredential("refresh_token"),
		account.GetCredential("profile_arn"),
		account.ID,
	)
}

func buildKiroMachineID(account *Account) string {
	if account == nil {
		return kiropkg.BuildMachineID("", "", "account:nil")
	}
	for _, key := range []string{"machine_id", "machineId"} {
		if machineID, ok := kiropkg.NormalizeMachineID(account.GetCredential(key)); ok {
			return machineID
		}
	}
	fallbackKey := buildKiroMachineIDFallbackKey(account)
	if account.Type == AccountTypeAPIKey {
		return kiropkg.BuildMachineID("", firstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key"), fallbackKey)
	}
	return kiropkg.BuildMachineID(account.GetCredential("refresh_token"), "", fallbackKey)
}

func firstKiroCredential(account *Account, keys ...string) string {
	if account == nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func buildKiroMachineIDFallbackKey(account *Account) string {
	if account == nil {
		return "account:nil"
	}
	if account.ID > 0 {
		return fmt.Sprintf("account:%d", account.ID)
	}
	for _, key := range []string{"client_id", "profile_arn"} {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return key + ":" + value
		}
	}
	if name := strings.TrimSpace(account.Name); name != "" {
		return "name:" + name
	}
	return "account:unknown"
}

func buildKiroRequestID(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	if requestID := strings.TrimSpace(resp.Header.Get("x-request-id")); requestID != "" {
		return requestID
	}
	if requestID := strings.TrimSpace(resp.Header.Get("x-amzn-requestid")); requestID != "" {
		return requestID
	}
	return strings.TrimSpace(resp.Header.Get("x-amz-request-id"))
}

func isKiroSuspendedBody(respBody []byte) bool {
	body := string(respBody)
	return strings.Contains(body, "SUSPENDED") || strings.Contains(body, "TEMPORARILY_SUSPENDED")
}

func isKiroTokenErrorBody(respBody []byte) bool {
	lower := strings.ToLower(string(respBody))
	return strings.Contains(lower, "token") ||
		strings.Contains(lower, "expired") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "unauthorized")
}

func kiroProxyURL(account *Account) string {
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

func isKiroDirectModeAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformKiro {
		return false
	}
	if account.Type == AccountTypeOAuth {
		return true
	}
	return account.Type == AccountTypeAPIKey && strings.TrimSpace(account.GetCredential("base_url")) == ""
}

// kiroAPIRegion 返回账号的推理区域：显式配置的 api_region 优先；否则取 profileArn 所在区域
// （arn:aws:codewhisperer:<region>:...，限 Kiro 已知区域）；再否则按登录区域就近选 Kiro 的两个区域之一。
func kiroAPIRegion(account *Account) string {
	if account == nil {
		return kiroDefaultRegion
	}
	if region := strings.TrimSpace(account.GetCredential("api_region")); region != "" {
		return region
	}
	if region := kiroRegionFromProfileArn(account.GetCredential("profile_arn")); region != "" {
		return region
	}
	if strings.HasPrefix(strings.TrimSpace(account.GetCredential("region")), "eu-") {
		return kiroEURegion
	}
	return kiroDefaultRegion
}

func kiroRegionFromProfileArn(arn string) string {
	parts := strings.Split(strings.TrimSpace(arn), ":")
	if len(parts) > 4 && parts[0] == "arn" {
		// 只采用 Kiro 实际提供推理服务的区域，其它值不可信，交给后续回落。
		switch parts[3] {
		case kiroDefaultRegion, kiroEURegion:
			return parts[3]
		}
	}
	return ""
}

// isKiroBuilderIDAccount 判断账号是否通过 AWS Builder ID 登录。Builder ID 不支持
// ListAvailableProfiles，统一使用 kiroBuilderIDProfileArn。
func isKiroBuilderIDAccount(account *Account) bool {
	if account == nil {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(account.GetCredential("provider")))
	if provider == "builderid" || provider == "builder_id" || provider == "builder id" {
		return true
	}
	startURL := strings.TrimRight(strings.TrimSpace(account.GetCredential("start_url")), "/#")
	return startURL == kiropkg.BuilderIDStartURL
}

func applyKiroConditionalHeaders(req *http.Request, account *Account) {
	if req == nil || account == nil {
		return
	}
	if account.Type == AccountTypeAPIKey {
		req.Header.Set("TokenType", "API_KEY")
		return
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "external_idp") {
		req.Header.Set("TokenType", "EXTERNAL_IDP")
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("provider")), "Internal") {
		req.Header.Set("redirect-for-internal", "true")
	}
}

func resolveKiroPayloadProfileArn(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("profile_arn"))
}

func newKiroJSONRequest(ctx context.Context, endpointURL string, payload []byte, token, accountKey, machineID, amzTarget string, account *Account) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", kiropkg.BuildRuntimeUserAgent(accountKey, machineID))
	req.Header.Set("X-Amz-User-Agent", kiropkg.BuildRuntimeAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	req.Header.Set("Amz-Sdk-Invocation-Id", uuid.NewString())
	if amzTarget != "" {
		req.Header.Set("X-Amz-Target", amzTarget)
	}
	if account != nil {
		profileArn := strings.TrimSpace(account.GetCredential("profile_arn"))
		if profileArn != "" {
			req.Header.Set("x-amzn-kiro-profile-arn", profileArn)
		}
	}
	applyKiroConditionalHeaders(req, account)
	return req, nil
}

package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/Wei-Shaw/sub2api/internal/util/httputil"
)

// Reachability is an unauthenticated observation, not a login or inference test.
type ProxyAIPlatformAssessment struct {
	Target   string `json:"target"`
	Platform string `json:"platform"`
	Surface  string `json:"surface"`
	Status   string `json:"status"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
}

type proxyQualityTarget struct {
	Target   string
	Platform string
	Surface  string
	URL      string
	Method   string
	Body     string
}

var proxyQualityTargets = []proxyQualityTarget{
	{Target: "anthropic", Platform: PlatformAnthropic, Surface: "api", URL: "https://api.anthropic.com/v1/models", Method: http.MethodGet},
	{Target: "claude_web", Platform: PlatformAnthropic, Surface: "web", URL: "https://claude.ai/login", Method: http.MethodGet},
	{Target: "openai", Platform: PlatformOpenAI, Surface: "api", URL: "https://api.openai.com/v1/models", Method: http.MethodGet},
	{Target: "chatgpt", Platform: PlatformOpenAI, Surface: "web", URL: "https://chatgpt.com/backend-api/models", Method: http.MethodGet},
	{Target: "gemini", Platform: PlatformGemini, Surface: "api", URL: "https://generativelanguage.googleapis.com/v1beta/models", Method: http.MethodGet},
	{Target: "gemini_web", Platform: PlatformGemini, Surface: "web", URL: "https://gemini.google.com/app", Method: http.MethodGet},
	{Target: "gemini_code_assist", Platform: PlatformGemini, Surface: "code_assist", URL: "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", Method: http.MethodPost, Body: "{}"},
	{Target: "grok", Platform: PlatformGrok, Surface: "api", URL: "https://api.x.ai/v1/models", Method: http.MethodGet},
}

type proxyAIError struct {
	Code, Type, Status, Message string
	Native                      bool
}

// Read the providers' error envelopes once, including Google ErrorInfo details.
func readProxyAIError(body []byte) proxyAIError {
	var envelope struct {
		Error  json.RawMessage `json:"error"`
		Detail json.RawMessage `json:"detail"`
		Code   string          `json:"code"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return proxyAIError{}
	}
	if len(envelope.Detail) > 0 {
		var detail string
		if json.Unmarshal(envelope.Detail, &detail) == nil {
			return proxyAIError{Message: detail, Native: true}
		}
		envelope.Error = envelope.Detail
	}
	var object struct {
		Code    json.RawMessage `json:"code"`
		Type    string          `json:"type"`
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	}
	if len(envelope.Error) == 0 || json.Unmarshal(envelope.Error, &object) != nil {
		var message string
		if envelope.Code != "" && json.Unmarshal(envelope.Error, &message) == nil {
			return proxyAIError{Code: envelope.Code, Message: message, Native: true}
		}
		return proxyAIError{}
	}
	var code string
	_ = json.Unmarshal(object.Code, &code) // Google's numeric HTTP code is not a provider error identifier.
	result := proxyAIError{Code: code, Type: object.Type, Status: object.Status, Message: object.Message,
		Native: object.Message != "" || object.Type != "" || object.Status != "" || code != ""}
	for _, detail := range object.Details {
		if detail.Reason != "" {
			if result.Code == "" {
				result.Code = detail.Reason
			}
		}
	}
	return result
}

// Ignore scripts/styles: translated refusal strings in JS bundles are not observations.
func proxyAIVisiblePage(body []byte) (title, visible string) {
	tokens := html.NewTokenizer(bytes.NewReader(body))
	var titleText, text strings.Builder
	var hidden, inTitle bool
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return strings.ToLower(titleText.String()), strings.ToLower(text.String())
		case html.StartTagToken:
			name, _ := tokens.TagName()
			switch string(name) {
			case "script", "style":
				hidden = true
			case "title":
				inTitle = true
			}
		case html.EndTagToken:
			name, _ := tokens.TagName()
			switch string(name) {
			case "script", "style":
				hidden = false
			case "title":
				inTitle = false
			}
		case html.TextToken:
			if hidden {
				continue
			}
			chunk := strings.TrimSpace(string(tokens.Text()))
			if inTitle {
				titleText.WriteString(chunk)
			}
			if chunk != "" && text.Len() < 16*1024 {
				text.WriteString(chunk)
				text.WriteByte(' ')
			}
		}
	}
}

func classifyProxyAIResponse(item *ProxyQualityCheckItem, target proxyQualityTarget, response *http.Response, body []byte) {
	item.Status, item.Evidence, item.Message = "warn", "unexpected_response", "响应不足以确认该 AI 服务的出口访问情况，需复检"
	finalURL := response.Request.URL
	if target.Target == "claude_web" && finalURL.Scheme == "https" &&
		finalURL.Host == "claude.com" && finalURL.Path == "/app-unavailable-in-region" {
		item.Status, item.Evidence, item.Message = "fail", "geo_restricted", "该 AI 服务明确返回地区或国家限制；此结论只作用于该服务路径"
		return
	}
	expectedURL, parseErr := url.Parse(target.URL)
	if parseErr != nil || finalURL.Host != expectedURL.Host || finalURL.Scheme != expectedURL.Scheme {
		item.Message = "请求被重定向到其他站点，未确认原 AI 服务路径；需复检"
		return
	}
	errInfo := readProxyAIError(body)
	item.ErrorCode = errInfo.Code
	if item.ErrorCode == "" {
		item.ErrorCode = errInfo.Type
	}
	if item.ErrorCode == "" {
		item.ErrorCode = errInfo.Status
	}
	message := strings.ToLower(errInfo.Message)
	code := strings.ToLower(errInfo.Code)
	var visible, title string
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		title, visible = proxyAIVisiblePage(body)
	}
	path := strings.ToLower(finalURL.Path)
	// Billing prerequisites and organization allowlists are account-scoped, not bad-IP evidence.
	if (strings.Contains(message, "free tier") && strings.Contains(message, "country")) ||
		code == "ip_not_authorized" || strings.Contains(message, "ip not authorized") || strings.Contains(message, "ip allowlist") {
		item.Status, item.Evidence, item.Message = "warn", "account_requirement", "服务要求账号计费或组织访问条件；当前探测无法确认该账号可用，未据此判 IP 欺诈"
		return
	}
	geoRestricted := false
	switch code {
	case "unsupported_country_region_territory", "unsupported_country", "unsupported_region", "country_not_supported", "region_not_supported", "location_not_supported":
		geoRestricted = true
	}
	for _, phrase := range []string{"user location is not supported", "country, region, or territory not supported", "not available in your country", "not available in your region", "not supported in your country", "not supported in your region", "unable to provide services in your country"} {
		if strings.Contains(message, phrase) || strings.Contains(visible, phrase) {
			geoRestricted = true
		}
	}
	if strings.Contains(path, "unsupported-country") || strings.Contains(path, "unsupported-region") {
		geoRestricted = true
	}
	if geoRestricted {
		item.Status, item.Evidence, item.Message = "fail", "geo_restricted", "该 AI 服务明确返回地区或国家限制；此结论只作用于该服务路径"
		return
	}
	ipBlocked := code == "ip_blocked" || code == "ip_address_blocked" || code == "blocked_ip"
	if !ipBlocked {
		for _, phrase := range []string{"your ip has been blocked", "ip address has been blocked", "your ip address is blocked"} {
			if strings.Contains(message, phrase) || strings.Contains(visible, phrase) {
				ipBlocked = true
				break
			}
		}
	}
	if ipBlocked {
		item.Status, item.Evidence, item.Message = "fail", "ip_blocked", "该 AI 服务明确拒绝此 IP；其他 AI 服务单独判断"
		return
	}
	if httputil.IsCloudflareChallengeResponse(response.StatusCode, response.Header, body) {
		item.Status, item.Evidence, item.Message = "challenge", "waf_challenge", "本次请求遇到访问挑战，尚未验证正常访问；挑战原因未归因到 IP 本身"
		item.CFRay = httputil.ExtractCloudflareRayID(response.Header, body)
		return
	}
	if code == "insufficient_quota" || code == "credit_balance_exhausted" || code == "organization_spend_limit_exceeded" ||
		code == "quota_exceeded" || errInfo.Type == "billing_error" || errInfo.Type == "insufficient_quota" || (errInfo.Native && response.StatusCode == http.StatusPaymentRequired) {
		item.Status, item.Evidence, item.Message = "warn", "quota_exhausted", "返回账号额度或计费限制，当前路径需复检；未据此判 IP 欺诈"
		return
	}
	if response.StatusCode == http.StatusTooManyRequests {
		item.Status, item.Evidence, item.Message = "warn", "rate_limited", "本次请求受到频控；需复检，单次 429 不作为 IP 黑名单证据"
		return
	}
	if response.StatusCode >= 500 {
		item.Status, item.Evidence, item.Message = "warn", "provider_overloaded", "上游服务异常或过载，当前可达性未确认；需复检"
		return
	}
	// Never turn an arbitrary 401/403 page into a successful AI observation.
	authBoundary := false
	if errInfo.Native {
		switch target.Target {
		case "anthropic":
			authBoundary = response.StatusCode == 401 && errInfo.Type == "authentication_error"
		case "openai", "grok":
			authBoundary = response.StatusCode == 401 && (errInfo.Type == "authentication_error" || code == "invalid_api_key" || code == "missing_api_key" ||
				(errInfo.Type == "invalid_request_error" && (strings.Contains(message, "api key") || strings.Contains(message, "authentication") || strings.Contains(message, "missing bearer") || strings.Contains(message, "authorization"))))
			if target.Target == "grok" && response.StatusCode == 401 && code == "unauthenticated:no-credentials" {
				authBoundary = true
			}
		case "chatgpt":
			authBoundary = response.StatusCode == 401 && (message == "unauthorized" || message == "not authenticated" || strings.Contains(message, "authentication token") || strings.Contains(message, "not logged in"))
		case "gemini", "gemini_code_assist":
			authBoundary = (response.StatusCode == 401 && errInfo.Status == "UNAUTHENTICATED") ||
				((response.StatusCode == 400 || response.StatusCode == 403) && (code == "api_key_invalid" || strings.Contains(message, "unregistered callers") || strings.Contains(message, "api key not valid") || strings.Contains(message, "api key is missing")))
		}
	}
	if authBoundary {
		item.Status, item.Evidence, item.Message = "pass", "authentication_required", "已抵达该 AI 服务的鉴权边界；仅验证出口访问，登录与推理仍需有效账号验证"
		return
	}
	if response.StatusCode == http.StatusOK {
		if target.Target == "claude_web" || target.Target == "gemini_web" {
			// Provider app/login HTML is weak positive reachability evidence, not account access.
			providerTitle := (target.Target == "claude_web" && strings.Contains(title, "claude")) ||
				(target.Target == "gemini_web" && strings.Contains(title, "gemini"))
			if providerTitle && !strings.Contains(title, "error") && !strings.Contains(visible, "access denied") {
				item.Status, item.Evidence, item.Message = "pass", "web_reachable", "观察到该 AI 的公开应用或登录页面；仅验证网页入口，未验证登录和对话"
			}
			return
		}
		var payload struct {
			Object string          `json:"object"`
			Data   json.RawMessage `json:"data"`
			Models json.RawMessage `json:"models"`
		}
		if json.Unmarshal(body, &payload) == nil && !errInfo.Native &&
			(len(payload.Data) > 0 && bytes.HasPrefix(bytes.TrimSpace(payload.Data), []byte("[")) || len(payload.Models) > 0 && (bytes.HasPrefix(bytes.TrimSpace(payload.Models), []byte("[")) || bytes.HasPrefix(bytes.TrimSpace(payload.Models), []byte("{")))) {
			item.Status, item.Evidence, item.Message = "pass", "api_reachable", "取得该 AI 服务的模型元数据响应；未验证真实推理请求"
		}
		return
	}
	if response.StatusCode == 403 || response.StatusCode == 451 {
		item.Status, item.Evidence, item.Message = "fail", "access_denied", "该 AI 路径返回访问拒绝，具体原因尚未确认；不推断为 IP 欺诈"
		return
	}
	if !errInfo.Native && (response.StatusCode == 401 || response.StatusCode == 400) {
		item.Evidence = "invalid_response"
		item.Message = "收到未识别的错误响应，不能作为 AI 接口可达证据；需复检"
	}
}

func assessProxyAIPlatforms(result *ProxyQualityCheckResult) {
	result.AIPlatforms = make([]ProxyAIPlatformAssessment, 0, len(proxyQualityTargets))
	for _, item := range result.Items {
		if item.Platform == "" {
			continue
		}
		status := "unknown"
		switch item.Evidence {
		case "authentication_required", "api_reachable", "web_reachable":
			status = "reachable"
		case "geo_restricted", "ip_blocked", "access_denied":
			status = "restricted"
		case "waf_challenge":
			status = "challenge"
		case "rate_limited":
			status = "rate_limited"
		case "quota_exhausted", "account_requirement":
			status = "account_limited"
		case "network_error", "provider_overloaded":
			status = "unavailable"
		}
		eligible := result.BaseEligible && item.Status == "pass" && status == "reachable"
		result.AIPlatforms = append(result.AIPlatforms, ProxyAIPlatformAssessment{
			Target: item.Target, Platform: item.Platform, Surface: item.Surface,
			Status: status, Eligible: eligible, Reason: item.Message, Evidence: item.Evidence,
		})
		result.Eligible = result.Eligible || eligible
	}
}

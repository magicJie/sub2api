package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFinalizeProxyQualityResult_ConnectivityCannotProveReputation(t *testing.T) {
	result := &ProxyQualityCheckResult{PassedCount: 5}
	finalizeProxyQualityResult(result)
	require.Equal(t, 0, result.Score)
	require.Equal(t, "Unknown", result.Grade)
	require.False(t, result.BaseEligible)
	require.False(t, result.Eligible)
	require.NotEqual(t, "healthy", proxyQualityOverallStatus(result))
}

func TestRunProxyQualityTarget_AIAdverseEvidence(t *testing.T) {
	cases := []struct {
		name, target, contentType, body, status, evidence string
		httpStatus                                        int
		challenge                                         bool
	}{
		{name: "OpenAI authentication boundary", target: "openai", httpStatus: 401, body: `{"error":{"type":"invalid_request_error","message":"Missing bearer or basic authentication in header"}}`, status: "pass", evidence: "authentication_required"},
		{name: "Claude authentication boundary", target: "anthropic", httpStatus: 401, body: `{"type":"error","error":{"type":"authentication_error","message":"x-api-key header is required"}}`, status: "pass", evidence: "authentication_required"},
		{name: "ChatGPT authentication boundary", target: "chatgpt", httpStatus: 401, body: `{"detail":"Unauthorized"}`, status: "pass", evidence: "authentication_required"},
		{name: "Gemini unregistered caller", target: "gemini", httpStatus: 403, body: `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Method doesn't allow unregistered callers (callers without established identity). Please use API Key"}}`, status: "pass", evidence: "authentication_required"},
		{name: "Gemini CodeAssist authentication boundary", target: "gemini_code_assist", httpStatus: 401, body: `{"error":{"code":401,"status":"UNAUTHENTICATED","message":"Request is missing required authentication credential"}}`, status: "pass", evidence: "authentication_required"},
		{name: "Gemini invalid key ErrorInfo", target: "gemini", httpStatus: 400, body: `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"API key not valid","details":[{"reason":"API_KEY_INVALID"}]}}`, status: "pass", evidence: "authentication_required"},
		{name: "Gemini real models metadata", target: "gemini", httpStatus: 200, body: `{"models":[{"name":"models/gemini-test"}]}`, status: "pass", evidence: "api_reachable"},
		{name: "HTML 401 is not authentication proof", target: "openai", httpStatus: 401, contentType: "text/html", body: `<html><title>Access denied</title><body>Proxy authentication gateway</body></html>`, status: "warn", evidence: "invalid_response"},
		{name: "OpenAI country refusal", target: "openai", httpStatus: 403, body: `{"error":{"code":"unsupported_country_region_territory","message":"Country not supported"}}`, status: "fail", evidence: "geo_restricted"},
		{name: "Claude country refusal within old allowed 400", target: "anthropic", httpStatus: 400, body: `{"error":{"type":"invalid_request_error","message":"Claude is not available in your country"}}`, status: "fail", evidence: "geo_restricted"},
		{name: "ChatGPT nested country refusal", target: "chatgpt", httpStatus: 403, body: `{"detail":{"code":"unsupported_country_region_territory","message":"Country not supported"}}`, status: "fail", evidence: "geo_restricted"},
		{name: "Gemini user location refusal", target: "gemini", httpStatus: 400, body: `{"error":{"code":400,"status":"FAILED_PRECONDITION","message":"User location is not supported for the API use."}}`, status: "fail", evidence: "geo_restricted"},
		{name: "Gemini billing-dependent region is account requirement", target: "gemini", httpStatus: 400, body: `{"error":{"status":"FAILED_PRECONDITION","message":"Gemini API free tier is not available in your country. Please enable billing on your project."}}`, status: "warn", evidence: "account_requirement"},
		{name: "Explicit IP refusal", target: "chatgpt", httpStatus: 403, body: `{"error":{"code":"ip_blocked","message":"Your IP has been blocked"}}`, status: "fail", evidence: "ip_blocked"},
		{name: "Organization IP allowlist is account requirement", target: "openai", httpStatus: 401, body: `{"error":{"code":"ip_not_authorized","message":"IP not authorized"}}`, status: "warn", evidence: "account_requirement"},
		{name: "Cloudflare challenge", target: "chatgpt", httpStatus: 403, contentType: "text/html", body: `<html><title>Just a moment...</title><script>window._cf_chl_opt={}</script></html>`, challenge: true, status: "challenge", evidence: "waf_challenge"},
		{name: "429 is transient not IP blacklist", target: "openai", httpStatus: 429, body: `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"Rate limit reached"}}`, status: "warn", evidence: "rate_limited"},
		{name: "429 exhausted account credits", target: "openai", httpStatus: 429, body: `{"error":{"type":"insufficient_quota","code":"credit_balance_exhausted","message":"No credit"}}`, status: "warn", evidence: "quota_exhausted"},
		{name: "429 quota type without code", target: "openai", httpStatus: 429, body: `{"error":{"type":"insufficient_quota","message":"Check your plan and billing details"}}`, status: "warn", evidence: "quota_exhausted"},
		{name: "Grok authentication boundary", target: "grok", httpStatus: 401, body: `{"code":"unauthenticated:no-credentials","error":"No credentials presented."}`, status: "pass", evidence: "authentication_required"},
		{name: "Claude overload", target: "anthropic", httpStatus: 529, body: `{"error":{"type":"overloaded_error","message":"Overloaded"}}`, status: "warn", evidence: "provider_overloaded"},
		{name: "Unexplained 403 is not geography proof", target: "gemini", httpStatus: 403, body: `{"error":{"status":"PERMISSION_DENIED","message":"Permission denied"}}`, status: "fail", evidence: "access_denied"},
		{name: "200 error envelope is not success", target: "openai", httpStatus: 200, body: `{"error":{"code":"unsupported_country_region_territory","message":"Country not supported"}}`, status: "fail", evidence: "geo_restricted"},
		{name: "200 arbitrary JSON is not success", target: "gemini", httpStatus: 200, body: `{"status":"ok"}`, status: "warn", evidence: "unexpected_response"},
		{name: "401 invalid request without auth explanation is unknown", target: "openai", httpStatus: 401, body: `{"error":{"type":"invalid_request_error","message":"Request cannot be processed"}}`, status: "warn", evidence: "unexpected_response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				contentType := tc.contentType
				if contentType == "" {
					contentType = "application/json"
				}
				w.Header().Set("Content-Type", contentType)
				if tc.challenge {
					w.Header().Set("cf-mitigated", "challenge")
					w.Header().Set("cf-ray", "test-ray")
				}
				if tc.httpStatus == 429 {
					w.Header().Set("Retry-After", "30")
				}
				w.WriteHeader(tc.httpStatus)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			item := runProxyQualityTarget(context.Background(), server.Client(), proxyQualityTarget{Target: tc.target, URL: server.URL, Method: http.MethodGet})
			require.Equal(t, tc.status, item.Status)
			require.Equal(t, tc.evidence, item.Evidence)
			if tc.challenge {
				require.Equal(t, "test-ray", item.CFRay)
			}
			if tc.httpStatus == 429 {
				require.Equal(t, "30", item.RetryAfter)
			}
		})
	}
}

func TestProxyAIWebEvidenceIgnoresScriptTranslations(t *testing.T) {
	for _, tc := range []struct{ name, target, body, status, evidence string }{
		{"Claude login page", "claude_web", `<html><title>Claude</title><body>Log in to Claude</body><script>const translation="not available in your country"; const other="Your IP address has been blocked"</script></html>`, "pass", "web_reachable"},
		{"Actual country refusal", "claude_web", `<html><title>Claude</title><body>Claude is not available in your country</body></html>`, "fail", "geo_restricted"},
		{"Unrelated captive page", "claude_web", `<html><title>Sign in to network</title><body>Welcome</body></html>`, "warn", "unexpected_response"},
		{"Claude visible IP block despite 200", "claude_web", `<html><title>Claude</title><body>Your IP address has been blocked</body></html>`, "fail", "ip_blocked"},
		{"Gemini visible denial despite 200", "gemini_web", `<html><title>Gemini</title><body>Access denied</body></html>`, "warn", "unexpected_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			item := runProxyQualityTarget(context.Background(), server.Client(), proxyQualityTarget{Target: tc.target, Surface: "web", URL: server.URL, Method: http.MethodGet})
			require.Equal(t, tc.status, item.Status)
			require.Equal(t, tc.evidence, item.Evidence)
		})
	}
}

func TestProxyAISeparateServiceEligibility(t *testing.T) {
	now := time.Now()
	reputation := assessProxyIPReputation(reputationFixture(now), netip.MustParseAddr("8.8.8.8"), now)
	result := &ProxyQualityCheckResult{Reputation: reputation, PassedCount: 3, FailedCount: 1, Items: []ProxyQualityCheckItem{
		{Target: "base_connectivity", Status: "pass"},
		{Target: "anthropic", Platform: PlatformAnthropic, Surface: "api", Status: "fail", Evidence: "geo_restricted"},
		{Target: "gemini", Platform: PlatformGemini, Surface: "api", Status: "pass", Evidence: "authentication_required"},
		{Target: "openai", Platform: PlatformOpenAI, Surface: "api", Status: "pass", Evidence: "authentication_required"},
		{Target: "chatgpt", Platform: PlatformOpenAI, Surface: "web", Status: "challenge", Evidence: "waf_challenge"},
	}}
	finalizeProxyQualityResult(result)
	require.True(t, result.BaseEligible)
	require.True(t, result.Eligible)
	require.Equal(t, "warn", proxyQualityOverallStatus(result))
	proxy := &Proxy{Host: "8.8.8.8", Protocol: "socks5", Port: 1080}
	checked := now.Unix()
	info := &ProxyLatencyInfo{Success: true, IPAddress: proxy.Host, QualityBaseEligible: result.BaseEligible, QualityAIPlatforms: result.AIPlatforms, QualityCheckedAt: &checked, QualityValidUntil: reputation.ValidUntil, QualityFingerprint: proxyQualityFingerprint(proxy)}
	require.True(t, proxyQualityAdmissibleForTarget(proxy, info, "gemini", now))
	require.True(t, proxyQualityAdmissibleForTarget(proxy, info, "openai", now))
	require.False(t, proxyQualityAdmissibleForTarget(proxy, info, "anthropic", now))
	require.False(t, proxyQualityAdmissibleForTarget(proxy, info, "chatgpt", now))
	require.False(t, proxyQualityAdmissibleForTarget(proxy, info, "gemini_code_assist", now))
	info.QualityAIPlatforms = nil
	require.False(t, proxyQualityAdmissibleForTarget(proxy, info, "gemini", now))
}

func TestProxyAIWebRedirectEvidence(t *testing.T) {
	for _, tc := range []struct{ name, finalURL, evidence, body string }{
		{"Claude official region redirect", "https://claude.com/app-unavailable-in-region", "geo_restricted", "<html><title>Claude</title><body>Sign in</body></html>"},
		{"Unrelated login redirect", "https://accounts.example.test/login", "unexpected_response", "<html><title>Claude</title><body>Sign in</body></html>"},
		{"Unrelated product region refusal", "https://accounts.example.test/login", "unexpected_response", "<html><title>Example</title><body>This product is not available in your country</body></html>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finalURL, err := url.Parse(tc.finalURL)
			require.NoError(t, err)
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}},
				Request: &http.Request{URL: finalURL}}
			item := ProxyQualityCheckItem{}
			classifyProxyAIResponse(&item, proxyQualityTarget{Target: "claude_web", Surface: "web", URL: "https://claude.ai/login"}, response, []byte(tc.body))
			require.Equal(t, tc.evidence, item.Evidence)
			require.NotEqual(t, "pass", item.Status)
		})
	}
}

package service

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// proxyQualityTargetForAccount follows the account's actual upstream access path. An empty
// target admits by exit identity and reputation only, for unprobed providers.
// Unsupported access modes use a target with no assessment and fail closed.
func proxyQualityTargetForAccount(account *Account) string {
	if account == nil {
		return ""
	}
	var target, host string
	switch account.Platform {
	case PlatformOpenAI:
		switch account.Type {
		case AccountTypeAPIKey, AccountTypeUpstream:
			target, host = "openai", "api.openai.com"
		case AccountTypeOAuth, AccountTypeSetupToken, "":
			target, host = "chatgpt", "chatgpt.com"
		default:
			return "unsupported_account_access"
		}
	case PlatformAnthropic:
		switch account.Type {
		case AccountTypeBedrock, AccountTypeServiceAccount:
			return ""
		case AccountTypeAPIKey, AccountTypeUpstream, AccountTypeOAuth, AccountTypeSetupToken, "":
			target, host = "anthropic", "api.anthropic.com"
		default:
			return "unsupported_account_access"
		}
	case PlatformGemini:
		switch account.Type {
		case AccountTypeServiceAccount:
			return "" // Vertex AI uses a separate endpoint and authentication path.
		case AccountTypeAPIKey, AccountTypeUpstream:
			target, host = "gemini", "generativelanguage.googleapis.com"
		case AccountTypeOAuth, "":
			switch account.GeminiOAuthType() {
			case "", "ai_studio", "code_assist", "google_one":
			default:
				return "unsupported_account_access"
			}
			if strings.TrimSpace(account.GetCredential("project_id")) != "" {
				target, host = "gemini_code_assist", "cloudcode-pa.googleapis.com"
			} else {
				target, host = "gemini", "generativelanguage.googleapis.com"
			}
		default:
			return "unsupported_account_access"
		}
	case PlatformGrok:
		switch account.Type {
		case AccountTypeAPIKey, AccountTypeOAuth, "":
			target, host = "grok", "api.x.ai"
		default:
			return "unsupported_account_access"
		}
	default:
		return ""
	}
	if account.Platform == PlatformGrok {
		if isOfficialGrokCodexBaseURL(account.GetGrokBaseURL()) {
			return target
		}
		return ""
	}
	if base := strings.TrimSpace(account.GetCredential("base_url")); base != "" &&
		(account.Type == AccountTypeAPIKey || account.Type == AccountTypeUpstream || target == "gemini") {
		parsed, err := url.Parse(base)
		if err != nil || !strings.EqualFold(parsed.Hostname(), host) {
			return ""
		}
	}
	return target
}

// checkNewAccountProxy checks newly attached proxies. A changed upstream target
// is a new access binding; callers pass no current proxy to recheck that target.
func (s *adminServiceImpl) checkNewAccountProxy(ctx context.Context, account *Account, current, requested *int64) error {
	if requested == nil || *requested <= 0 || !proxyIDChanged(current, requested) {
		return nil
	}
	now := time.Now()
	proxy, info, err := s.accountProxyAdmissionEvidence(ctx, *requested, now)
	if err != nil {
		return err
	}
	if !proxyQualityAdmissibleForTarget(proxy, info, proxyQualityTargetForAccount(account), now) {
		return ErrProxyPoolProxyNotInPool
	}
	return nil
}

func (s *adminServiceImpl) accountProxyAdmissionEvidence(ctx context.Context, proxyID int64, now time.Time) (*Proxy, *ProxyLatencyInfo, error) {
	if s.proxyRepo == nil || s.proxyLatencyCache == nil {
		return nil, nil, ErrProxyPoolQualityUnavailable
	}
	proxy, err := s.proxyRepo.GetByID(ctx, proxyID)
	if err != nil {
		return nil, nil, err
	}
	if proxy == nil || proxy.Status != StatusActive || proxy.IsExpired(now) {
		return nil, nil, ErrProxyPoolProxyNotInPool
	}
	infos, err := s.proxyLatencyCache.GetProxyLatencies(ctx, []int64{proxyID})
	if err != nil {
		return nil, nil, ErrProxyPoolQualityUnavailable.WithCause(err)
	}
	return proxy, infos[proxyID], nil
}

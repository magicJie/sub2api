package service

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// proxyQualityTargetForAccount follows the account's actual upstream access path for
// quality recommendations. Unprobed providers use exit identity and reputation only;
// unsupported access modes have no matching recommendation.
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

// checkAccountProxyAvailable validates the resource selected at the account boundary.
func (s *adminServiceImpl) checkAccountProxyAvailable(ctx context.Context, proxyID int64) error {
	if s.proxyRepo == nil {
		return ErrProxyPoolUnavailable
	}
	proxy, err := s.proxyRepo.GetByID(ctx, proxyID)
	if err != nil {
		return err
	}
	if proxy == nil || !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return ErrProxyPoolProxyNotInPool
	}
	return nil
}

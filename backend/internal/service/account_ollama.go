package service

import (
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const DefaultOllamaBaseURL = "https://ollama.com/v1"

// normalizeOllamaAccount validates account writes and stores the default endpoint.
func normalizeOllamaAccount(account *Account) error {
	if account.Platform != PlatformOllama {
		return nil
	}
	if account.Type != AccountTypeAPIKey {
		return infraerrors.BadRequest("OLLAMA_ACCOUNT_TYPE_INVALID", "ollama accounts only support apikey credentials")
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "ollama.com") ||
		(parsed.Port() != "" && parsed.Port() != "443") || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" ||
		(parsed.Path != "" && parsed.Path != "/" && parsed.Path != "/v1" && parsed.Path != "/v1/") {
		return infraerrors.BadRequest("OLLAMA_BASE_URL_INVALID", "ollama base_url must use https://ollama.com or https://ollama.com/v1")
	}
	if account.Credentials == nil {
		account.Credentials = make(map[string]any)
	}
	account.Credentials["base_url"] = baseURL
	return nil
}

// CRS keeps the platform assigned by migration when refreshing its source key.
func normalizeCRSAPIKeyPlatform(existing *Account, sourcePlatform string, credentials map[string]any) (string, error) {
	if !IsOllamaCloudUsageAccount(existing) {
		return sourcePlatform, nil
	}
	account := &Account{Platform: PlatformOllama, Type: AccountTypeAPIKey, Credentials: credentials}
	if err := normalizeOllamaAccount(account); err != nil {
		return "", err
	}
	return PlatformOllama, nil
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type accountAdmissionProxyRepo struct {
	ProxyRepository
	proxy *Proxy
}

func (r *accountAdmissionProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	return r.proxy, nil
}

type accountAdmissionCache struct {
	info  *ProxyLatencyInfo
	err   error
	calls int
}

func (c *accountAdmissionCache) GetProxyLatencies(_ context.Context, ids []int64) (map[int64]*ProxyLatencyInfo, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	out := make(map[int64]*ProxyLatencyInfo, len(ids))
	for _, id := range ids {
		out[id] = c.info
	}
	return out, nil
}

func (c *accountAdmissionCache) SetProxyLatency(context.Context, int64, *ProxyLatencyInfo) error {
	return nil
}

func accountAdmissionService(repo AccountRepository, proxyID int64) *adminServiceImpl {
	proxy := &Proxy{ID: proxyID, Host: "203.0.113.1", Status: StatusActive}
	checked := time.Now().Unix()
	return &adminServiceImpl{
		accountRepo: repo,
		proxyRepo:   &accountAdmissionProxyRepo{proxy: proxy},
		proxyLatencyCache: &accountAdmissionCache{info: &ProxyLatencyInfo{
			Success: true, IPAddress: proxy.Host, QualityBaseEligible: true,
			QualityCheckedAt: &checked, QualityValidUntil: time.Now().Add(time.Hour).Unix(),
			QualityFingerprint: proxyQualityFingerprint(proxy),
			QualityAIPlatforms: []ProxyAIPlatformAssessment{
				{Target: "anthropic", Status: "restricted", Eligible: false},
				{Target: "openai", Status: "reachable", Eligible: true},
				{Target: "chatgpt", Status: "restricted", Eligible: false},
				{Target: "gemini", Status: "reachable", Eligible: true},
				{Target: "gemini_code_assist", Status: "restricted", Eligible: false},
				{Target: "grok", Status: "reachable", Eligible: true},
			},
		}},
	}
}

func TestProxyQualityTargetForAccount(t *testing.T) {
	for _, tc := range []struct {
		name, platform, kind, oauthType, project, base, target string
	}{
		{"OpenAI API", "openai", "apikey", "", "", "", "openai"},
		{"OpenAI OAuth", "openai", "oauth", "", "", "", "chatgpt"},
		{"OpenAI setup token", "openai", "setup-token", "", "", "", "chatgpt"},
		{"omitted type", "openai", "", "", "", "", "chatgpt"},
		{"Claude API", "anthropic", "apikey", "", "", "", "anthropic"},
		{"Claude Code", "anthropic", "oauth", "", "", "", "anthropic"},
		{"Bedrock", "anthropic", "bedrock", "", "", "", ""},
		{"Claude Vertex", "anthropic", "service_account", "", "", "", ""},
		{"Gemini API", "gemini", "apikey", "", "", "", "gemini"},
		{"Gemini Vertex service account", "gemini", "service_account", "", "", "", ""},
		{"AI Studio", "gemini", "oauth", "ai_studio", "", "", "gemini"},
		{"Code Assist", "gemini", "oauth", "code_assist", "project", "", "gemini_code_assist"},
		{"Google One", "gemini", "oauth", "google_one", "project", "", "gemini_code_assist"},
		{"legacy Gemini OAuth", "gemini", "oauth", "", "project", "", "gemini_code_assist"},
		{"Grok", "grok", "apikey", "", "", "", "grok"},
		{"Grok CLI", "grok", "oauth", "", "", "https://cli-chat-proxy.grok.com/v1", "grok"},
		{"Kiro Claude models", "kiro", "oauth", "", "", "", ""},
		{"ignored OAuth base URL", "openai", "oauth", "", "", "https://relay.example/v1", "chatgpt"},
		{"Ollama", "ollama", "apikey", "", "", "", ""},
		{"Antigravity", "antigravity", "oauth", "", "", "", ""},
		{"custom relay", "anthropic", "apikey", "", "", "https://relay.example/v1", ""},
		{"unknown OpenAI type", "openai", "unknown", "", "", "", "unsupported_account_access"},
		{"unknown Gemini OAuth scope", "gemini", "oauth", "unknown", "", "", "unsupported_account_access"},
		{"AI Studio custom relay", "gemini", "oauth", "ai_studio", "", "https://relay.example/v1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Platform: tc.platform, Type: tc.kind, Credentials: map[string]any{"oauth_type": tc.oauthType, "project_id": tc.project, "base_url": tc.base}}
			require.Equal(t, tc.target, proxyQualityTargetForAccount(account))
		})
	}
}

func TestManualAccountSelectionUsesAvailabilityOnly(t *testing.T) {
	ctx := context.Background()
	id := int64(9)
	for _, evidence := range []string{"low", "unknown", "stale", "missing", "cache error", "no cache"} {
		for _, mutation := range []string{"create", "edit", "bulk"} {
			t.Run(evidence+"/"+mutation, func(t *testing.T) {
				repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
					1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
				}}
				svc := accountAdmissionService(&upstreamBillingProbeAdminRepo{repo}, id)
				cache := svc.proxyLatencyCache.(*accountAdmissionCache)
				switch evidence {
				case "low":
					cache.info.QualityBaseEligible = false
				case "unknown":
					cache.info.QualityAIPlatforms = nil
				case "stale":
					cache.info.QualityValidUntil = time.Now().Add(-time.Hour).Unix()
				case "missing":
					cache.info = nil
				case "cache error":
					cache.err = errors.New("cache offline")
				case "no cache":
					svc.proxyLatencyCache = nil
				}
				switch mutation {
				case "create":
					account, err := svc.CreateAccount(ctx, &CreateAccountInput{
						Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id, SkipDefaultGroupBind: true,
					})
					require.NoError(t, err)
					require.Equal(t, &id, repo.accounts[account.ID].ProxyID)
				case "edit":
					_, err := svc.UpdateAccount(ctx, 1, &UpdateAccountInput{ProxyID: &id})
					require.NoError(t, err)
					require.Equal(t, &id, repo.accounts[1].ProxyID)
				case "bulk":
					_, err := svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{1}, ProxyID: &id})
					require.NoError(t, err)
					require.Len(t, repo.bulkUpdates, 1)
					require.Equal(t, &id, repo.bulkUpdates[0].ProxyID)
				}
				require.Zero(t, cache.calls)
			})
		}
	}
}

func TestManualAccountSelectionRejectsUnavailableResources(t *testing.T) {
	id := int64(9)
	expired := time.Now().Add(-time.Minute)
	for _, unavailable := range []string{"missing", "inactive", "expired"} {
		for _, mutation := range []string{"create", "edit", "bulk"} {
			t.Run(unavailable+"/"+mutation, func(t *testing.T) {
				repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
					1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
				}}
				svc := accountAdmissionService(&upstreamBillingProbeAdminRepo{repo}, id)
				proxies := svc.proxyRepo.(*accountAdmissionProxyRepo)
				switch unavailable {
				case "missing":
					proxies.proxy = nil
				case "inactive":
					proxies.proxy.Status = StatusDisabled
				case "expired":
					proxies.proxy.ExpiresAt = &expired
				}
				var err error
				switch mutation {
				case "create":
					_, err = svc.CreateAccount(context.Background(), &CreateAccountInput{
						Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id, SkipDefaultGroupBind: true,
					})
					require.Len(t, repo.accounts, 1)
				case "edit":
					_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{ProxyID: &id})
				case "bulk":
					_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, ProxyID: &id})
				}
				require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)
				require.Nil(t, repo.accounts[1].ProxyID)
				require.Empty(t, repo.bulkUpdates)
			})
		}
	}
}

func TestAccountAccessAndCredentialChangesKeepManualProxy(t *testing.T) {
	id := int64(9)
	for _, bulk := range []bool{false, true} {
		repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
			1: {ID: 1, Platform: PlatformGemini, Type: AccountTypeOAuth, Status: StatusActive, ProxyID: &id,
				Credentials: map[string]any{"oauth_type": "ai_studio"}},
		}}
		svc := accountAdmissionService(&upstreamBillingProbeAdminRepo{repo}, id)
		cache := svc.proxyLatencyCache.(*accountAdmissionCache)
		cache.err = errors.New("cache offline")
		credentials := map[string]any{"project_id": "synthetic-project"}
		if bulk {
			_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Credentials: credentials})
			require.NoError(t, err)
			require.Equal(t, credentials, repo.bulkUpdates[0].Credentials)
		} else {
			_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Credentials: credentials})
			require.NoError(t, err)
			require.Equal(t, "synthetic-project", repo.accounts[1].Credentials["project_id"])
			_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Type: AccountTypeAPIKey})
			require.NoError(t, err)
			require.Equal(t, AccountTypeAPIKey, repo.accounts[1].Type)
		}
		require.Equal(t, &id, repo.accounts[1].ProxyID)
		require.Zero(t, cache.calls)
	}
}

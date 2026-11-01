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
	info *ProxyLatencyInfo
	err  error
}

func (c *accountAdmissionCache) GetProxyLatencies(_ context.Context, ids []int64) (map[int64]*ProxyLatencyInfo, error) {
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

func TestDirectAccountProxyAdmission(t *testing.T) {
	ctx := context.Background()
	id := int64(9)
	for _, tc := range []struct {
		name, platform, kind, oauthType string
		allowed                         bool
	}{
		{"Claude restricted", "anthropic", "oauth", "", false},
		{"OpenAI API allowed", "openai", "apikey", "", true},
		{"ChatGPT blocked", "openai", "oauth", "", false},
		{"Gemini API allowed", "gemini", "apikey", "", true},
		{"Gemini AI Studio allowed", "gemini", "oauth", "ai_studio", true},
		{"Gemini Code Assist blocked", "gemini", "oauth", "code_assist", false},
		{"unmapped base only", "kiro", "oauth", "", true},
		{"Gemini Vertex base only", "gemini", "service_account", "", true},
		{"Claude Bedrock base only", "anthropic", "bedrock", "", true},
		{"unknown AI account type denied", "openai", "unknown", "", false},
		{"unknown Gemini OAuth scope denied", "gemini", "oauth", "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := accountAdmissionService(nil, id)
			account := &Account{Platform: tc.platform, Type: tc.kind, Credentials: map[string]any{"oauth_type": tc.oauthType}}
			if tc.oauthType == "code_assist" {
				account.Credentials["project_id"] = "project"
			}
			err := svc.checkNewAccountProxy(ctx, account, nil, &id)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)
			}
		})
	}
	svc := accountAdmissionService(nil, id)
	cache := svc.proxyLatencyCache.(*accountAdmissionCache)
	account := &Account{Platform: "openai", Type: "apikey"}
	cache.info.QualityAIPlatforms = nil
	require.ErrorIs(t, svc.checkNewAccountProxy(ctx, account, nil, &id), ErrProxyPoolProxyNotInPool)
	account.Platform = "kiro"
	require.NoError(t, svc.checkNewAccountProxy(ctx, account, nil, &id))
	cache.err = errors.New("cache offline")
	require.ErrorIs(t, svc.checkNewAccountProxy(ctx, account, nil, &id), ErrProxyPoolQualityUnavailable)
	require.NoError(t, svc.checkNewAccountProxy(ctx, account, &id, &id), "unchanged binding does not consult cache")
	svc.proxyLatencyCache = nil
	require.ErrorIs(t, svc.checkNewAccountProxy(ctx, account, nil, &id), ErrProxyPoolQualityUnavailable)
}

func TestManualAccountMutationUsesTargetAdmission(t *testing.T) {
	ctx := context.Background()
	id := int64(9)
	for _, tc := range []struct {
		name, platform, kind string
		allowed              bool
	}{
		{"Claude", "anthropic", "oauth", false},
		{"OpenAI API", "openai", "apikey", true},
		{"OpenAI OAuth", "openai", "oauth", false},
		{"Gemini API", "gemini", "apikey", true},
		{"Gemini OAuth", "gemini", "oauth", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
				1: {ID: 1, Platform: tc.platform, Type: tc.kind, Status: StatusActive},
			}}
			credentials := map[string]any{}
			if tc.platform == "gemini" && tc.kind == "oauth" {
				credentials["project_id"] = "project"
			}
			base.accounts[1].Credentials = credentials
			svc := accountAdmissionService(&upstreamBillingProbeAdminRepo{base}, id)
			check := func(err error) {
				t.Helper()
				if tc.allowed {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)
				}
			}
			_, err := svc.CreateAccount(ctx, &CreateAccountInput{
				Platform: tc.platform, Type: tc.kind, Credentials: credentials, ProxyID: &id, SkipDefaultGroupBind: true,
			})
			check(err)
			_, err = svc.UpdateAccount(ctx, 1, &UpdateAccountInput{ProxyID: &id})
			check(err)
			// Restore the original binding so the batch request attaches a new proxy.
			base.accounts[1].ProxyID = nil
			_, err = svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{1}, ProxyID: &id})
			check(err)
			if !tc.allowed {
				require.Empty(t, base.bulkUpdates)
				require.Nil(t, base.accounts[1].ProxyID)
			}
		})
	}
}

func TestManualAccountUnchangedBindingSkipsAdmission(t *testing.T) {
	id := int64(9)
	base := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		1: {ID: 1, Platform: "openai", Type: "oauth", ProxyID: &id, Status: StatusActive},
	}}
	svc := &adminServiceImpl{accountRepo: &upstreamBillingProbeAdminRepo{base}}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Name: "renamed", ProxyID: &id})
	require.NoError(t, err, "existing bindings survive unrelated edits with no quality cache")
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Name: "renamed again", ProxyID: &id})
	require.NoError(t, err)
}

func TestAccountAccessPathChangeRevalidatesExistingProxy(t *testing.T) {
	id := int64(9)
	for _, explicitProxy := range []bool{false, true} {
		base := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
			1: {ID: 1, Platform: "openai", Type: "apikey", ProxyID: &id, Status: StatusActive},
		}}
		svc := accountAdmissionService(&upstreamBillingProbeAdminRepo{base}, id)
		input := &UpdateAccountInput{Type: AccountTypeOAuth}
		if explicitProxy {
			input.ProxyID = &id
		}
		_, err := svc.UpdateAccount(context.Background(), 1, input)
		require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool, "API access does not authorize ChatGPT OAuth on the same proxy")
		require.Equal(t, AccountTypeAPIKey, base.accounts[1].Type)
	}
}

func TestAccountCredentialPathChangeRevalidatesExistingProxy(t *testing.T) {
	id := int64(9)
	for _, tc := range []struct {
		name, platform, kind string
		before, change       map[string]any
		blockedTarget        string
	}{
		{"Gemini add project", "gemini", "oauth", map[string]any{"oauth_type": "ai_studio", "base_url": "https://relay.example"}, map[string]any{"project_id": "project"}, "gemini_code_assist"},
		{"Gemini remove project", "gemini", "oauth", map[string]any{"oauth_type": "code_assist", "project_id": "project"}, map[string]any{"project_id": ""}, "gemini"},
		{"custom relay to Claude API", "anthropic", "apikey", map[string]any{"base_url": "https://relay.example"}, map[string]any{"base_url": "https://api.anthropic.com"}, "anthropic"},
	} {
		for _, bulk := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				base := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
					1: {ID: 1, Platform: tc.platform, Type: tc.kind, ProxyID: &id, Status: StatusActive, Credentials: tc.before},
				}}
				svc := accountAdmissionService(&upstreamBillingProbeAdminRepo{base}, id)
				svc.proxyLatencyCache.(*accountAdmissionCache).info.QualityAIPlatforms = []ProxyAIPlatformAssessment{
					{Target: tc.blockedTarget, Status: "restricted", Eligible: false},
					{Target: "grok", Status: "reachable", Eligible: true},
				}
				if previousTarget := proxyQualityTargetForAccount(base.accounts[1]); previousTarget != "" {
					svc.proxyLatencyCache.(*accountAdmissionCache).info.QualityAIPlatforms = append(
						svc.proxyLatencyCache.(*accountAdmissionCache).info.QualityAIPlatforms,
						ProxyAIPlatformAssessment{Target: previousTarget, Status: "reachable", Eligible: true},
					)
				}
				var err error
				if bulk {
					_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Credentials: tc.change})
				} else {
					_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Credentials: tc.change})
				}
				require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)
				require.Equal(t, tc.before, base.accounts[1].Credentials)
				require.Empty(t, base.bulkUpdates)
			})
		}
	}
}

func TestAccountCredentialMetadataEditKeepsExistingProxy(t *testing.T) {
	id := int64(9)
	base := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		1: {ID: 1, Platform: "openai", Type: "apikey", ProxyID: &id, Status: StatusActive},
	}}
	svc := &adminServiceImpl{accountRepo: &upstreamBillingProbeAdminRepo{base}}
	metadata := map[string]any{"model_mapping": map[string]any{"alias": "gpt-model"}}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Credentials: metadata})
	require.NoError(t, err, "same-target metadata edit does not require fresh proxy evidence")
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Credentials: metadata})
	require.NoError(t, err)
}

func TestAccountCreationRequiresActualForwardingSurface(t *testing.T) {
	for _, tc := range []struct {
		name, platform, target string
		credentials            map[string]any
	}{
		{"Studio project ignores custom relay", "gemini", "gemini_code_assist", map[string]any{"oauth_type": "ai_studio", "project_id": " project ", "base_url": "https://relay.example/v1"}},
		{"nominal Code Assist without project uses Gemini", "gemini", "gemini", map[string]any{"oauth_type": "code_assist"}},
		{"nominal Code Assist without project respects custom relay", "gemini", "", map[string]any{"oauth_type": "code_assist", "base_url": "https://relay.example/v1"}},
		{"malformed Grok override falls back to official API", "grok", "grok", map[string]any{"base_url": "not-a-url"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidenceTargets := []string{"openai"}
			if tc.platform == "gemini" {
				evidenceTargets[0] = "gemini"
				if tc.target == "gemini" {
					evidenceTargets[0] = "gemini_code_assist"
				}
			}
			if tc.target != "" {
				evidenceTargets = append(evidenceTargets, tc.target)
			}
			for _, evidenceTarget := range evidenceTargets {
				t.Run(evidenceTarget, func(t *testing.T) {
					id := int64(9)
					repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{}}
					svc := accountAdmissionService(&upstreamBillingProbeAdminRepo{repo}, id)
					svc.proxyLatencyCache.(*accountAdmissionCache).info.QualityAIPlatforms = []ProxyAIPlatformAssessment{
						{Target: evidenceTarget, Status: "reachable", Eligible: true},
					}
					account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
						Platform: tc.platform, Type: "oauth", Credentials: tc.credentials, ProxyID: &id, SkipDefaultGroupBind: true,
					})
					if tc.target == "" || evidenceTarget == tc.target {
						require.NoError(t, err)
						require.Equal(t, id, *account.ProxyID)
					} else {
						require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)
						require.Empty(t, repo.accounts)
					}
				})
			}
		})
	}
}

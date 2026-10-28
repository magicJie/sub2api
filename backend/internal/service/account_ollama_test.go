//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOllamaAccountOpenAIContract(t *testing.T) {
	var nilAccount *Account
	require.False(t, nilAccount.IsOpenAI())
	account := &Account{Platform: PlatformOllama, Type: AccountTypeAPIKey, Extra: map[string]any{
		"openai_passthrough":               true,
		openAILongContextBillingEnabledKey: true,
		featureKeyWebSearchEmulation:       WebSearchModeEnabled,
	}}
	require.True(t, account.IsOpenAI())
	require.True(t, account.IsOpenAIApiKey())
	require.True(t, account.IsOpenAICompatible())
	require.True(t, account.IsOpenAIPassthroughEnabled())
	require.True(t, account.IsOpenAILongContextBillingEnabled())
	require.True(t, account.IsHeaderOverrideEligible())
	require.True(t, IsUpstreamBillingProbeIdentity(PlatformOllama, AccountTypeAPIKey))
	require.Equal(t, DefaultOllamaBaseURL, account.GetOpenAIBaseURL())
	require.Equal(t, WebSearchModeEnabled, account.GetWebSearchEmulationMode())
	require.False(t, account.IsMultiProtocolAPIKey())
	require.False(t, account.RoutesProtocolByInbound())
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		account.Type = accountType
		require.False(t, account.IsOpenAIOAuth())
		require.False(t, account.IsOpenAIOAuthLike())
		if accountType != AccountTypeAPIKey {
			require.False(t, account.IsOpenAI())
		}
	}
}

func TestOllamaAccountValidationAndDefaults(t *testing.T) {
	for _, baseURL := range []string{"", " https://ollama.com/v1 ", "https://ollama.com", "https://ollama.com/", "https://OLLAMA.COM:443/v1/"} {
		t.Run(baseURL, func(t *testing.T) {
			account, err := buildAccountForCreate(&CreateAccountInput{
				Platform: PlatformOllama, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": baseURL, "api_key": "key"},
			}, nil)
			require.NoError(t, err)
			if baseURL == "" {
				require.Equal(t, DefaultOllamaBaseURL, account.GetCredential("base_url"))
			}
			require.Equal(t, strings.TrimSpace(account.GetCredential("base_url")), account.GetCredential("base_url"))
		})
	}
	for _, baseURL := range []string{"https://www.ollama.com/v1", "https://ollama.com.evil.test", "http://ollama.com", "https://ollama.com:444", "https://user@ollama.com", "https://ollama.com/v2", "https://ollama.com?next=evil", "https://ollama.com#x"} {
		_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOllama, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": baseURL}}, nil)
		require.Error(t, err, baseURL)
	}
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeUpstream} {
		_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOllama, Type: accountType}, nil)
		require.ErrorContains(t, err, "only support apikey")
	}
}

func TestOllamaAdminUpdateRejectsCredentialAndTypeChanges(t *testing.T) {
	for _, input := range []*UpdateAccountInput{
		{Type: AccountTypeOAuth},
		{Credentials: map[string]any{"base_url": "https://relay.example.com"}},
	} {
		repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOllama, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "key"}}}
		svc := &adminServiceImpl{accountRepo: repo}
		_, err := svc.UpdateAccount(context.Background(), 1, input)
		require.Error(t, err)
	}
	repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOllama, Type: AccountTypeAPIKey}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Credentials: map[string]any{"base_url": "https://relay.example.com"}})
	require.Error(t, err)
	require.Zero(t, repo.bulkUpdateCalls)
}

func TestOllamaGroupKeepsOpenAIConfiguration(t *testing.T) {
	group := &Group{Platform: PlatformOllama, AllowMessagesDispatch: true, DefaultMappedModel: "mapped", MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{OpusMappedModel: "custom-opus"}, MaxReasoningEffort: "high", MaxReasoningEffortOverLimit: ReasoningEffortOverLimitDeny}
	sanitizeGroupMessagesDispatchFields(group)
	sanitizeGroupReasoningEffortPolicy(group)
	require.True(t, group.AllowMessagesDispatch)
	require.Equal(t, "mapped", group.DefaultMappedModel)
	require.Equal(t, "custom-opus", group.ResolveMessagesDispatchModel("claude-opus-4"))
	require.Equal(t, "high", group.MaxReasoningEffort)
	require.Equal(t, ReasoningEffortOverLimitDeny, group.MaxReasoningEffortOverLimit)
	require.True(t, groupSupportsOpenAIFast(PlatformOllama))
	require.False(t, groupSupportsOAuthOnlyFilter(PlatformOllama))
	require.Equal(t, defaultModelsListCandidateIDs(PlatformOpenAI), defaultModelsListCandidateIDs(PlatformOllama))
	manifest := normalizeCodexModelsManifestConfig(PlatformOllama, GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{1}})
	require.True(t, manifest.Enabled)
	require.Equal(t, []int64{1}, manifest.AccountIDs)
}

func TestOllamaConnectionProbeUsesChatCompletions(t *testing.T) {
	ctx, recorder := newTestContext()
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"pong\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")),
	}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}}
	account := &Account{ID: 1, Platform: PlatformOllama, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "ollama-key", "model_mapping": map[string]any{"alias": "deepseek-v3"}}}
	err := svc.testOpenAIAccountConnection(ctx, account, "alias", "hello", "")
	require.NoError(t, err)
	require.Equal(t, "https://ollama.com/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer ollama-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "deepseek-v3", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"success":true`)
}

func TestOllamaLongContextBillingConfigurationMatchesOpenAI(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformOllama} {
		extra, err := normalizeOpenAILongContextBillingExtra(platform, nil)
		require.NoError(t, err)
		require.Equal(t, false, extra[openAILongContextBillingEnabledKey])
		_, err = normalizeOpenAILongContextBillingExtra(platform, map[string]any{openAILongContextBillingEnabledKey: "true"})
		require.Error(t, err)
		account := &Account{Platform: platform, Type: AccountTypeAPIKey, Extra: map[string]any{openAILongContextBillingEnabledKey: true}}
		updated, err := normalizeOpenAILongContextBillingUpdateExtra(account, &UpdateAccountInput{Extra: map[string]any{}})
		require.NoError(t, err)
		require.Equal(t, true, updated[openAILongContextBillingEnabledKey])
		_, err = validateBulkOpenAISettingsTargets(&BulkUpdateAccountsInput{AccountIDs: []int64{1}}, bulkOpenAISettings{longContextBilling: true, endpointCapabilities: true}, map[int64]*Account{1: account})
		require.NoError(t, err)
	}
	price := 0.000003
	entry := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"deepseek-v3"}, InputPrice: &price}
	oldGroup := &Group{Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{entry}}
	migratedGroup := &Group{Platform: PlatformOllama, ModelPricing: []ChannelModelPricing{entry}}
	require.Equal(t, matchGroupModelPricing(oldGroup, "deepseek-v3"), matchGroupModelPricing(migratedGroup, "deepseek-v3"))
	require.True(t, profitControlPlatformSupported(PlatformOllama))
}

func TestCRSSyncPreservesMigratedOllamaPlatformAndUsage(t *testing.T) {
	for _, collection := range []string{"openaiResponsesAccounts", "claudeConsoleAccounts"} {
		t.Run(collection, func(t *testing.T) {
			existing := &Account{ID: 41, Platform: PlatformOllama, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "key", "base_url": DefaultOllamaBaseURL}, Extra: map[string]any{
				"crs_account_id":                    "crs-openai-1",
				OllamaCloudUsageSessionExtraKey:     "local-session",
				OllamaCloudUsageAutoRefreshExtraKey: true,
			}}
			repo := newCRSLongContextAccountRepo(existing)
			result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{collection: collection, credentials: map[string]any{"api_key": "key", "base_url": DefaultOllamaBaseURL}})
			require.Equal(t, 1, result.Updated)
			require.Equal(t, PlatformOllama, existing.Platform)
			require.Equal(t, "local-session", existing.Extra[OllamaCloudUsageSessionExtraKey])
			require.Equal(t, true, existing.Extra[OllamaCloudUsageAutoRefreshExtraKey])
		})
	}
}

func TestOllamaMappedModelPricingAndCostMatchesLegacyOpenAI(t *testing.T) {
	billing := newTestBillingServiceWithOpenAILadderCatalog(t)
	resolver := NewModelPricingResolver(nil, billing)
	for _, customGroupPrices := range []bool{false, true} {
		var legacyPricing *ModelPricing
		var legacyCost *CostBreakdown
		for _, platform := range []string{PlatformOpenAI, PlatformOllama} {
			account := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"model_mapping": map[string]any{"client-model": "gpt-5.4"},
			}}
			model := account.GetMappedModel("client-model")
			require.Equal(t, "gpt-5.4", model)
			pricing, err := billing.GetModelPricing(model)
			require.NoError(t, err)
			require.InDelta(t, 2.5e-6, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, 15e-6, pricing.OutputPricePerToken, 1e-12)
			group := &Group{Platform: platform, RateMultiplier: 1.5}
			expectedTotal := 0.01
			if customGroupPrices {
				inputPrice, outputPrice := 3e-6, 6e-6
				// Migration retains existing OpenAI price cards verbatim.
				group.ModelPricing = []ChannelModelPricing{{
					Platform: PlatformOpenAI, Models: []string{model}, BillingMode: BillingModeToken,
					InputPrice: &inputPrice, OutputPrice: &outputPrice,
				}}
				expectedTotal = 0.006
			}
			longContextEnabled := account.IsOpenAILongContextBillingEnabled()
			cost, err := billing.CalculateCostUnified(CostInput{
				Ctx: context.Background(), Model: model, Group: group,
				Tokens:         UsageTokens{InputTokens: 1000, OutputTokens: 500},
				RateMultiplier: group.RateMultiplier, Resolver: resolver,
				LongContextBillingEnabled: &longContextEnabled,
			})
			require.NoError(t, err)
			require.InDelta(t, expectedTotal, cost.TotalCost, 1e-12)
			require.InDelta(t, expectedTotal*1.5, cost.ActualCost, 1e-12)
			if platform == PlatformOpenAI {
				legacyPricing, legacyCost = pricing, cost
			} else {
				require.Equal(t, legacyPricing, pricing)
				require.Equal(t, legacyCost, cost)
			}
		}
	}
}

func TestOllamaCodexCatalogRetainsConfiguredAndPinnedModels(t *testing.T) {
	const groupID int64 = 14
	account := Account{ID: 58, Platform: PlatformOllama, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "local-key", "base_url": DefaultOllamaBaseURL, "model_mapping": map[string]any{"public-alias": "deepseek-v4.1-flash"}}}
	repo := splitCodexModelsAccountRepo{
		schedulable: map[int64][]Account{groupID: {account}},
		catalog:     map[int64][]Account{groupID: {account}},
		all:         map[int64][]Account{groupID: {account}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"deepseek-v4.1-flash"}]}`))}}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	group := &Group{ID: groupID, Platform: PlatformOllama}
	local, configured, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), group, "")
	require.NoError(t, err)
	require.True(t, configured)
	require.Equal(t, []string{"public-alias"}, codexManifestModelSlugs(t, local.Body))
	group.CodexModelsManifestConfig = GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{account.ID}}
	pinned, selected, err := svc.FetchPinnedCodexModelsManifest(context.Background(), group, "0.55.0")
	require.NoError(t, err)
	require.Equal(t, account.ID, selected.ID)
	require.Equal(t, []string{"public-alias"}, codexManifestModelSlugs(t, pinned.Body))
}

func TestOllamaProfitControlRejectsOverThresholdAccount(t *testing.T) {
	group := profitControlTestGroup(14, 0.3, 0)
	group.Platform = PlatformOllama
	svc := &OpenAIGatewayService{}
	ctx := svc.withOpenAIProfitControlGate(profitControlTestCtx(group), &group.ID)
	account := profitControlTestAccountWithRate(&Account{Platform: PlatformOllama, Type: AccountTypeAPIKey}, 2)
	rejected, reason := openAIProfitControlVetoReason(ctx, account)
	require.True(t, rejected)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
}

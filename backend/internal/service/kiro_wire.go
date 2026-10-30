package service

// Kiro（fork）与上游代码的胶水层。
//
// 约定：Kiro 逻辑只放在 kiro_*.go；上游文件只允许新增字段 / 嵌入结构体或一行转发调用。
// 本文件集中放置这些转发调用的实现、嵌入到上游结构体的类型，以及 wire 依赖注入。

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	kirocontinuation "github.com/Wei-Shaw/sub2api/internal/kiro/continuation"
	kirocooldown "github.com/Wei-Shaw/sub2api/internal/kiro/cooldown"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// 嵌入到上游结构体的字段组
// ---------------------------------------------------------------------------

// KiroAccountRuntime 是嵌入 Account 的 Kiro 用量 / 运行时派生状态（非持久化）。
type KiroAccountRuntime struct {
	KiroQuotaState     string
	KiroQuotaReason    string
	KiroQuotaResetAt   *time.Time
	KiroRuntimeState   string
	KiroRuntimeReason  string
	KiroRuntimeResetAt *time.Time
}

// KiroCreditProgress 表示 Kiro 主额度或 Bonus 的用量进度。
type KiroCreditProgress struct {
	CurrentUsage   float64    `json:"current_usage"`
	UsageLimit     float64    `json:"usage_limit"`
	PercentageUsed float64    `json:"percentage_used"`
	DaysRemaining  int        `json:"days_remaining,omitempty"`
	ExpiryDate     *time.Time `json:"expiry_date,omitempty"`
	// WindowStats 为本地 usage_logs 在当前账期内的请求/token/费用统计（仅主额度填充）。
	WindowStats *WindowStats `json:"window_stats,omitempty"`
}

// KiroOverageInfo 表示 Kiro 账号的 overage 状态。
type KiroOverageInfo struct {
	CurrentOverages float64 `json:"current_overages"`
	OverageCharges  float64 `json:"overage_charges"`
	CurrencyCode    string  `json:"currency_code,omitempty"`
	CurrencySymbol  string  `json:"currency_symbol,omitempty"`
}

// KiroUsageInfo 是嵌入 UsageInfo 的 Kiro 额度字段（JSON 平铺到 UsageInfo）。
type KiroUsageInfo struct {
	KiroSubscriptionName string              `json:"kiro_subscription_name,omitempty"`
	KiroSubscriptionType string              `json:"kiro_subscription_type,omitempty"`
	KiroResetAt          *time.Time          `json:"kiro_reset_at,omitempty"`
	KiroOveragesEnabled  bool                `json:"kiro_overages_enabled,omitempty"`
	KiroCredit           *KiroCreditProgress `json:"kiro_credit,omitempty"`
	KiroBonus            *KiroCreditProgress `json:"kiro_bonus,omitempty"`
	KiroOverage          *KiroOverageInfo    `json:"kiro_overage,omitempty"`
	KiroQuotaState       string              `json:"kiro_quota_state,omitempty"`
	KiroQuotaReason      string              `json:"kiro_quota_reason,omitempty"`
	KiroQuotaResetAt     *time.Time          `json:"kiro_quota_reset_at,omitempty"`
	KiroRuntimeState     string              `json:"kiro_runtime_state,omitempty"`
	KiroRuntimeReason    string              `json:"kiro_runtime_reason,omitempty"`
	KiroRuntimeResetAt   *time.Time          `json:"kiro_runtime_reset_at,omitempty"`
	// KiroUsagePending 表示还没有落库快照（或已排进"全部刷新"队列），后台刷新器稍后会补上。
	KiroUsagePending bool `json:"kiro_usage_pending,omitempty"`
	// KiroUsageLastAttemptAt 是后台最近一次尝试拉取的时间（成功或失败）；UpdatedAt 是最近一次成功。
	KiroUsageLastAttemptAt *time.Time `json:"kiro_usage_last_attempt_at,omitempty"`
}

// KiroOpsDiagnostics 是嵌入 OpsUpstreamErrorEvent 的模型诊断字段。
type KiroOpsDiagnostics struct {
	RequestedModel      string `json:"requested_model,omitempty"`
	MappedModel         string `json:"mapped_model,omitempty"`
	KiroModelID         string `json:"kiro_model_id,omitempty"`
	HasTools            bool   `json:"has_tools,omitempty"`
	HasAdaptiveThinking bool   `json:"has_adaptive_thinking,omitempty"`
	HasContext1MBeta    bool   `json:"has_context_1m_beta,omitempty"`
}

// kiroUsageCache 缓存 Kiro 额度快照。
type kiroUsageCache struct {
	usageInfo *UsageInfo
	timestamp time.Time
}

// kiroUsageErrorTTL 是 Kiro 用量查询可恢复错误的负缓存 TTL。
const kiroUsageErrorTTL = 1 * time.Minute

// ---------------------------------------------------------------------------
// 上游文件中一行转发调用的实现
// ---------------------------------------------------------------------------

// kiroResolveModelMapping 处理 Kiro 账号的模型映射；非 Kiro 账号返回 ok=false。
func kiroResolveModelMapping(a *Account, rawMapping map[string]any) (map[string]string, bool) {
	if a == nil || a.Platform != PlatformKiro {
		return nil, false
	}
	result := make(map[string]string, len(rawMapping))
	for k, v := range rawMapping {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	if len(result) == 0 {
		return domainDefaultKiroModelMapping, true
	}
	normalizeKiroClaude5Mappings(result)
	return result, true
}

func normalizeKiroClaude5Mappings(mapping map[string]string) {
	for model, canonical := range map[string]string{
		"claude-opus-5-0":            "claude-opus-5",
		"claude-opus-5-0-thinking":   "claude-opus-5",
		"claude-opus-5.0":            "claude-opus-5",
		"claude-opus-5":              "claude-opus-5",
		"claude-opus-5-thinking":     "claude-opus-5",
		"claude-sonnet-5-0":          "claude-sonnet-5",
		"claude-sonnet-5-0-thinking": "claude-sonnet-5",
		"claude-sonnet-5.0":          "claude-sonnet-5",
		"claude-sonnet-5":            "claude-sonnet-5",
		"claude-sonnet-5-thinking":   "claude-sonnet-5",
	} {
		if _, exists := mapping[model]; exists {
			mapping[model] = canonical
		}
	}
	for legacyModel, clientModel := range map[string]string{
		"claude-opus-5-0":            "claude-opus-5",
		"claude-opus-5-0-thinking":   "claude-opus-5-thinking",
		"claude-opus-5.0":            "claude-opus-5",
		"claude-sonnet-5-0":          "claude-sonnet-5",
		"claude-sonnet-5-0-thinking": "claude-sonnet-5-thinking",
		"claude-sonnet-5.0":          "claude-sonnet-5",
	} {
		if _, legacyExists := mapping[legacyModel]; legacyExists {
			if _, clientExists := mapping[clientModel]; !clientExists {
				mapping[clientModel] = strings.TrimSuffix(clientModel, "-thinking")
			}
		}
	}
}

// getKiroUsageForAccount 是 AccountUsageService.getUsageForAccount 的 Kiro 分支。
//
// 后台刷新开启时（见 kiro_usage_refresher.go）：不带 force 只读落库快照、不打上游；
// 带 force（单号手动刷新）同步拉一次并落库。关闭时保持按需拉取的旧行为。
func (s *AccountUsageService) getKiroUsageForAccount(ctx context.Context, account *Account, forceProbe bool) (*UsageInfo, error) {
	if s.kiroUsageSnapshotMode() && account != nil && account.Type == AccountTypeOAuth {
		if !forceProbe {
			usage := s.readKiroUsageSnapshot(ctx, account)
			s.attachKiroCreditWindowStats(ctx, account, usage, time.Now())
			return usage, nil
		}
		usage, _, err := s.refreshKiroUsageSnapshot(ctx, account)
		if err != nil && usage == nil {
			return nil, err
		}
		if err != nil && !errors.Is(err, ErrKiroUsageSnapshotStale) {
			slog.Warn("kiro_usage_snapshot_persist_failed", "account_id", account.ID, "error", err)
		}
		s.attachKiroCreditWindowStats(ctx, account, usage, time.Now())
		return usage, nil
	}
	usage, err := s.getKiroUsage(ctx, account, "active", forceProbe)
	if err == nil {
		s.tryClearRecoverableAccountError(ctx, account)
		s.attachKiroCreditWindowStats(ctx, account, usage, time.Now())
	}
	return usage, err
}

// attachKiroCreditWindowStats 为 Kiro 主额度附加当前账期内的本地用量统计，
// 账期起点取 kiro_reset_at 往前一个月；重置时间未知时退化为最近 30 天。
func (s *AccountUsageService) attachKiroCreditWindowStats(ctx context.Context, account *Account, usage *UsageInfo, now time.Time) {
	if s == nil || s.usageLogRepo == nil || account == nil || usage == nil || usage.KiroCredit == nil {
		return
	}
	start := kiroCreditWindowStart(usage.KiroResetAt, now)
	stats, err := s.usageLogRepo.GetAccountWindowStats(ctx, account.ID, start)
	if err != nil {
		slog.Warn("kiro_credit_window_stats_query_failed", "account_id", account.ID, "window_start", start, "error", err)
		return
	}
	// cloneUsageInfo 是浅拷贝，复制一份 KiroCredit，避免把统计写进缓存快照。
	credit := *usage.KiroCredit
	credit.WindowStats = windowStatsFromAccountStats(stats)
	usage.KiroCredit = &credit
}

func kiroCreditWindowStart(resetAt *time.Time, now time.Time) time.Time {
	if resetAt != nil && resetAt.After(now) {
		if start := resetAt.AddDate(0, -1, 0); !start.After(now) {
			return start
		}
	}
	return now.Add(-30 * 24 * time.Hour)
}

// isKiroRuntimeSchedulable 检查 Kiro 直连账号是否处于活跃冷却状态。
// 非 Kiro 平台或未配置冷却存储时始终视为可调度。
func (s *GatewayService) isKiroRuntimeSchedulable(ctx context.Context, account *Account) bool {
	if !isKiroDirectModeAccount(account) || s == nil || s.kiroCooldownStore == nil {
		return true
	}
	state, err := s.getKiroCooldownState(ctx, buildKiroAccountKey(account))
	if err != nil {
		return true
	}
	return state == nil || !state.Active
}

// tryRecoverKiroCooldownPoolCtx 包装 tryRecoverKiroCooldownPool：成功清除一个冷却时
// 返回带“已尝试”标记的 ctx，供调度器重试一次。
func (s *GatewayService) tryRecoverKiroCooldownPoolCtx(ctx context.Context, accounts []Account, requestedModel string, excludedIDs map[int64]struct{}, allowMixed bool) (context.Context, bool) {
	if !s.tryRecoverKiroCooldownPool(ctx, accounts, requestedModel, excludedIDs, allowMixed) {
		return ctx, false
	}
	return context.WithValue(ctx, kiroCooldownRecoveryAttemptedKey, true), true
}

type kiroBillingAccountKeyType struct{}

var kiroBillingAccountKey = kiroBillingAccountKeyType{}

// withKiroBillingAccount 在计费 ctx 上标记本次用量来自 Kiro 账号，
// 供 calculateTokenCost 的定价失败兜底使用（避免修改上游计费函数签名）。
func withKiroBillingAccount(ctx context.Context, account *Account) context.Context {
	if account == nil || account.Platform != PlatformKiro {
		return ctx
	}
	return context.WithValue(ctx, kiroBillingAccountKey, true)
}

// kiroConservativeCostOnPricingError 在 Kiro 上游返回 auto 等无法定价的模型名时，
// 按保守兜底模型计费，避免漏计费。非 Kiro 请求返回 nil。
func (s *GatewayService) kiroConservativeCostOnPricingError(ctx context.Context, result *ForwardResult, tokens UsageTokens, multiplier float64) *CostBreakdown {
	if ctx == nil || ctx.Value(kiroBillingAccountKey) != true || result == nil {
		return nil
	}
	modelHint := result.UpstreamModel
	if modelHint == "" {
		modelHint = result.Model
	}
	return s.calculateKiroConservativeTokenCost(tokens, multiplier, modelHint)
}

// SetKiroOAuthService 注册 Kiro 平台的后台 token 刷新；必须在 Start() 之前调用。
func (s *TokenRefreshService) SetKiroOAuthService(kiroOAuthService *KiroOAuthService) {
	if kiroOAuthService == nil {
		return
	}
	refresher := NewKiroTokenRefresher(kiroOAuthService)
	s.registrations = append(s.registrations, tokenRefreshRegistration{platform: PlatformKiro, refresher: refresher, executor: refresher})
}

// ---------------------------------------------------------------------------
// wire 依赖注入
// ---------------------------------------------------------------------------

// KiroRuntimeBinding 是把 Kiro 依赖注入到上游服务的副作用标记。
// admin.NewKiroOAuthHandler 依赖它，从而保证 wire 会构造它（上游构造函数无需感知）。
type KiroRuntimeBinding struct{}

// ProvideKiroTokenProvider creates KiroTokenProvider with OAuthRefreshAPI injection.
func ProvideKiroTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	kiroOAuthService *KiroOAuthService,
	refreshAPI *OAuthRefreshAPI,
) *KiroTokenProvider {
	p := NewKiroTokenProvider(accountRepo, tokenCache, kiroOAuthService)
	p.SetRefreshAPI(refreshAPI, NewKiroTokenRefresher(kiroOAuthService))
	p.SetRefreshPolicy(GeminiProviderRefreshPolicy())
	return p
}

// ProvideKiroCooldownStore creates the Redis-backed Kiro cooldown store.
func ProvideKiroCooldownStore(redisClient *redis.Client) KiroCooldownStore {
	return kirocooldown.NewStore(redisClient)
}

// ProvideKiroContinuationStore creates the Redis-backed Kiro agent state cache.
func ProvideKiroContinuationStore(redisClient *redis.Client) KiroContinuationStore {
	return kirocontinuation.NewStore(redisClient)
}

// ProvideKiroRuntimeBinding 把 Kiro 运行时依赖注入到已构造的上游服务。
func ProvideKiroRuntimeBinding(
	gatewayService *GatewayService,
	accountUsageService *AccountUsageService,
	accountTestService *AccountTestService,
	kiroTokenProvider *KiroTokenProvider,
	kiroCooldownStore KiroCooldownStore,
	kiroContinuationStore KiroContinuationStore,
) KiroRuntimeBinding {
	gatewayService.SetKiroTokenProvider(kiroTokenProvider)
	gatewayService.SetKiroCooldownStore(kiroCooldownStore)
	gatewayService.SetKiroContinuationStore(kiroContinuationStore)
	accountUsageService.SetKiroCooldownStore(kiroCooldownStore)
	kiroPaceRefresher.Store(accountUsageService)
	accountTestService.SetKiroTokenProvider(kiroTokenProvider)
	return KiroRuntimeBinding{}
}

// SetKiroTokenProvider 注入 Kiro token provider（nil 时 Kiro OAuth 账号无法自动刷新 token）。
func (s *GatewayService) SetKiroTokenProvider(provider *KiroTokenProvider) *GatewayService {
	if s != nil {
		s.kiroTokenProvider = provider
	}
	return s
}

// SetKiroCooldownStore 注入 Kiro 冷却限流存储（nil 时 Kiro 转发跳过冷却检查）。
func (s *GatewayService) SetKiroCooldownStore(store KiroCooldownStore) *GatewayService {
	if s != nil {
		s.kiroCooldownStore = store
	}
	return s
}

// SetKiroContinuationStore 注入 Kiro agent 状态缓存（nil 时保持无状态重放）。
func (s *GatewayService) SetKiroContinuationStore(store KiroContinuationStore) *GatewayService {
	if s != nil {
		s.kiroContinuationStore = store
	}
	return s
}

// SetKiroCooldownStore 注入 Kiro 冷却限流存储（nil 时用量查询不展示运行时状态）。
func (s *AccountUsageService) SetKiroCooldownStore(store KiroCooldownStore) *AccountUsageService {
	if s != nil {
		s.kiroCooldownStore = store
	}
	return s
}

// SetKiroTokenProvider 注入 Kiro token provider。
func (s *AccountTestService) SetKiroTokenProvider(provider *KiroTokenProvider) {
	s.kiroTokenProvider = provider
}

// KiroProviderSet 是 Kiro 的 wire 依赖集合，挂在 service.ProviderSet 中。
var KiroProviderSet = wire.NewSet(
	NewKiroOAuthService,
	ProvideKiroTokenProvider,
	ProvideKiroUsageRefreshService,
	NewKiroRetireService,
	ProvideKiroCooldownStore,
	ProvideKiroContinuationStore,
	ProvideKiroRuntimeBinding,
)

// domainDefaultKiroModelMapping 是 Kiro 平台的默认模型映射。
// 键为对外暴露/允许请求的模型名，值为实际发送到 Kiro 上游的模型名。
var domainDefaultKiroModelMapping = map[string]string{
	"claude-opus-5-5":                     "claude-opus-5.5",
	"claude-opus-5-5-thinking":            "claude-opus-5.5",
	"claude-sonnet-5-5":                   "claude-sonnet-5.5",
	"claude-sonnet-5-5-thinking":          "claude-sonnet-5.5",
	"claude-opus-5":                       "claude-opus-5",
	"claude-opus-5-thinking":              "claude-opus-5",
	"claude-opus-5-0":                     "claude-opus-5",
	"claude-opus-5-0-thinking":            "claude-opus-5",
	"claude-opus-4-8":                     "claude-opus-4.8",
	"claude-opus-4-8-thinking":            "claude-opus-4.8",
	"claude-opus-4-7":                     "claude-opus-4.7",
	"claude-opus-4-7-thinking":            "claude-opus-4.7",
	"claude-sonnet-5":                     "claude-sonnet-5",
	"claude-sonnet-5-thinking":            "claude-sonnet-5",
	"claude-sonnet-5-0":                   "claude-sonnet-5",
	"claude-sonnet-5-0-thinking":          "claude-sonnet-5",
	"claude-opus-4-6":                     "claude-opus-4.6",
	"claude-opus-4-6-thinking":            "claude-opus-4.6",
	"claude-sonnet-4-6":                   "claude-sonnet-4.6",
	"claude-sonnet-4-6-thinking":          "claude-sonnet-4.6",
	"claude-opus-4-5-20251101":            "claude-opus-4.5",
	"claude-opus-4-5-20251101-thinking":   "claude-opus-4.5",
	"claude-sonnet-4-5-20250929":          "claude-sonnet-4.5",
	"claude-sonnet-4-5-20250929-thinking": "claude-sonnet-4.5",
	"claude-haiku-4-5-20251001":           "claude-haiku-4.5",
	"claude-haiku-4-5-20251001-thinking":  "claude-haiku-4.5",
	// 实验性：只确认了请求侧 modelId 直通不会被拦截，响应语义未经真实账号验证。
	"gpt-5.6-sol":   "gpt-5.6-sol",
	"gpt-5.6-terra": "gpt-5.6-terra",
	"gpt-5.6-luna":  "gpt-5.6-luna",
}

// kiroPreservedCredentialKeys 是 Kiro 账号的身份与设备字段：更新凭据时调用方没带上就沿用旧值。
// 管理端编辑、接口只改 model_mapping 等部分提交都不应丢掉它们（丢 profile_arn 会让用量查询 403，
// 丢 machine_id 会改变设备指纹）。要清除某个字段需显式传空字符串。
var kiroPreservedCredentialKeys = []string{
	"email", "profile_arn", "machine_id", "auth_method", "provider", "region", "start_url",
	"client_id", "client_secret", "client_id_hash", "expires_at", "_token_version",
}

// kiroPreserveCredentialFields 是 adminServiceImpl.UpdateAccount 合并凭据后的 Kiro 分支。
func kiroPreserveCredentialFields(account *Account, existing map[string]any) {
	if account == nil || account.Platform != PlatformKiro || account.Credentials == nil {
		return
	}
	for _, key := range kiroPreservedCredentialKeys {
		if _, provided := account.Credentials[key]; provided {
			continue
		}
		if value, ok := existing[key]; ok {
			account.Credentials[key] = value
		}
	}
}

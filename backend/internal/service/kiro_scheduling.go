package service

import (
	"context"
	"strings"
	"time"

	kirocooldown "github.com/Wei-Shaw/sub2api/internal/kiro/cooldown"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// kiroConservativeFallbackBillingModel 是 Kiro 账号在上游返回 auto 等无法定价模型时
// 使用的保守计费兜底模型（费用取该模型的价格，避免漏计费）。默认按 Claude 家族计费。
const kiroConservativeFallbackBillingModel = "claude-opus-4-6"

// kiroConservativeFallbackBillingModelGPT 是请求的模型明显属于 GPT 家族时使用的保守
// 计费兜底模型（Kiro 近期新增的 OpenAI GPT-5.6 代理模型，sol 为该系列中价格最高档，
// 与 Claude 侧选用 Opus 档位同理，避免因家族误配导致漏计费）。
const kiroConservativeFallbackBillingModelGPT = "gpt-5.6-sol"

// kiroCooldownPoolMaxWait 是账号池全部处于 429 冷却时，等待最早恢复账号的最长时间；
// 超过该时长则直接清除最早到期的冷却。
const kiroCooldownPoolMaxWait = 10 * time.Second

type kiroCooldownRecoveryAttemptedKeyType struct{}

// kiroCooldownRecoveryAttemptedKey 用于在单次请求的重试链路中标记"已经尝试过冷却池恢复"，
// 避免同一请求在多次账号切换重试时重复触发冷却池恢复逻辑。
var kiroCooldownRecoveryAttemptedKey = kiroCooldownRecoveryAttemptedKeyType{}

// tryRecoverKiroCooldownPool 在候选 Kiro 账号池全部处于瞬时(429)冷却时，先等待最早到期的
// 账号恢复（最多 kiroCooldownPoolMaxWait），再清除其冷却状态以恢复调度，避免因短暂限流
// 导致整个分组直接返回 429。
//
// 已接入 gateway_scheduling.go 的 SelectAccountWithLoadAwareness 候选筛选兜底路径
// （候选为空时触发一次恢复重试），并配合 isKiroRuntimeSchedulable 在候选筛选阶段跳过
// 处于活跃冷却状态的 Kiro 账号。
func (s *GatewayService) tryRecoverKiroCooldownPool(ctx context.Context, accounts []Account, requestedModel string, excludedIDs map[int64]struct{}, allowMixedScheduling bool) bool {
	if s == nil || s.kiroCooldownStore == nil || ctx.Value(kiroCooldownRecoveryAttemptedKey) == true {
		return false
	}
	tokenKeys, earliest := s.kiroTransientCooldownRecoveryKeys(ctx, accounts, requestedModel, excludedIDs, allowMixedScheduling)
	if len(tokenKeys) == 0 {
		return false
	}
	if wait := min(earliest, kiroCooldownPoolMaxWait); wait > 0 {
		if err := kiroRetrySleep(ctx, wait); err != nil {
			return false
		}
	}
	cleared, err := s.kiroCooldownStore.ClearEarliestTransientCooldown(ctx, tokenKeys)
	if err != nil {
		logger.LegacyPrintf("service.gateway", "Kiro cooldown pool recovery failed: %v", err)
		return false
	}
	if cleared {
		logger.LegacyPrintf("service.gateway", "Kiro cooldown pool recovery cleared one transient cooldown")
	}
	// 等待期间最早的冷却可能已自然到期（无可清除项），此时同样可以重新调度。
	return cleared || earliest <= kiroCooldownPoolMaxWait
}

// kiroTransientCooldownRecoveryKeys 收集候选账号中"全部处于 429 瞬时冷却"场景下可供
// ClearEarliestTransientCooldown 尝试恢复的 token key 列表，以及其中最早到期的剩余冷却时长；
// 只要出现任一账号不满足全员瞬时冷却的条件，就返回空列表（放弃本轮恢复，交由正常调度/冷却过期处理）。
func (s *GatewayService) kiroTransientCooldownRecoveryKeys(ctx context.Context, accounts []Account, requestedModel string, excludedIDs map[int64]struct{}, allowMixedScheduling bool) ([]string, time.Duration) {
	tokenKeys := make([]string, 0, len(accounts))
	eligible := 0
	var earliest time.Duration
	for i := range accounts {
		acc := &accounts[i]
		if acc == nil || acc.Platform != PlatformKiro || acc.Type != AccountTypeOAuth {
			if allowMixedScheduling {
				continue
			}
			return nil, 0
		}
		if _, excluded := excludedIDs[acc.ID]; excluded {
			continue
		}
		if !acc.IsSchedulable() {
			continue
		}
		if requestedModel != "" && !s.isModelSupportedByAccountWithContext(ctx, acc, requestedModel) {
			continue
		}
		if !s.isAccountSchedulableForQuota(acc) ||
			!s.isAccountSchedulableForWindowCost(ctx, acc, false) ||
			!s.isAccountSchedulableForRPM(ctx, acc, false) {
			continue
		}
		eligible++
		state, err := s.getKiroCooldownState(ctx, buildKiroAccountKey(acc))
		if err != nil || state == nil || !state.Active {
			return nil, 0
		}
		if state.Reason != kirocooldown.CooldownReason429 {
			return nil, 0
		}
		if earliest == 0 || state.Remaining < earliest {
			earliest = state.Remaining
		}
		tokenKeys = append(tokenKeys, buildKiroAccountKey(acc))
	}
	if eligible == 0 {
		return nil, 0
	}
	return tokenKeys, earliest
}

// calculateKiroConservativeTokenCost 使用保守兜底模型计算本次用量费用，避免因模型名
// 无法定价而漏计费。当 modelHint 明显属于 GPT 家族时改用 GPT 侧的保守兜底模型，
// 否则默认按 Claude 家族计费（兼容未提供 modelHint 的历史调用方）。
func (s *GatewayService) calculateKiroConservativeTokenCost(tokens UsageTokens, multiplier float64, modelHint string) *CostBreakdown {
	if s == nil || s.billingService == nil {
		return nil
	}
	fallbackModel := kiroConservativeFallbackBillingModel
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelHint)), "gpt-") {
		fallbackModel = kiroConservativeFallbackBillingModelGPT
	}
	cost, err := s.billingService.CalculateCost(fallbackModel, tokens, multiplier)
	if err != nil {
		return nil
	}
	return cost
}

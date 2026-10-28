package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// KiroAccountRuntimeDTO 是 Kiro（fork）账号的用量 / 冷却派生状态，从本地缓存读取，不持久化。
type KiroAccountRuntimeDTO struct {
	KiroQuotaState     string     `json:"kiro_quota_state,omitempty"`
	KiroQuotaReason    string     `json:"kiro_quota_reason,omitempty"`
	KiroQuotaResetAt   *time.Time `json:"kiro_quota_reset_at,omitempty"`
	KiroRuntimeState   string     `json:"kiro_runtime_state,omitempty"`
	KiroRuntimeReason  string     `json:"kiro_runtime_reason,omitempty"`
	KiroRuntimeResetAt *time.Time `json:"kiro_runtime_reset_at,omitempty"`
	// KiroQuotaRejectedAt / Reason 是上游额度拒绝时落库的「可退役」标记（extra 系统托管键）。
	KiroQuotaRejectedAt     *time.Time `json:"kiro_quota_rejected_at,omitempty"`
	KiroQuotaRejectedReason string     `json:"kiro_quota_rejected_reason,omitempty"`
	// KiroSubscriptionName 来自后台刷新的额度快照（如 "KIRO PRO MAX"），供账号列表显示套餐。
	KiroSubscriptionName string `json:"kiro_subscription_name,omitempty"`
}

func kiroAccountRuntimeDTO(a *service.Account) KiroAccountRuntimeDTO {
	if a == nil {
		return KiroAccountRuntimeDTO{}
	}
	out := KiroAccountRuntimeDTO{
		KiroQuotaState:     a.KiroQuotaState,
		KiroQuotaReason:    a.KiroQuotaReason,
		KiroQuotaResetAt:   a.KiroQuotaResetAt,
		KiroRuntimeState:   a.KiroRuntimeState,
		KiroRuntimeReason:  a.KiroRuntimeReason,
		KiroRuntimeResetAt: a.KiroRuntimeResetAt,
	}
	if a.Platform == service.PlatformKiro {
		out.KiroQuotaRejectedAt, out.KiroQuotaRejectedReason = service.KiroQuotaRejectionFromExtra(a.Extra)
		out.KiroSubscriptionName = service.KiroSubscriptionNameFromExtra(a.Extra)
	}
	return out
}

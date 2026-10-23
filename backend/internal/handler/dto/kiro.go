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
}

func kiroAccountRuntimeDTO(a *service.Account) KiroAccountRuntimeDTO {
	if a == nil {
		return KiroAccountRuntimeDTO{}
	}
	return KiroAccountRuntimeDTO{
		KiroQuotaState:     a.KiroQuotaState,
		KiroQuotaReason:    a.KiroQuotaReason,
		KiroQuotaResetAt:   a.KiroQuotaResetAt,
		KiroRuntimeState:   a.KiroRuntimeState,
		KiroRuntimeReason:  a.KiroRuntimeReason,
		KiroRuntimeResetAt: a.KiroRuntimeResetAt,
	}
}

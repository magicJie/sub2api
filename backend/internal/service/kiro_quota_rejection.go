package service

// Kiro（fork）上游额度拒绝的识别、标记与清除。
//
// 只有上游真正拒绝时才把账号移出调度（不按显示的百分比提前排除）：
//   - 402 MONTHLY_REQUEST_COUNT：限流到下月 1 号 UTC（原有行为）；
//   - 402 / 429 的 body 含 quota / credit / overage 类文字，或无法识别的 402：
//     限流到 reset（优先用快照里的 kiro_reset_at，没有就用下月 1 号 UTC），带 body 摘要记 warn；
//   - 限流性质的 429（Too many requests）不在这里处理，仍走冷却逻辑。
// 被拒绝的账号在 extra 里写 kiro_quota_rejected_at / kiro_quota_rejected_reason（「可退役」标记），
// 用专用 repo 方法只改这两个键，不改 updated_at、不发调度 outbox。
// 新周期 reset 后（限流已过且快照显示用量下降），后台额度刷新时自动清除标记。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	// KiroQuotaRejectedAtExtraKey 是账号被上游额度拒绝的时间（RFC3339，系统托管）。
	KiroQuotaRejectedAtExtraKey = "kiro_quota_rejected_at"
	// KiroQuotaRejectedReasonExtraKey 是拒绝原因（状态码、分类与 body 摘要，系统托管）。
	KiroQuotaRejectedReasonExtraKey = "kiro_quota_rejected_reason"

	kiroQuotaRejectedReasonMaxBytes = 256
)

// KiroQuotaRejectionManagedExtraKeys 是普通账号编辑不可写、沿用库里值的 extra 键。
var KiroQuotaRejectionManagedExtraKeys = []string{KiroQuotaRejectedAtExtraKey, KiroQuotaRejectedReasonExtraKey}

// KiroQuotaRejectionRepository 是写「可退役」标记的专用 repo 方法；只改上面两个键。
type KiroQuotaRejectionRepository interface {
	SetKiroQuotaRejected(ctx context.Context, accountID int64, rejectedAt time.Time, reason string) error
	ClearKiroQuotaRejected(ctx context.Context, accountID int64) error
}

// kiroQuotaRejections 是本实例刚标记的拒绝（accountID -> 限流截止时间），
// 调度快照同步前粘性判断也能立刻看到。
var kiroQuotaRejections sync.Map

// kiroQuotaRejectionCategory 判断上游错误是否属于额度拒绝；返回分类名，空串表示不是。
func kiroQuotaRejectionCategory(statusCode int, body string) string {
	if statusCode != http.StatusPaymentRequired && statusCode != http.StatusTooManyRequests {
		return ""
	}
	category := classifyKiroHTTPError(statusCode, body).Category
	switch category {
	case kiroErrorMonthlyRequest, kiroErrorQuotaExhausted, kiroErrorOverageExhausted:
		return category
	case kiroErrorPromptTooLong, kiroErrorModelCapacity:
		return "" // 与账号额度无关
	}
	if statusCode == http.StatusPaymentRequired {
		return "payment_required_unknown"
	}
	return "" // 限流性质的 429 走冷却
}

// markKiroQuotaRejectedIfNeeded 在上游额度拒绝时把账号限流到 reset 并写「可退役」标记；
// 返回 true 表示已按额度拒绝处理（调用方不应再走通用 429 / 402 处理）。
func (s *GatewayService) markKiroQuotaRejectedIfNeeded(ctx context.Context, account *Account, statusCode int, body string) bool {
	if s == nil || account == nil || account.Platform != PlatformKiro {
		return false
	}
	category := kiroQuotaRejectionCategory(statusCode, body)
	if category == "" {
		return false
	}
	now := time.Now()
	var resetAt time.Time
	if category == kiroErrorMonthlyRequest {
		s.markKiroMonthlyRequestCountRateLimited(ctx, account, body)
		resetAt = nextKiroMonthlyResetUTC(now)
	} else {
		resetAt = kiroQuotaRejectionResetAt(account, now)
		excerpt := truncateForLog([]byte(strings.TrimSpace(body)), 512)
		if s.accountRepo != nil {
			if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetAt); err != nil {
				logger.L().Warn("kiro quota rejection rate-limit failed",
					zap.Int64("account_id", account.ID),
					zap.Time("reset_at", resetAt),
					zap.Error(err),
				)
			}
		}
		logger.L().Warn("kiro quota rejected by upstream",
			zap.Int64("account_id", account.ID),
			zap.Int("status", statusCode),
			zap.String("category", category),
			zap.Time("reset_at", resetAt),
			zap.String("body_excerpt", excerpt),
		)
	}
	kiroQuotaRejections.Store(account.ID, resetAt)
	reason := fmt.Sprintf("%d %s", statusCode, category)
	if trimmed := strings.TrimSpace(body); trimmed != "" {
		reason = fmt.Sprintf("%s: %s", reason, truncateForLog([]byte(trimmed), kiroQuotaRejectedReasonMaxBytes))
	}
	if repo, ok := s.accountRepo.(KiroQuotaRejectionRepository); ok {
		// 落库不跟随请求取消：拒绝已经发生，标记不能因为客户端断开丢失。
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := repo.SetKiroQuotaRejected(writeCtx, account.ID, now, reason); err != nil {
			logger.L().Warn("kiro quota rejection mark failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	}
	return true
}

// kiroQuotaRejectionResetAt 返回额度拒绝的限流截止时间：优先用调度快照 / 落库快照里的
// kiro_reset_at（必须在未来），没有就用下月 1 号 UTC。
func kiroQuotaRejectionResetAt(account *Account, now time.Time) time.Time {
	if value, ok := kiroPaceSnapshots.Load(account.ID); ok {
		if reset := value.(kiroPaceSnapshot).resetAt; reset != nil && reset.After(now) {
			return *reset
		}
	}
	if snapshot := decodeKiroUsageSnapshot(account.Extra); snapshot != nil && snapshot.KiroResetAt != nil && snapshot.KiroResetAt.After(now) {
		return *snapshot.KiroResetAt
	}
	return nextKiroMonthlyResetUTC(now)
}

// KiroQuotaRejectionFromExtra 读取账号 extra 里的「可退役」标记；没有标记时 at 为 nil。
func KiroQuotaRejectionFromExtra(extra map[string]any) (at *time.Time, reason string) {
	if extra == nil {
		return nil, ""
	}
	raw, _ := extra[KiroQuotaRejectedAtExtraKey].(string)
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return nil, ""
	}
	reason, _ = extra[KiroQuotaRejectedReasonExtraKey].(string)
	return &parsed, reason
}

// kiroAccountQuotaRejected 报告账号是否处于「已被上游拒绝且未 reset」。
//
// 额度拒绝总是伴随 SetRateLimited（截止到 reset），而调度快照里的 extra 只保留调度用的键，
// 所以「未 reset」以限流状态为准；本实例刚打的标记（kiroQuotaRejections）兜住限流写入失败或
// 调度缓存尚未同步的窗口。限流被管理员手动清掉时视为不再拒绝；extra 里的标记只表示「可退役」。
func kiroAccountQuotaRejected(account *Account, now time.Time) bool {
	if account == nil {
		return false
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		return true
	}
	if value, ok := kiroQuotaRejections.Load(account.ID); ok {
		if now.Before(value.(time.Time)) {
			return true
		}
		kiroQuotaRejections.Delete(account.ID)
	}
	return false
}

// kiroQuotaRejectionShouldClear 判断新周期是否已经开始：限流已过、新快照晚于拒绝时间，
// 且用量下降或账期重置时间已经推进。
func kiroQuotaRejectionShouldClear(account *Account, prev, next *KiroUsageSnapshot, now time.Time) bool {
	if account == nil || !next.hasData() || next.KiroCredit == nil {
		return false
	}
	rejectedAt, _ := KiroQuotaRejectionFromExtra(account.Extra)
	if rejectedAt == nil || !next.UpdatedAt.After(*rejectedAt) {
		return false
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		return false
	}
	if prev.hasData() && prev.KiroCredit != nil && next.KiroCredit.PercentageUsed < prev.KiroCredit.PercentageUsed {
		return true
	}
	if next.KiroResetAt == nil {
		return false
	}
	if prev != nil && prev.KiroResetAt != nil && next.KiroResetAt.After(*prev.KiroResetAt) {
		return true
	}
	// 上一份快照已是新周期（例如拒绝后快照一直失败）：账期重置时间晚于限流截止时间也算 reset。
	return account.RateLimitResetAt != nil && next.KiroResetAt.After(*account.RateLimitResetAt)
}

// clearKiroQuotaRejectionAfterReset 在后台额度刷新成功后检查并清除「可退役」标记。
func (s *AccountUsageService) clearKiroQuotaRejectionAfterReset(ctx context.Context, account *Account, prev, next *KiroUsageSnapshot, now time.Time) {
	if s == nil || !kiroQuotaRejectionShouldClear(account, prev, next, now) {
		return
	}
	repo, ok := s.accountRepo.(KiroQuotaRejectionRepository)
	if !ok {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := repo.ClearKiroQuotaRejected(writeCtx, account.ID); err != nil {
		logger.L().Warn("kiro quota rejection clear failed", zap.Int64("account_id", account.ID), zap.Error(err))
		return
	}
	kiroQuotaRejections.Delete(account.ID)
	for _, key := range KiroQuotaRejectionManagedExtraKeys {
		delete(account.Extra, key)
	}
	logger.L().Info("kiro quota rejection cleared after reset", zap.Int64("account_id", account.ID))
}

// stripKiroQuotaRejectionExtra 删除普通编辑请求里的「可退役」标记（系统托管，只能由网关写入）。
func stripKiroQuotaRejectionExtra(extra map[string]any) {
	for _, key := range KiroQuotaRejectionManagedExtraKeys {
		delete(extra, key)
	}
}

// kiroPreserveQuotaRejectionExtra 让整份替换 extra 的编辑沿用库里的「可退役」标记。
func kiroPreserveQuotaRejectionExtra(dst, current map[string]any) {
	if dst == nil {
		return
	}
	stripKiroQuotaRejectionExtra(dst)
	for _, key := range KiroQuotaRejectionManagedExtraKeys {
		if value, ok := current[key]; ok {
			dst[key] = value
		}
	}
}

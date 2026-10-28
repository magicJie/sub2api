package service

// Kiro（fork）额度快照：后台刷新器把 getUsageLimits 的结果写进账号 extra 的
// kiro_usage_snapshot，账号页、批量 usage 接口和调度都读这份；进程内缓存只做加速。

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// KiroUsageSnapshotExtraKey 是账号 extra 中 Kiro 额度快照的键（系统托管，普通编辑不可写）。
const KiroUsageSnapshotExtraKey = "kiro_usage_snapshot"

// KiroUsageSnapshot 是落库的 Kiro 额度快照。
//
// UpdatedAt 是最近一次成功拉取的时间；LastAttemptAt 是最近一次尝试（成功或失败）。
// 失败时保留上一次成功的数据，只更新错误字段与失败计数。
// RefreshRequestedAt 由"全部刷新"写入，晚于 LastAttemptAt 时视为排队待刷新。
type KiroUsageSnapshot struct {
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

	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
	LastAttemptAt      time.Time  `json:"last_attempt_at"`
	LastError          string     `json:"last_error,omitempty"`
	ErrorCode          string     `json:"error_code,omitempty"`
	NeedsReauth        bool       `json:"needs_reauth,omitempty"`
	FailureCount       int        `json:"failure_count,omitempty"`
	RefreshRequestedAt *time.Time `json:"refresh_requested_at,omitempty"`
}

// hasData 报告快照里是否有一次成功拉取的额度数据。
func (s *KiroUsageSnapshot) hasData() bool {
	return s != nil && s.UpdatedAt != nil
}

// refreshRequested 报告快照是否被"全部刷新"排进了队列。
func (s *KiroUsageSnapshot) refreshRequested() bool {
	return s != nil && s.RefreshRequestedAt != nil && s.RefreshRequestedAt.After(s.LastAttemptAt)
}

// decodeKiroUsageSnapshot 从账号 extra 解析快照；缺失或格式不对时返回 nil。
// KiroSubscriptionNameFromExtra 返回额度快照里的套餐名，没有快照时为空。
func KiroSubscriptionNameFromExtra(extra map[string]any) string {
	if snap := decodeKiroUsageSnapshot(extra); snap != nil {
		return strings.TrimSpace(snap.KiroSubscriptionName)
	}
	return ""
}

func decodeKiroUsageSnapshot(extra map[string]any) *KiroUsageSnapshot {
	if extra == nil {
		return nil
	}
	raw, ok := extra[KiroUsageSnapshotExtraKey]
	if !ok || raw == nil {
		return nil
	}
	return decodeKiroUsageSnapshotValue(raw)
}

// DecodeKiroUsageSnapshotJSON 解析 repo 读出的快照 JSON 文本；无效时返回 nil。
func DecodeKiroUsageSnapshotJSON(raw string) *KiroUsageSnapshot {
	return decodeKiroUsageSnapshotValue(raw)
}

func decodeKiroUsageSnapshotValue(raw any) *KiroUsageSnapshot {
	var data []byte
	switch v := raw.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	case *KiroUsageSnapshot:
		if v == nil {
			return nil
		}
		cloned := *v
		return &cloned
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		data = encoded
	}
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var snapshot KiroUsageSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil
	}
	if snapshot.LastAttemptAt.IsZero() && snapshot.UpdatedAt == nil {
		return nil
	}
	return &snapshot
}

// buildKiroUsageSnapshot 把一次拉取结果合并进上一份快照。
// usage 无错误视为成功；否则保留 prev 的额度数据，只记录错误并累加失败次数。
func buildKiroUsageSnapshot(prev *KiroUsageSnapshot, usage *UsageInfo, now time.Time) *KiroUsageSnapshot {
	attempt := now.UTC()
	if usage != nil && usage.Error == "" && usage.ErrorCode == "" {
		updatedAt := attempt
		snapshot := &KiroUsageSnapshot{
			KiroSubscriptionName: usage.KiroSubscriptionName,
			KiroSubscriptionType: usage.KiroSubscriptionType,
			KiroResetAt:          usage.KiroResetAt,
			KiroOveragesEnabled:  usage.KiroOveragesEnabled,
			KiroCredit:           stripKiroCreditWindowStats(usage.KiroCredit),
			KiroBonus:            stripKiroCreditWindowStats(usage.KiroBonus),
			KiroOverage:          usage.KiroOverage,
			KiroQuotaState:       usage.KiroQuotaState,
			KiroQuotaReason:      usage.KiroQuotaReason,
			KiroQuotaResetAt:     usage.KiroQuotaResetAt,
			UpdatedAt:            &updatedAt,
			LastAttemptAt:        attempt,
		}
		return snapshot
	}
	snapshot := &KiroUsageSnapshot{}
	if prev != nil {
		cloned := *prev
		snapshot = &cloned
	}
	snapshot.LastAttemptAt = attempt
	snapshot.FailureCount++
	snapshot.RefreshRequestedAt = nil
	snapshot.LastError = "usage API error"
	snapshot.ErrorCode = errorCodeNetworkError
	snapshot.NeedsReauth = false
	if usage != nil {
		if usage.Error != "" {
			snapshot.LastError = usage.Error
		}
		if usage.ErrorCode != "" {
			snapshot.ErrorCode = usage.ErrorCode
		}
		snapshot.NeedsReauth = usage.NeedsReauth
		// 上游错误本身说明了额度状态（如 credits 耗尽）时以它为准。
		if usage.KiroQuotaState != "" {
			snapshot.KiroQuotaState = usage.KiroQuotaState
			snapshot.KiroQuotaReason = usage.KiroQuotaReason
			snapshot.KiroQuotaResetAt = usage.KiroQuotaResetAt
		}
	}
	return snapshot
}

func stripKiroCreditWindowStats(progress *KiroCreditProgress) *KiroCreditProgress {
	if progress == nil {
		return nil
	}
	cloned := *progress
	cloned.WindowStats = nil
	return &cloned
}

// usageInfo 把快照转成接口返回的 UsageInfo；UpdatedAt 取最近一次成功时间。
func (s *KiroUsageSnapshot) usageInfo(source string) *UsageInfo {
	info := &UsageInfo{Source: source}
	if s == nil {
		info.KiroUsagePending = true
		return info
	}
	info.KiroSubscriptionName = s.KiroSubscriptionName
	info.KiroSubscriptionType = s.KiroSubscriptionType
	info.KiroResetAt = s.KiroResetAt
	info.KiroOveragesEnabled = s.KiroOveragesEnabled
	info.KiroCredit = stripKiroCreditWindowStats(s.KiroCredit)
	info.KiroBonus = stripKiroCreditWindowStats(s.KiroBonus)
	info.KiroOverage = s.KiroOverage
	info.KiroQuotaState = s.KiroQuotaState
	info.KiroQuotaReason = s.KiroQuotaReason
	info.KiroQuotaResetAt = s.KiroQuotaResetAt
	if s.UpdatedAt != nil {
		updatedAt := *s.UpdatedAt
		info.UpdatedAt = &updatedAt
	}
	lastAttempt := s.LastAttemptAt
	if !lastAttempt.IsZero() {
		info.KiroUsageLastAttemptAt = &lastAttempt
	}
	if s.FailureCount > 0 {
		info.Error = s.LastError
		info.ErrorCode = s.ErrorCode
		info.NeedsReauth = s.NeedsReauth
	}
	// 拉取失败过的不算"排队中"：错误已经展示，重试由退避控制。
	info.KiroUsagePending = (!s.hasData() && s.FailureCount == 0) || s.refreshRequested()
	return info
}

// restoreKiroPaceSnapshot 用落库快照恢复调度用的进程内快照；只在快照更新时覆盖。
func restoreKiroPaceSnapshot(accountID int64, snapshot *KiroUsageSnapshot) {
	if accountID <= 0 || !snapshot.hasData() || snapshot.KiroCredit == nil || snapshot.KiroCredit.UsageLimit <= 0 {
		return
	}
	updatedAt := *snapshot.UpdatedAt
	if value, ok := kiroPaceSnapshots.Load(accountID); ok && !value.(kiroPaceSnapshot).updatedAt.Before(updatedAt) {
		return
	}
	kiroPaceSnapshots.Store(accountID, kiroPaceSnapshot{
		usedPercent: snapshot.KiroCredit.PercentageUsed,
		resetAt:     snapshot.KiroResetAt,
		updatedAt:   updatedAt,
	})
}

// forgetKiroUsageSnapshot 清掉账号的进程内额度缓存与调度快照（凭据变更、删除时调用）。
func forgetKiroUsageSnapshot(accountID int64) {
	if accountID <= 0 {
		return
	}
	kiroPaceSnapshots.Delete(accountID)
	kiroPaceRefreshAttempts.Delete(accountID)
	if usageService := kiroPaceRefresher.Load(); usageService != nil && usageService.cache != nil {
		usageService.cache.kiroUsageCache.Delete(accountID)
	}
}

// kiroUsageIdentityKeys 是决定"还是不是同一个 Kiro 账号"的凭据字段；变化时旧快照作废。
// access_token 不在内：后台 token 刷新每小时都会换它。
var kiroUsageIdentityKeys = []string{
	"refresh_token", "profile_arn", "email", "region", "api_region", "start_url", "client_id", "auth_method", "provider",
}

func kiroUsageIdentity(account *Account) map[string]any {
	if account == nil || account.Platform != PlatformKiro {
		return nil
	}
	identity := map[string]any{"type": account.Type}
	for _, key := range kiroUsageIdentityKeys {
		if value, ok := account.Credentials[key]; ok {
			identity[key] = value
		}
	}
	return identity
}

// kiroInvalidateUsageSnapshotOnIdentityChange 是账号编辑路径的 Kiro 分支：
// 平台不再是 Kiro 或身份字段变化时，随本次写入删掉快照并清掉进程内缓存。
func kiroInvalidateUsageSnapshotOnIdentityChange(account *Account, previousIdentity map[string]any) {
	if account == nil || account.Extra == nil {
		return
	}
	if _, ok := account.Extra[KiroUsageSnapshotExtraKey]; !ok {
		return
	}
	if account.Platform == PlatformKiro && reflect.DeepEqual(previousIdentity, kiroUsageIdentity(account)) {
		return
	}
	delete(account.Extra, KiroUsageSnapshotExtraKey)
	forgetKiroUsageSnapshot(account.ID)
}

// kiroUsageSnapshotMode 报告 Kiro 额度是否由后台刷新器维护（开关打开且已注入）。
// 关闭时回到旧行为：页面打开时按需拉取上游。
func (s *AccountUsageService) kiroUsageSnapshotMode() bool {
	return s != nil && s.kiroUsageRefresher != nil && s.kiroUsageRefresher.Enabled()
}

// kiroBatchForce 是 GetUsageBatch 的 Kiro 分支：快照模式下批量接口不打上游，忽略 force。
func (s *AccountUsageService) kiroBatchForce(account *Account, force bool) bool {
	if force && account != nil && account.Platform == PlatformKiro && s.kiroUsageSnapshotMode() {
		return false
	}
	return force
}

// readKiroUsageSnapshot 只读快照，不请求上游；没有快照时返回空结果并唤醒后台刷新。
func (s *AccountUsageService) readKiroUsageSnapshot(ctx context.Context, account *Account) *UsageInfo {
	snapshot := decodeKiroUsageSnapshot(account.Extra)
	var usage *UsageInfo
	if snapshot != nil {
		usage = snapshot.usageInfo("active")
		restoreKiroPaceSnapshot(account.ID, snapshot)
	} else if cached, ok := s.getCachedKiroUsage(account.ID); ok && cached.ErrorCode == "" && cached.Error == "" {
		// 落库失败时的兜底：本实例刚拉到的结果还在内存缓存里。
		usage = cached
		usage.Source = "active"
	} else {
		usage = (*KiroUsageSnapshot)(nil).usageInfo("active")
	}
	if usage.KiroUsagePending && s.kiroUsageRefresher != nil {
		s.kiroUsageRefresher.Wake()
	}
	s.attachKiroRuntimeState(ctx, account, usage)
	return usage
}

// refreshKiroUsageSnapshot 同步拉取一次上游并把结果落库（后台刷新与单号 force 共用）。
func (s *AccountUsageService) refreshKiroUsageSnapshot(ctx context.Context, account *Account) (*UsageInfo, *KiroUsageSnapshot, error) {
	prev := decodeKiroUsageSnapshot(account.Extra)
	usage, err := s.getKiroUsage(ctx, account, "active", true)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	if s.kiroUsageRefresher != nil {
		now = s.kiroUsageRefresher.currentTime()
	}
	snapshot := buildKiroUsageSnapshot(prev, usage, now)
	if s.kiroUsageRefresher != nil {
		if persistErr := s.kiroUsageRefresher.persistSnapshot(ctx, account, snapshot); persistErr != nil {
			return usage, snapshot, persistErr
		}
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	account.Extra[KiroUsageSnapshotExtraKey] = snapshot
	restoreKiroPaceSnapshot(account.ID, snapshot)
	s.clearKiroQuotaRejectionAfterReset(ctx, account, prev, snapshot, now)
	return usage, snapshot, nil
}

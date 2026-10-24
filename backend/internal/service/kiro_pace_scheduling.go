package service

// Kiro（fork）多账号调度：按额度进度排序（借鉴 yetone/magpie，MIT）。
//
// Kiro 订阅按月重置 Credit。默认的「优先级 → 负载率 → LRU」不看额度，容易出现
// 一个账号先用到见底、另一个账号到重置时还剩大半的情况。这里在优先级之后、负载率之前
// 加一层（同一进度档内再按负载率、LRU 打散）：
//   - 按已用比例分档：<90% 正常；90%–98% 次之；≥98% 最后（与 magpie 一致）。
//   - 正常档内按进度分 pace = (100 − used%) / max(距重置小时数, 1) 从高到低，
//     即「剩余额度相对剩余时间最多」的账号先用；与档内最高值相差 10% 以内视为同一档，
//     交给后续 LRU 打散，避免所有新会话挤到同一个账号。
//   - 没有用量快照的账号按「刚开新周期、30 天内用完」估算，不会被饿死。
// 用量来自 AccountUsageService 拉取 Kiro 额度时写入的进程内快照，调度时不发网络请求。
// 粘性会话（Layer 1.5）在此之前生效，同一会话不会因为排序变化而换账号。
// 设置 SUB2API_KIRO_PACE_SCHEDULING=off 可关闭，回到原有排序。

import (
	"context"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	kiroPaceLowShare   = 90.0
	kiroPaceSpentShare = 98.0
	kiroPaceBandRatio  = 0.9
	// kiroPaceUnknownWindow 是没有用量快照时假设的剩余周期。
	kiroPaceUnknownWindow = 30 * 24 * time.Hour
	// kiroPaceSnapshotMaxAge 之后的快照视为过期，按未知处理。
	kiroPaceSnapshotMaxAge = 6 * time.Hour
	// kiroPaceRefreshAfter 之后调度时在后台刷新一次用量（同一账号每分钟最多一次）。
	kiroPaceRefreshAfter    = 5 * time.Minute
	kiroPaceRefreshMinGap   = time.Minute
	kiroPaceRefreshDeadline = 20 * time.Second
)

// kiroPaceRefresher 由 ProvideKiroRuntimeBinding 注入，用于后台刷新 Kiro 用量快照。
var kiroPaceRefresher atomic.Pointer[AccountUsageService]

var kiroPaceRefreshAttempts sync.Map // accountID -> time.Time

// maybeRefreshKiroPace 在快照缺失或较旧时异步拉取一次用量；不阻塞调度。
func maybeRefreshKiroPace(account *Account, now time.Time) {
	usageService := kiroPaceRefresher.Load()
	if usageService == nil || account == nil || account.Type != AccountTypeOAuth {
		return
	}
	if value, ok := kiroPaceSnapshots.Load(account.ID); ok && now.Sub(value.(kiroPaceSnapshot).updatedAt) < kiroPaceRefreshAfter {
		return
	}
	if last, ok := kiroPaceRefreshAttempts.Load(account.ID); ok && now.Sub(last.(time.Time)) < kiroPaceRefreshMinGap {
		return
	}
	kiroPaceRefreshAttempts.Store(account.ID, now)
	snapshot := *account
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), kiroPaceRefreshDeadline)
		defer cancel()
		_, _ = usageService.getKiroUsage(ctx, &snapshot, "active", false)
	}()
}

type kiroPaceSnapshot struct {
	usedPercent float64
	resetAt     *time.Time
	updatedAt   time.Time
}

var kiroPaceSnapshots sync.Map // accountID -> kiroPaceSnapshot

// recordKiroPaceSnapshot 在拿到 Kiro 额度后记录调度用的快照。
func recordKiroPaceSnapshot(accountID int64, usage *UsageInfo, now time.Time) {
	if accountID <= 0 || usage == nil || usage.KiroCredit == nil || usage.KiroCredit.UsageLimit <= 0 {
		return
	}
	kiroPaceSnapshots.Store(accountID, kiroPaceSnapshot{
		usedPercent: usage.KiroCredit.PercentageUsed,
		resetAt:     usage.KiroResetAt,
		updatedAt:   now,
	})
}

func kiroPaceSchedulingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_PACE_SCHEDULING"))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// kiroPaceInfo 返回账号的已用比例、进度分，以及是否有可信快照。
func kiroPaceInfo(accountID int64, now time.Time) (used, pace float64, known bool) {
	value, ok := kiroPaceSnapshots.Load(accountID)
	if ok {
		snap := value.(kiroPaceSnapshot)
		if now.Sub(snap.updatedAt) <= kiroPaceSnapshotMaxAge {
			used = math.Min(math.Max(snap.usedPercent, 0), 100)
			if snap.resetAt != nil && !now.Before(*snap.resetAt) {
				used = 0 // 已过重置时间，额度已经回满
			}
			hours := kiroPaceUnknownWindow.Hours()
			if snap.resetAt != nil && now.Before(*snap.resetAt) {
				hours = snap.resetAt.Sub(now).Hours()
			}
			return used, (100 - used) / math.Max(hours, 1), true
		}
	}
	return 0, 100 / kiroPaceUnknownWindow.Hours(), false
}

func kiroPaceTier(used float64) int {
	switch {
	case used >= kiroPaceSpentShare:
		return 2
	case used >= kiroPaceLowShare:
		return 1
	default:
		return 0
	}
}

// filterByKiroPace 在全部是 Kiro 账号时，按额度档位与进度分收窄候选集合；
// 混有其它平台的账号时原样返回（其它平台没有可比的额度数据）。
func filterByKiroPace(accounts []accountWithLoad) []accountWithLoad {
	if len(accounts) <= 1 || !kiroPaceSchedulingEnabled() {
		return accounts
	}
	for _, acc := range accounts {
		if acc.account == nil || acc.account.Platform != PlatformKiro {
			return accounts
		}
	}
	now := time.Now()
	type scored struct {
		acc  accountWithLoad
		used float64
		pace float64
		tier int
	}
	items := make([]scored, 0, len(accounts))
	bestTier := 3
	for _, acc := range accounts {
		maybeRefreshKiroPace(acc.account, now)
		used, pace, _ := kiroPaceInfo(acc.account.ID, now)
		tier := kiroPaceTier(used)
		items = append(items, scored{acc: acc, used: used, pace: pace, tier: tier})
		if tier < bestTier {
			bestTier = tier
		}
	}
	result := make([]accountWithLoad, 0, len(accounts))
	if bestTier > 0 {
		// 都在低额度或耗尽档：用得最少的先上。
		minUsed := math.Inf(1)
		for _, it := range items {
			if it.tier == bestTier && it.used < minUsed {
				minUsed = it.used
			}
		}
		for _, it := range items {
			if it.tier == bestTier && it.used == minUsed {
				result = append(result, it.acc)
			}
		}
		return result
	}
	topPace := 0.0
	for _, it := range items {
		if it.tier == 0 && it.pace > topPace {
			topPace = it.pace
		}
	}
	for _, it := range items {
		if it.tier == 0 && it.pace >= topPace*kiroPaceBandRatio {
			result = append(result, it.acc)
		}
	}
	return result
}

// kiroStickyAccountSpent 报告粘住的 Kiro 账号是否已接近用尽（≥98%），且池中还有未到这一档的
// Kiro 账号可接手。此时放开粘性：继续粘着只会在几次请求后撞上额度耗尽、被迫中途换号。
// 会话绑定不删除，额度回满后（重置）仍可回到原账号。
func kiroStickyAccountSpent(account *Account, pool []Account) bool {
	if account == nil || account.Platform != PlatformKiro || !kiroPaceSchedulingEnabled() {
		return false
	}
	now := time.Now()
	used, _, known := kiroPaceInfo(account.ID, now)
	if !known || used < kiroPaceSpentShare {
		return false
	}
	for i := range pool {
		other := &pool[i]
		if other.ID == account.ID || other.Platform != PlatformKiro || !other.IsSchedulable() {
			continue
		}
		if otherUsed, _, _ := kiroPaceInfo(other.ID, now); otherUsed < kiroPaceSpentShare {
			return true
		}
	}
	return false
}

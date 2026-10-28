package service

// Kiro（fork）多账号调度：churn 平台的「集中耗尽」策略（默认）。
//
// Kiro 是 churn 平台：账号用完就退役、腾出 IP。均摊用量（pace 策略）会让所有号同时半满，
// 没有一个号能尽早用完。drain 策略在优先级之后、负载率之前按以下顺序收窄候选：
//   - 有可信快照（≤6 小时）的号排在没有快照 / 快照过期的号之前（未知号不排除）；
//   - 重置时间越早越先用：到重置还用不完的额度会作废；
//   - 同一重置时间里，已用比例越高越先用；
//   - 完全相同的号交给后续负载率、LRU 打散。
// 显示 100% 但未被上游拒绝的号仍参与调度；只有上游真正拒绝（402 / 额度类 429）时才由
// kiro_quota_rejection.go 标记限流到 reset，并打上「可退役」标记。
// 首选号并发满或在 429 冷却时，调度循环会移除它并重新收窄，自然溢出到排序里的下一个号。
//
// 开关：SUB2API_KIRO_SCHEDULING_STRATEGY=drain|pace（默认 drain；pace 回到
// kiro_pace_scheduling.go 的均摊逻辑）。SUB2API_KIRO_PACE_SCHEDULING=off 关闭全部 Kiro 排序。

import (
	"math"
	"os"
	"strings"
	"time"
)

const (
	kiroSchedulingStrategyDrain = "drain"
	kiroSchedulingStrategyPace  = "pace"
	kiroSchedulingStrategyOff   = "off"
)

// kiroSchedulingStrategy 返回当前生效的 Kiro 排序策略。
func kiroSchedulingStrategy() string {
	if !kiroPaceSchedulingEnabled() {
		return kiroSchedulingStrategyOff
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_SCHEDULING_STRATEGY"))) == kiroSchedulingStrategyPace {
		return kiroSchedulingStrategyPace
	}
	return kiroSchedulingStrategyDrain
}

// filterKiroCandidates 是调度主流程的 Kiro 收窄入口：按策略分派。
func filterKiroCandidates(accounts []accountWithLoad) []accountWithLoad {
	switch kiroSchedulingStrategy() {
	case kiroSchedulingStrategyPace:
		return filterByKiroPace(accounts)
	case kiroSchedulingStrategyDrain:
		return filterByKiroDrain(accounts)
	default:
		return accounts
	}
}

// kiroDrainKey 是 drain 排序键：known 优先，其次 resetAt 早，其次 used 高。
type kiroDrainKey struct {
	known   bool
	resetAt int64 // Unix 分钟；未知时为 MaxInt64
	used    float64
}

func (k kiroDrainKey) better(o kiroDrainKey) bool {
	if k.known != o.known {
		return k.known
	}
	if k.resetAt != o.resetAt {
		return k.resetAt < o.resetAt
	}
	return k.used > o.used
}

// kiroDrainInfo 读取调度快照；没有快照、快照过期或已过重置时间（新周期尚未刷新）都视为未知。
func kiroDrainInfo(accountID int64, now time.Time) kiroDrainKey {
	unknown := kiroDrainKey{resetAt: math.MaxInt64}
	value, ok := kiroPaceSnapshots.Load(accountID)
	if !ok {
		return unknown
	}
	snap := value.(kiroPaceSnapshot)
	if now.Sub(snap.updatedAt) > kiroPaceSnapshotMaxAge {
		return unknown
	}
	key := kiroDrainKey{known: true, resetAt: math.MaxInt64, used: math.Min(math.Max(snap.usedPercent, 0), 100)}
	if snap.resetAt != nil {
		if !now.Before(*snap.resetAt) {
			return unknown
		}
		// 按分钟比较：同一账期的重置时间可能差几秒。
		key.resetAt = snap.resetAt.Unix() / 60
	}
	return key
}

// filterByKiroDrain 在全部是 Kiro 账号时，收窄到 drain 排序键最优的那一批；
// 混有其它平台的账号时原样返回。
func filterByKiroDrain(accounts []accountWithLoad) []accountWithLoad {
	if len(accounts) <= 1 {
		return accounts
	}
	for _, acc := range accounts {
		if acc.account == nil || acc.account.Platform != PlatformKiro {
			return accounts
		}
	}
	now := time.Now()
	keys := make([]kiroDrainKey, len(accounts))
	best := 0
	for i, acc := range accounts {
		maybeRefreshKiroPace(acc.account, now)
		keys[i] = kiroDrainInfo(acc.account.ID, now)
		if keys[i].better(keys[best]) {
			best = i
		}
	}
	result := make([]accountWithLoad, 0, len(accounts))
	for i, acc := range accounts {
		if keys[i] == keys[best] {
			result = append(result, acc)
		}
	}
	return result
}

// kiroStickyAccountSpent 报告粘住的 Kiro 账号是否应放开粘性。
//   - 已被上游额度拒绝（限流中，或带 rejected 标记且未到 reset）时放开：继续粘着只会再被拒。
//   - pace 策略下沿用原有判断：≥98% 且池中有未到这一档的号可接手。
//
// drain 策略不看显示的百分比：显示 100% 的号可能还能正常请求。
// 会话绑定不删除，额度回满后（重置）仍可回到原账号。
func kiroStickyAccountSpent(account *Account, pool []Account) bool {
	if account == nil || account.Platform != PlatformKiro {
		return false
	}
	switch kiroSchedulingStrategy() {
	case kiroSchedulingStrategyOff:
		return false
	case kiroSchedulingStrategyPace:
		return kiroAccountQuotaRejected(account, time.Now()) || kiroStickyAccountPaceSpent(account, pool)
	default:
		return kiroAccountQuotaRejected(account, time.Now())
	}
}

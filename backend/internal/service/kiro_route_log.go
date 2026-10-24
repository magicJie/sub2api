package service

// Kiro（fork）路由记录：在进程内保留最近 N 次 Kiro 转发尝试，便于排查调度与换号（借鉴 yetone/magpie，MIT）。
//
// 每次把请求交给某个 Kiro 账号就记一条：请求 ID、入口、模型、账号、该账号当时的额度进度与结果。
// 同一请求因 failover 换号会留下多条相同请求 ID 的记录，按时间即可看出尝试顺序。
// 只存内存、不落库，重启清空；不记录请求或响应内容。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const kiroRouteLogCapacity = 200

// KiroRouteRecord 是一次 Kiro 转发尝试。
type KiroRouteRecord struct {
	At          time.Time `json:"at"`
	RequestID   string    `json:"request_id,omitempty"`
	Endpoint    string    `json:"endpoint"`
	Model       string    `json:"model,omitempty"`
	Stream      bool      `json:"stream"`
	GroupID     *int64    `json:"group_id,omitempty"`
	AccountID   int64     `json:"account_id"`
	AccountName string    `json:"account_name,omitempty"`
	// UsedPercent / Pace 为调度时该账号的额度快照；PaceKnown=false 表示没有可信快照（按估算排序）。
	UsedPercent float64 `json:"used_percent"`
	Pace        float64 `json:"pace"`
	PaceKnown   bool    `json:"pace_known"`
	// Outcome：ok、failover（换号重试）、error。
	Outcome    string `json:"outcome"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type kiroRouteLog struct {
	mu      sync.Mutex
	records []KiroRouteRecord
	next    int
	full    bool
}

var globalKiroRouteLog = &kiroRouteLog{records: make([]KiroRouteRecord, kiroRouteLogCapacity)}

func (l *kiroRouteLog) add(record KiroRouteRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records[l.next] = record
	l.next = (l.next + 1) % len(l.records)
	if l.next == 0 {
		l.full = true
	}
}

// recent 按时间倒序返回最多 limit 条记录。
func (l *kiroRouteLog) recent(limit int) []KiroRouteRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	size := l.next
	if l.full {
		size = len(l.records)
	}
	if limit <= 0 || limit > size {
		limit = size
	}
	out := make([]KiroRouteRecord, 0, limit)
	for i := 1; i <= limit; i++ {
		out = append(out, l.records[(l.next-i+len(l.records))%len(l.records)])
	}
	return out
}

// KiroRecentRoutes 返回最近的 Kiro 转发尝试（新的在前）。
func KiroRecentRoutes(limit int) []KiroRouteRecord {
	return globalKiroRouteLog.recent(limit)
}

// recordKiroRoute 记录一次转发尝试；在 forwardKiro* 入口 defer 调用。
func recordKiroRoute(ctx context.Context, endpoint string, account *Account, parsed *ParsedRequest, startTime time.Time, err error) {
	if account == nil {
		return
	}
	now := time.Now()
	record := KiroRouteRecord{
		At:          startTime,
		Endpoint:    endpoint,
		AccountID:   account.ID,
		AccountName: account.Name,
		Outcome:     "ok",
		DurationMs:  now.Sub(startTime).Milliseconds(),
	}
	if ctx != nil {
		if requestID, _ := ctx.Value(ctxkey.RequestID).(string); requestID != "" {
			record.RequestID = requestID
		}
	}
	if parsed != nil {
		record.Model = parsed.Model
		record.Stream = parsed.Stream
		record.GroupID = parsed.GroupID
	}
	record.UsedPercent, record.Pace, record.PaceKnown = kiroPaceInfo(account.ID, now)
	if err != nil {
		record.Outcome = "error"
		var failoverErr *UpstreamFailoverError
		var bridgeErr *kiroHTTPErrorAsBridgeError
		switch {
		case errors.As(err, &failoverErr):
			record.Outcome = "failover"
			record.StatusCode = failoverErr.StatusCode
		case errors.As(err, &bridgeErr) && bridgeErr.kiroHTTPErrorOutcome != nil:
			record.StatusCode = bridgeErr.StatusCode
		}
		record.Error = truncateKiroRouteError(sanitizeUpstreamErrorMessage(err.Error()))
	}
	globalKiroRouteLog.add(record)
}

func truncateKiroRouteError(msg string) string {
	msg = strings.TrimSpace(msg)
	const limit = 300
	if len(msg) <= limit {
		return msg
	}
	cut := limit
	for cut > 0 && (msg[cut]&0xC0) == 0x80 {
		cut--
	}
	return msg[:cut] + "…"
}

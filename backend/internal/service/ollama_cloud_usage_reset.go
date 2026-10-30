package service

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ollama429ExhaustedPercent 是 429 后判定窗口耗尽的阈值：页面按整数四舍五入显示，
// 上游已拒绝时可能仍显示 99%。只在 429 触发的 probe 中使用。
const ollama429ExhaustedPercent = 99

var (
	ollamaRelativeDurationPattern = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?|an?)\s*(seconds?|secs?|minutes?|mins?|hours?|hrs?|days?|weeks?|months?)\b`)
	ollamaBalanceNumberPattern    = regexp.MustCompile(`-?[0-9][0-9,]*(?:\.[0-9]+)?`)
)

// ollamaCloudUsageExhaustionResetAt returns the wall-clock time at which an
// exhausted Ollama Cloud usage window is expected to roll over. It is only used
// after a model 429, so it answers "which exhausted window explains this 429 and
// when does it clear". It is a pure helper and performs no I/O.
//
// Snapshot usability: the observation is trusted only when it is successful
// (Status == OK) and FetchedAt is present and not older than minFetchedAt.
//
// Window handling:
//   - Five-hour and seven-day windows count as exhausted at UsedPercent >= 99
//     (the page rounds to whole percents).
//   - The monthly window counts only when it is exhausted and the page shows no
//     positive purchased balance: purchased credits keep requests flowing after
//     included credits run out, so a 429 then is not a monthly block.
//   - A window's reset is its ResetAt, else its relative ResetText ("in 3 hours",
//     "Refills in 1 week") resolved against FetchedAt.
//   - The account cannot recover before any exhausted window rolls over, so the
//     latest known future reset is a safe lower bound and is returned. Exhausted
//     windows whose reset is unknown or already past are skipped (the caller's
//     escalating cooldown covers them); ok is false only when no exhausted window
//     has a known future reset.
func ollamaCloudUsageExhaustionResetAt(
	snapshot *OllamaCloudUsageSnapshot,
	now time.Time,
	minFetchedAt time.Time,
) (reset time.Time, ok bool) {
	if snapshot == nil || snapshot.Status != OllamaCloudUsageStatusOK || snapshot.Data == nil {
		return time.Time{}, false
	}
	if snapshot.FetchedAt == nil || snapshot.FetchedAt.IsZero() || snapshot.FetchedAt.Before(minFetchedAt) {
		return time.Time{}, false
	}
	windows := []*OllamaCloudUsageWindow{snapshot.Data.FiveHour, snapshot.Data.SevenDay}
	if !ollamaCloudUsageHasPositiveBalance(snapshot.Data.Balance) {
		windows = append(windows, snapshot.Data.Monthly)
	}

	var latest time.Time
	for _, window := range windows {
		if window == nil || window.UsedPercent < ollama429ExhaustedPercent {
			continue
		}
		resetAt := ollamaCloudUsageWindowResetAt(window, *snapshot.FetchedAt)
		if resetAt.IsZero() || !resetAt.After(now) {
			continue
		}
		if latest.IsZero() || resetAt.After(latest) {
			latest = resetAt
		}
	}
	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func ollamaCloudUsageWindowResetAt(window *OllamaCloudUsageWindow, fetchedAt time.Time) time.Time {
	if window.ResetAt != nil && !window.ResetAt.IsZero() {
		return window.ResetAt.UTC()
	}
	if d, ok := parseOllamaRelativeDuration(window.ResetText); ok {
		return fetchedAt.Add(d).UTC()
	}
	return time.Time{}
}

// parseOllamaRelativeDuration 解析 "in 3 hours"、"2 hours 15 minutes"、"Refills in 1 week" 等相对时长。
func parseOllamaRelativeDuration(text string) (time.Duration, bool) {
	matches := ollamaRelativeDurationPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, m := range matches {
		n := 1.0
		if v := strings.ToLower(m[1]); v != "a" && v != "an" {
			parsed, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, false
			}
			n = parsed
		}
		var unit time.Duration
		switch u := strings.ToLower(m[2]); {
		case strings.HasPrefix(u, "sec"):
			unit = time.Second
		case strings.HasPrefix(u, "min"):
			unit = time.Minute
		case strings.HasPrefix(u, "h"):
			unit = time.Hour
		case strings.HasPrefix(u, "day"):
			unit = 24 * time.Hour
		case strings.HasPrefix(u, "week"):
			unit = 7 * 24 * time.Hour
		case strings.HasPrefix(u, "month"):
			unit = 30 * 24 * time.Hour
		}
		total += time.Duration(n * float64(unit))
	}
	return total, total > 0
}

func ollamaCloudUsageHasPositiveBalance(balance string) bool {
	m := ollamaBalanceNumberPattern.FindString(balance)
	if m == "" {
		return false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", ""), 64)
	return err == nil && v > 0 && !strings.Contains(balance[:strings.Index(balance, m)], "-")
}

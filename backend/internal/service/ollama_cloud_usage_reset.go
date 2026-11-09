package service

import (
	"strconv"
	"strings"
	"time"
)

// Official API percentages are exact; only a fully used window is exhausted.
const ollama429ExhaustedPercent = 100

// ollamaCloudUsageExhaustionResetAt returns the wall-clock time at which an
// exhausted Ollama Cloud usage window is expected to roll over. It is only used
// after a model 429, so it answers "which exhausted window explains this 429 and
// when does it clear". It is a pure helper and performs no I/O.
//
// Snapshot usability: the observation is trusted only when it is successful
// (Status == OK) and FetchedAt is present and not older than minFetchedAt.
//
// Window handling:
//   - Five-hour and seven-day windows count as exhausted at UsedPercent >= 100.
//   - The monthly window counts only when exhausted with no positive total
//     balance; purchased credits keep requests flowing after included credits run out.
//   - Each window uses the exact ResetAt reported by the balance API.
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
		if window.ResetAt == nil {
			continue
		}
		resetAt := window.ResetAt.UTC()
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

func ollamaCloudUsageHasPositiveBalance(balance string) bool {
	v, err := strconv.ParseFloat(strings.TrimPrefix(balance, "$"), 64)
	return err == nil && v > 0
}

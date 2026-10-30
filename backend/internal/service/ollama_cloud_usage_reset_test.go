package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mustT(t time.Time) *time.Time { return &t }

func TestOllamaCloudUsageExhaustionResetAt(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	minFetchedAt := now.Add(-time.Hour)

	at := func(h, m int) *time.Time {
		return mustT(now.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute))
	}
	window := func(percent float64, reset *time.Time) *OllamaCloudUsageWindow {
		return &OllamaCloudUsageWindow{UsedPercent: percent, ResetAt: reset}
	}
	okSnapshot := func(data *OllamaCloudUsageData) *OllamaCloudUsageSnapshot {
		return &OllamaCloudUsageSnapshot{
			Status:    OllamaCloudUsageStatusOK,
			Data:      data,
			FetchedAt: mustT(now.Add(-time.Minute)),
		}
	}

	tests := []struct {
		name     string
		snapshot *OllamaCloudUsageSnapshot
		now      time.Time
		minFresh time.Time
		want     time.Time
		wantOK   bool
	}{
		{
			name:   "nil snapshot",
			wantOK: false,
		},
		{
			name: "failed status unusable",
			snapshot: &OllamaCloudUsageSnapshot{
				Status: OllamaCloudUsageStatusFailed,
				Data:   &OllamaCloudUsageData{FiveHour: window(100, at(2, 0))},
			},
			wantOK: false,
		},
		{
			name: "nil data unusable",
			snapshot: &OllamaCloudUsageSnapshot{
				Status:    OllamaCloudUsageStatusOK,
				FetchedAt: mustT(now.Add(-time.Minute)),
			},
			wantOK: false,
		},
		{
			name: "stale snapshot rejected",
			snapshot: func() *OllamaCloudUsageSnapshot {
				s := okSnapshot(&OllamaCloudUsageData{FiveHour: window(100, at(2, 0))})
				s.FetchedAt = mustT(now.Add(-3 * time.Hour))
				return s
			}(),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
		{
			name: "no window exhausted yields no recovery",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(50, at(2, 0)),
				SevenDay: window(30, at(48, 0)),
			}),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
		{
			name: "five hour exhausted below boundary not counted",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(98.5, at(2, 0)),
			}),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
		{
			name: "five hour exactly 100 future reset",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(2 * time.Hour),
			wantOK:   true,
		},
		{
			name: "five hour exhausted past reset yields no recovery",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(-1, 0)),
			}),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
		{
			name: "five hour exhausted missing reset yields no recovery",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, nil),
			}),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
		{
			name: "seven day exhausted alone",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				SevenDay: window(120, at(30, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(30 * time.Hour),
			wantOK:   true,
		},
		{
			name: "monthly included credits exhausted with purchased balance is not a hard limit",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				Monthly: window(100, at(720, 0)),
				Balance: "$15.47",
			}),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
		{
			name: "monthly exhaustion without a reset does not hide a five-hour recovery",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
				Monthly:  window(100, nil),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(2 * time.Hour),
			wantOK:   true,
		},
		{
			name: "seven day exhausted missing reset keeps known five-hour lower bound",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
				SevenDay: window(100, nil),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(2 * time.Hour),
			wantOK:   true,
		},
		{
			name: "seven day exhausted past reset keeps known five-hour lower bound",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
				SevenDay: window(100, at(-1, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(2 * time.Hour),
			wantOK:   true,
		},
		{
			name: "both exhausted returns later reset",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
				SevenDay: window(100, at(48, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(48 * time.Hour),
			wantOK:   true,
		},
		{
			name: "both exhausted returns later even when seven day is sooner",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(48, 0)),
				SevenDay: window(100, at(2, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(48 * time.Hour),
			wantOK:   true,
		},
		{
			name: "non exhausted window reset must not affect",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
				SevenDay: window(40, at(1, 0)), // past + not exhausted -> ignored
			}),
			minFresh: minFetchedAt,
			want:     now.Add(2 * time.Hour),
			wantOK:   true,
		},
		{
			name: "non exhausted window with missing reset must not affect",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
				SevenDay: window(40, nil),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(2 * time.Hour),
			wantOK:   true,
		},
		{
			name: "absent seven day does not block",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(2, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(2 * time.Hour),
			wantOK:   true,
		},
		{
			name: "page rounds to 99 percent after upstream rejection",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(99, at(1, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(time.Hour),
			wantOK:   true,
		},
		{
			name: "relative reset text resolved against fetched at",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: &OllamaCloudUsageWindow{UsedPercent: 100, ResetText: "in 3 hours."},
			}),
			minFresh: minFetchedAt,
			want:     now.Add(-time.Minute + 3*time.Hour),
			wantOK:   true,
		},
		{
			name: "monthly exhausted without balance blocks until refill",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				Monthly: &OllamaCloudUsageWindow{UsedPercent: 100, ResetText: "Refills in 1 week"},
				Balance: "$0.00",
			}),
			minFresh: minFetchedAt,
			want:     now.Add(-time.Minute + 7*24*time.Hour),
			wantOK:   true,
		},
		{
			name: "monthly exhausted with absolute reset and no balance field",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				Monthly: window(100, at(240, 0)),
			}),
			minFresh: minFetchedAt,
			want:     now.Add(240 * time.Hour),
			wantOK:   true,
		},
		{
			name: "exhausted window without any reset yields no recovery",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: &OllamaCloudUsageWindow{UsedPercent: 100, ResetText: "soon"},
			}),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
		{
			name: "future reset at now boundary treated as not future",
			snapshot: okSnapshot(&OllamaCloudUsageData{
				FiveHour: window(100, at(0, 0)),
			}),
			minFresh: minFetchedAt,
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testNow := tt.now
			if testNow.IsZero() {
				testNow = now
			}
			got, ok := ollamaCloudUsageExhaustionResetAt(tt.snapshot, testNow, tt.minFresh)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.True(t, got.Equal(tt.want), "reset = %v, want %v", got, tt.want)
			} else {
				require.True(t, got.IsZero())
			}
		})
	}
}

// TestOllamaCloudUsageExhaustionResetAtMissingFetchedAt checks the fetched-at
// gate separately because the okSnapshot helper always sets FetchedAt.
func TestParseOllamaRelativeDuration(t *testing.T) {
	for text, want := range map[string]time.Duration{
		"in 3 hours.":           3 * time.Hour,
		"in 2 hours 15 minutes": 2*time.Hour + 15*time.Minute,
		"Refills in 1 week":     7 * 24 * time.Hour,
		"in an hour":            time.Hour,
		"in 4 days":             96 * time.Hour,
		"in 1.5 hours":          90 * time.Minute,
	} {
		got, ok := parseOllamaRelativeDuration(text)
		require.True(t, ok, text)
		require.Equal(t, want, got, text)
	}
	_, ok := parseOllamaRelativeDuration("Jan 2, 2027")
	require.False(t, ok)
}

func TestOllamaCloudUsageHasPositiveBalance(t *testing.T) {
	require.True(t, ollamaCloudUsageHasPositiveBalance("$15.47"))
	require.True(t, ollamaCloudUsageHasPositiveBalance("USD 1,200"))
	require.False(t, ollamaCloudUsageHasPositiveBalance("$0.00"))
	require.False(t, ollamaCloudUsageHasPositiveBalance(""))
	require.False(t, ollamaCloudUsageHasPositiveBalance("-$3"))
}

func TestOllamaCloudUsageExhaustionResetAtMissingFetchedAt(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	snapshot := &OllamaCloudUsageSnapshot{
		Status: OllamaCloudUsageStatusOK,
		Data: &OllamaCloudUsageData{
			FiveHour: &OllamaCloudUsageWindow{UsedPercent: 100, ResetAt: mustT(now.Add(2 * time.Hour))},
		},
	}
	got, ok := ollamaCloudUsageExhaustionResetAt(snapshot, now, now.Add(-time.Hour))
	require.False(t, ok)
	require.True(t, got.IsZero())
}

// TestOllamaCloudUsageExhaustionResetAtFetchedBoundary pins the inclusive
// minFetchedAt boundary: a snapshot fetched exactly at the bound is usable.
func TestOllamaCloudUsageExhaustionResetAtFetchedBoundary(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	bound := now.Add(-time.Hour)
	snapshot := &OllamaCloudUsageSnapshot{
		Status:    OllamaCloudUsageStatusOK,
		FetchedAt: &bound, // exactly at the validity bound -> accepted
		Data: &OllamaCloudUsageData{
			FiveHour: &OllamaCloudUsageWindow{UsedPercent: 100, ResetAt: mustT(now.Add(2 * time.Hour))},
		},
	}
	got, ok := ollamaCloudUsageExhaustionResetAt(snapshot, now, bound)
	require.True(t, ok)
	require.True(t, got.Equal(now.Add(2*time.Hour)))
}

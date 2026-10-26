//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestKiroCreditWindowStart(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), kiroCreditWindowStart(&reset, now))

	past := now.Add(-time.Hour)
	require.Equal(t, now.Add(-30*24*time.Hour), kiroCreditWindowStart(&past, now))
	require.Equal(t, now.Add(-30*24*time.Hour), kiroCreditWindowStart(nil, now))
}

func TestAttachKiroCreditWindowStatsDoesNotMutateSharedCredit(t *testing.T) {
	repo := &usageLogWindowBatchRepoStub{singleResult: map[int64]*usagestats.AccountStats{
		7: {Requests: 12, Tokens: 3400, Cost: 1.5, StandardCost: 1.5, UserCost: 2},
	}}
	svc := &AccountUsageService{usageLogRepo: repo}
	shared := &KiroCreditProgress{CurrentUsage: 10, UsageLimit: 100, PercentageUsed: 10}
	usage := &UsageInfo{}
	usage.KiroCredit = shared

	svc.attachKiroCreditWindowStats(context.Background(), &Account{ID: 7, Platform: PlatformKiro}, usage, time.Now())

	require.NotNil(t, usage.KiroCredit.WindowStats)
	require.Equal(t, int64(12), usage.KiroCredit.WindowStats.Requests)
	require.Equal(t, 1.5, usage.KiroCredit.WindowStats.Cost)
	require.Nil(t, shared.WindowStats)
}

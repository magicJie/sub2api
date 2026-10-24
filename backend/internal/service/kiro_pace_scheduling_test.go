//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func kiroPaceCandidate(id int64) accountWithLoad {
	return accountWithLoad{
		account:  &Account{ID: id, Platform: PlatformKiro, Type: AccountTypeAPIKey},
		loadInfo: &AccountLoadInfo{AccountID: id},
	}
}

func setKiroPace(t *testing.T, id int64, used float64, resetIn time.Duration) {
	t.Helper()
	reset := time.Now().Add(resetIn)
	recordKiroPaceSnapshot(id, &UsageInfo{
		KiroCredit:  &KiroCreditProgress{CurrentUsage: used * 50, UsageLimit: 5000, PercentageUsed: used},
		KiroResetAt: &reset,
	}, time.Now())
	t.Cleanup(func() { kiroPaceSnapshots.Delete(id) })
}

func ids(accounts []accountWithLoad) []int64 {
	out := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, a.account.ID)
	}
	return out
}

func TestFilterByKiroPacePrefersMoreRemainingPerHour(t *testing.T) {
	// 与 magpie 实测数据同形：8% / 96% / 98% 已用，同一天重置。
	setKiroPace(t, 9001, 8, 7*24*time.Hour)
	setKiroPace(t, 9002, 96, 7*24*time.Hour)
	setKiroPace(t, 9003, 98.4, 7*24*time.Hour)
	got := filterByKiroPace([]accountWithLoad{kiroPaceCandidate(9002), kiroPaceCandidate(9001), kiroPaceCandidate(9003)})
	require.Equal(t, []int64{9001}, ids(got))
}

func TestFilterByKiroPaceSpendsSoonerResetFirst(t *testing.T) {
	// 同样剩 50%：两天后重置的比二十天后重置的更该先用（快要作废）。
	setKiroPace(t, 9011, 50, 2*24*time.Hour)
	setKiroPace(t, 9012, 50, 20*24*time.Hour)
	got := filterByKiroPace([]accountWithLoad{kiroPaceCandidate(9012), kiroPaceCandidate(9011)})
	require.Equal(t, []int64{9011}, ids(got))
}

func TestFilterByKiroPaceKeepsNearTiesForLoadBalancing(t *testing.T) {
	setKiroPace(t, 9021, 40, 10*24*time.Hour)
	setKiroPace(t, 9022, 44, 10*24*time.Hour) // 相差 <10%，同一档
	setKiroPace(t, 9023, 80, 10*24*time.Hour)
	got := filterByKiroPace([]accountWithLoad{kiroPaceCandidate(9021), kiroPaceCandidate(9022), kiroPaceCandidate(9023)})
	require.ElementsMatch(t, []int64{9021, 9022}, ids(got))
}

func TestFilterByKiroPaceAllLowPicksLeastUsed(t *testing.T) {
	setKiroPace(t, 9031, 99, 24*time.Hour)
	setKiroPace(t, 9032, 92, 24*time.Hour)
	got := filterByKiroPace([]accountWithLoad{kiroPaceCandidate(9031), kiroPaceCandidate(9032)})
	require.Equal(t, []int64{9032}, ids(got))
}

func TestFilterByKiroPaceUnknownAccountIsNotStarved(t *testing.T) {
	// 没有快照的账号按「30 天用完」估算；比快要见底的账号优先，比余量充足且快重置的账号靠后。
	setKiroPace(t, 9041, 95, 10*24*time.Hour)
	got := filterByKiroPace([]accountWithLoad{kiroPaceCandidate(9041), kiroPaceCandidate(9042)})
	require.Equal(t, []int64{9042}, ids(got))

	setKiroPace(t, 9043, 10, 3*24*time.Hour)
	got = filterByKiroPace([]accountWithLoad{kiroPaceCandidate(9043), kiroPaceCandidate(9044)})
	require.Equal(t, []int64{9043}, ids(got))
}

func TestFilterByKiroPaceTreatsPastResetAsFull(t *testing.T) {
	// 快照里是 99% 已用，但重置时间已过：额度已回满，不能再当成耗尽账号排到最后。
	setKiroPace(t, 9051, 99, -time.Hour)
	setKiroPace(t, 9052, 95, 5*24*time.Hour)
	got := filterByKiroPace([]accountWithLoad{kiroPaceCandidate(9051), kiroPaceCandidate(9052)})
	require.Equal(t, []int64{9051}, ids(got))
}

func TestFilterByKiroPaceIgnoresMixedPlatforms(t *testing.T) {
	setKiroPace(t, 9061, 90, time.Hour)
	other := accountWithLoad{account: &Account{ID: 9062, Platform: PlatformAnthropic}, loadInfo: &AccountLoadInfo{}}
	in := []accountWithLoad{kiroPaceCandidate(9061), other}
	require.Equal(t, ids(in), ids(filterByKiroPace(in)))
}

func TestFilterByKiroPaceCanBeDisabled(t *testing.T) {
	t.Setenv("SUB2API_KIRO_PACE_SCHEDULING", "off")
	setKiroPace(t, 9071, 8, 24*time.Hour)
	setKiroPace(t, 9072, 96, 24*time.Hour)
	in := []accountWithLoad{kiroPaceCandidate(9071), kiroPaceCandidate(9072)}
	require.Equal(t, ids(in), ids(filterByKiroPace(in)))
}

func TestKiroStickyAccountSpentReleasesOnlyWithAlternative(t *testing.T) {
	setKiroPace(t, 9081, 98.5, 5*24*time.Hour)
	setKiroPace(t, 9082, 40, 5*24*time.Hour)
	sticky := &Account{ID: 9081, Platform: PlatformKiro, Status: StatusActive, Schedulable: true}
	other := Account{ID: 9082, Platform: PlatformKiro, Status: StatusActive, Schedulable: true}
	require.True(t, kiroStickyAccountSpent(sticky, []Account{*sticky, other}))
	require.False(t, kiroStickyAccountSpent(sticky, []Account{*sticky}), "没有可接手的账号时保持粘性")

	setKiroPace(t, 9083, 70, 5*24*time.Hour)
	healthy := &Account{ID: 9083, Platform: PlatformKiro, Status: StatusActive, Schedulable: true}
	require.False(t, kiroStickyAccountSpent(healthy, []Account{*healthy, other}))
}

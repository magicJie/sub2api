//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type scheduledBatchPlanRepo struct {
	ScheduledTestPlanRepository
	plans       map[int64][]*ScheduledTestPlan
	failAccount int64
}

func (r *scheduledBatchPlanRepo) ListByAccountID(_ context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return r.plans[accountID], nil
}

func (r *scheduledBatchPlanRepo) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if plan.AccountID == r.failAccount {
		return nil, errors.New("account unavailable")
	}
	saved := *plan
	saved.ID = int64(len(r.plans[plan.AccountID]) + 1)
	r.plans[plan.AccountID] = append(r.plans[plan.AccountID], &saved)
	return &saved, nil
}

func TestScheduledTestBatchPartialFailureAndRetry(t *testing.T) {
	repo := &scheduledBatchPlanRepo{plans: make(map[int64][]*ScheduledTestPlan), failAccount: 2}
	svc := NewScheduledTestService(repo, nil)
	config := ScheduledTestPlan{ModelID: " model ", TestType: "quality", CronExpression: "0 * * * *", Enabled: true}
	first, err := svc.BatchCreatePlans(context.Background(), []int64{1, 2, 1, 3}, config)
	require.NoError(t, err)
	require.Equal(t, 2, first.Created)
	require.Equal(t, 1, first.Failed)
	require.Equal(t, 0, first.Skipped)
	require.Len(t, first.Results, 3)
	require.Equal(t, int64(2), first.Results[1].AccountID)
	require.Equal(t, "account unavailable", first.Results[1].Error)
	require.Empty(t, repo.plans[2])
	require.Equal(t, "model", repo.plans[1][0].ModelID)

	repo.failAccount = 0
	retried, err := svc.BatchCreatePlans(context.Background(), []int64{1, 2, 3}, config)
	require.NoError(t, err)
	require.Equal(t, 1, retried.Created)
	require.Equal(t, 2, retried.Skipped)
	require.Zero(t, retried.Failed)
	for _, accountID := range []int64{1, 2, 3} {
		require.Len(t, repo.plans[accountID], 1)
	}
}

func TestScheduledTestBatchKeepsDifferentPlans(t *testing.T) {
	repo := &scheduledBatchPlanRepo{plans: make(map[int64][]*ScheduledTestPlan)}
	svc := NewScheduledTestService(repo, nil)
	config := ScheduledTestPlan{ModelID: "model", TestType: "quality", QualityThreshold: 80, CronExpression: "0 * * * *", Enabled: false}
	_, err := svc.BatchCreatePlans(context.Background(), []int64{1}, config)
	require.NoError(t, err)
	config.Enabled = true
	second, err := svc.BatchCreatePlans(context.Background(), []int64{1}, config)
	require.NoError(t, err)
	require.Equal(t, 1, second.Created)
	config.QualityThreshold = 100
	third, err := svc.BatchCreatePlans(context.Background(), []int64{1}, config)
	require.NoError(t, err)
	require.Equal(t, 1, third.Created)
	require.Len(t, repo.plans[1], 3)
	require.False(t, repo.plans[1][0].Enabled)
	require.Equal(t, 80, repo.plans[1][1].QualityThreshold)
}

func TestScheduledTestBatchValidatesBeforeCreatingAnyPlan(t *testing.T) {
	for _, config := range []ScheduledTestPlan{
		{ModelID: "model", TestType: "quality", CronExpression: "invalid"},
		{TestType: "quality", CronExpression: "0 * * * *"},
		{ModelID: "model", TestType: "quality", CronExpression: "0 * * * *", AutoRecover: true},
	} {
		repo := &scheduledBatchPlanRepo{plans: make(map[int64][]*ScheduledTestPlan)}
		_, err := NewScheduledTestService(repo, nil).BatchCreatePlans(context.Background(), []int64{1, 2}, config)
		require.Error(t, err)
		require.Empty(t, repo.plans)
	}
}

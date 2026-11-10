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

func (r *scheduledBatchPlanRepo) Update(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	for i, existing := range r.plans[plan.AccountID] {
		if existing.ID == plan.ID {
			r.plans[plan.AccountID][i] = plan
			return plan, nil
		}
	}
	return nil, errors.New("plan unavailable")
}

func (r *scheduledBatchPlanRepo) Delete(_ context.Context, id int64) error {
	for accountID, plans := range r.plans {
		for i, plan := range plans {
			if plan.ID == id {
				r.plans[accountID] = append(plans[:i], plans[i+1:]...)
				return nil
			}
		}
	}
	return errors.New("plan unavailable")
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

func TestScheduledTestBatchUpsert(t *testing.T) {
	template := ScheduledTestPlan{
		ModelID: "model", TestType: "fingerprint", ExpectedModel: "reference",
		QualityThreshold: 80, CronExpression: "0 */6 * * *", Enabled: true, MaxResults: 50,
	}
	for _, tc := range []struct {
		name    string
		count   int
		oldCron string
		status  string
		removed int
	}{
		{"create", 0, "", "created", 0},
		{"update", 1, "0 * * * *", "updated", 0},
		{"remove duplicates", 3, template.CronExpression, "updated", 2},
		{"skip identical", 1, template.CronExpression, "skipped", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := template
			other.ID, other.AccountID, other.TestType = 10, 1, "quality"
			repo := &scheduledBatchPlanRepo{plans: map[int64][]*ScheduledTestPlan{1: {&other}}}
			for i := range tc.count {
				plan := template
				plan.ID, plan.AccountID, plan.CronExpression = int64(3-i), 1, tc.oldCron
				repo.plans[1] = append(repo.plans[1], &plan)
			}
			result, err := NewScheduledTestService(repo, nil).BatchUpsertPlans(context.Background(), []int64{1, 1}, template)
			require.NoError(t, err)
			require.Len(t, result.Results, 1)
			item := result.Results[0]
			require.Equal(t, tc.status, item.Status)
			require.Equal(t, tc.removed, item.Removed)
			require.Equal(t, template.CronExpression, item.Plan.CronExpression)
			if tc.count > 0 {
				require.Equal(t, int64(3), item.Plan.ID)
			}
			require.Len(t, repo.plans[1], 2)
			require.Equal(t, &other, repo.plans[1][0])
			require.Equal(t, "quality", other.TestType)
			require.Equal(t, template.CronExpression, other.CronExpression)
			switch tc.status {
			case "created":
				require.Equal(t, 1, result.Created)
			case "updated":
				require.Equal(t, 1, result.Updated)
			case "skipped":
				require.Equal(t, 1, result.Skipped)
			}
		})
	}
}

func TestScheduledTestBatchUpsertValidatesBeforeWriting(t *testing.T) {
	plan := &ScheduledTestPlan{ID: 3, AccountID: 1, TestType: "fingerprint", CronExpression: "0 * * * *"}
	repo := &scheduledBatchPlanRepo{plans: map[int64][]*ScheduledTestPlan{1: {plan}}}
	_, err := NewScheduledTestService(repo, nil).BatchUpsertPlans(context.Background(), []int64{1, 2},
		ScheduledTestPlan{ModelID: "model", TestType: "fingerprint", CronExpression: "invalid"})
	require.Error(t, err)
	require.Equal(t, map[int64][]*ScheduledTestPlan{1: {plan}}, repo.plans)
	require.Equal(t, "0 * * * *", plan.CronExpression)
}

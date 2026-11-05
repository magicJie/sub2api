//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scheduledValidationPlanRepo struct {
	ScheduledTestPlanRepository
	saved *ScheduledTestPlan
}

func (r *scheduledValidationPlanRepo) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func (r *scheduledValidationPlanRepo) Update(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func TestScheduledTestCreatePlanDefaults(t *testing.T) {
	repo := &scheduledValidationPlanRepo{}
	svc := NewScheduledTestService(repo, nil)
	plan, err := svc.CreatePlan(context.Background(), &ScheduledTestPlan{CronExpression: "*/30 * * * *", AutoRecover: true})
	require.NoError(t, err)
	require.Equal(t, "connectivity", plan.TestType)
	require.Equal(t, 80, plan.QualityThreshold)
	require.Equal(t, 50, plan.MaxResults)
	require.True(t, plan.AutoRecover)
	require.NotNil(t, plan.NextRunAt)
	require.Same(t, plan, repo.saved)
}

func TestScheduledTestPlanValidation(t *testing.T) {
	cases := []struct {
		name    string
		change  func(*ScheduledTestPlan)
		message string
	}{
		{"unknown type", func(p *ScheduledTestPlan) { p.TestType = "other" }, "invalid test_type"},
		{"fingerprint requires model", func(p *ScheduledTestPlan) { p.TestType = "fingerprint"; p.ModelID = " " }, "model_id is required"},
		{"quality requires model", func(p *ScheduledTestPlan) { p.TestType = "quality"; p.ModelID = "" }, "model_id is required"},
		{"comprehensive requires model", func(p *ScheduledTestPlan) { p.TestType = "comprehensive"; p.ModelID = "" }, "model_id is required"},
		{"diagnostic auto recovery", func(p *ScheduledTestPlan) { p.AutoRecover = true }, "auto_recover is only available"},
		{"low threshold", func(p *ScheduledTestPlan) { p.QualityThreshold = -1 }, "quality_threshold"},
		{"high threshold", func(p *ScheduledTestPlan) { p.QualityThreshold = 101 }, "quality_threshold"},
		{"low retention", func(p *ScheduledTestPlan) { p.MaxResults = -1 }, "max_results"},
		{"high retention", func(p *ScheduledTestPlan) { p.MaxResults = 1001 }, "max_results"},
		{"invalid cron", func(p *ScheduledTestPlan) { p.CronExpression = "invalid" }, "invalid cron expression"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, update := range []bool{false, true} {
				repo := &scheduledValidationPlanRepo{}
				svc := NewScheduledTestService(repo, nil)
				plan := &ScheduledTestPlan{TestType: "quality", ModelID: "model", QualityThreshold: 80, MaxResults: 50, CronExpression: "*/30 * * * *"}
				tc.change(plan)
				var err error
				if update {
					_, err = svc.UpdatePlan(context.Background(), plan)
				} else {
					_, err = svc.CreatePlan(context.Background(), plan)
				}
				require.ErrorContains(t, err, tc.message)
				require.Nil(t, repo.saved)
			}
		})
	}
}

func TestScheduledTestDiagnosticPlanTypes(t *testing.T) {
	for _, testType := range []string{"fingerprint", "quality", "comprehensive"} {
		t.Run(testType, func(t *testing.T) {
			repo := &scheduledValidationPlanRepo{}
			svc := NewScheduledTestService(repo, nil)
			plan, err := svc.CreatePlan(context.Background(), &ScheduledTestPlan{
				TestType: testType, ModelID: " model ", ExpectedModel: " reference ",
				QualityThreshold: 100, MaxResults: 1000, CronExpression: "*/30 * * * *",
			})
			require.NoError(t, err)
			require.Equal(t, "model", plan.ModelID)
			require.Equal(t, "reference", plan.ExpectedModel)
			require.Equal(t, testType, plan.TestType)
			plan.QualityThreshold, plan.MaxResults = 1, 1
			_, err = svc.UpdatePlan(context.Background(), plan)
			require.NoError(t, err)
			plan.QualityThreshold = 0
			_, err = svc.UpdatePlan(context.Background(), plan)
			require.ErrorContains(t, err, "quality_threshold")
			plan.QualityThreshold, plan.MaxResults = 80, 0
			_, err = svc.UpdatePlan(context.Background(), plan)
			require.ErrorContains(t, err, "max_results")
		})
	}
}

type scheduledPersistResultRepo struct {
	ScheduledTestResultRepository
	pruneErr   error
	prunedPlan int64
	keepCount  int
}

func (r *scheduledPersistResultRepo) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	saved := *result
	saved.ID = 42
	saved.CreatedAt = time.Unix(1000, 0)
	return &saved, nil
}

func (r *scheduledPersistResultRepo) PruneOldResults(_ context.Context, planID int64, count int) error {
	r.prunedPlan, r.keepCount = planID, count
	return r.pruneErr
}

func TestScheduledTestSaveResultReturnsPersistedFields(t *testing.T) {
	repo := &scheduledPersistResultRepo{}
	svc := NewScheduledTestService(nil, repo)
	report := &ModelDetectionReport{Version: "test", TestType: "quality"}
	result := &ScheduledTestResult{Status: "warning", Diagnostics: report}
	require.NoError(t, svc.SaveResult(context.Background(), 7, 20, result))
	require.Equal(t, int64(42), result.ID)
	require.Equal(t, int64(7), result.PlanID)
	require.Equal(t, time.Unix(1000, 0), result.CreatedAt)
	require.Same(t, report, result.Diagnostics)
	require.Equal(t, int64(7), repo.prunedPlan)
	require.Equal(t, 20, repo.keepCount)
	repo.pruneErr = errors.New("prune failed")
	require.ErrorIs(t, svc.SaveResult(context.Background(), 7, 20, result), repo.pruneErr)
}

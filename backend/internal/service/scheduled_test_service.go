package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo   ScheduledTestPlanRepository
	resultRepo ScheduledTestResultRepository
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// CreatePlan validates the plan, computes next_run_at, and persists it.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := prepareScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	return s.planRepo.Create(ctx, plan)
}

func prepareScheduledTestPlan(plan *ScheduledTestPlan) error {
	if plan.MaxResults == 0 {
		plan.MaxResults = 50
	}
	if plan.QualityThreshold == 0 {
		plan.QualityThreshold = 80
	}
	if err := validateScheduledTestPlan(plan); err != nil {
		return err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun
	return nil
}

// BatchCreatePlans applies one configuration per account, preserving existing
// plans and reporting account-local failures without undoing successful creates.
func (s *ScheduledTestService) BatchCreatePlans(ctx context.Context, accountIDs []int64, template ScheduledTestPlan) (*ScheduledTestBatchResult, error) {
	if err := prepareScheduledTestPlan(&template); err != nil {
		return nil, err
	}
	result := &ScheduledTestBatchResult{Results: make([]ScheduledTestBatchItem, 0, len(accountIDs))}
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		item := ScheduledTestBatchItem{AccountID: accountID}
		existing, err := s.planRepo.ListByAccountID(ctx, accountID)
		if err == nil {
			for _, plan := range existing {
				if plan.ModelID == template.ModelID && plan.TestType == template.TestType &&
					plan.ExpectedModel == template.ExpectedModel && plan.QualityThreshold == template.QualityThreshold &&
					plan.CronExpression == template.CronExpression && plan.Enabled == template.Enabled &&
					plan.MaxResults == template.MaxResults && plan.AutoRecover == template.AutoRecover {
					item.Status, item.Plan = "skipped", plan
					break
				}
			}
			if item.Status == "" {
				plan := template
				plan.AccountID = accountID
				item.Plan, err = s.planRepo.Create(ctx, &plan)
				if err == nil {
					item.Status = "created"
				}
			}
		}
		if err != nil {
			item.Status, item.Error = "failed", err.Error()
		}
		switch item.Status {
		case "created":
			result.Created++
		case "skipped":
			result.Skipped++
		case "failed":
			result.Failed++
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan validates the plan and updates its next execution time.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := validateScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult inserts a result and prunes old entries beyond maxResults.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	result.PlanID = planID
	saved, err := s.resultRepo.Create(ctx, result)
	if err != nil {
		return err
	}
	*result = *saved
	return s.resultRepo.PruneOldResults(ctx, planID, maxResults)
}

func validateScheduledTestPlan(plan *ScheduledTestPlan) error {
	plan.TestType = strings.TrimSpace(plan.TestType)
	plan.ModelID = strings.TrimSpace(plan.ModelID)
	plan.ExpectedModel = strings.TrimSpace(plan.ExpectedModel)
	if plan.TestType == "" {
		plan.TestType = "connectivity"
	}
	switch plan.TestType {
	case "connectivity":
	case "fingerprint", "quality", "comprehensive":
		if plan.ModelID == "" {
			return fmt.Errorf("model_id is required for diagnostic tests")
		}
		if plan.AutoRecover {
			return fmt.Errorf("auto_recover is only available for connectivity tests")
		}
	default:
		return fmt.Errorf("invalid test_type: %s", plan.TestType)
	}
	if plan.QualityThreshold < 1 || plan.QualityThreshold > 100 {
		return fmt.Errorf("quality_threshold must be between 1 and 100")
	}
	if plan.MaxResults < 1 || plan.MaxResults > 1000 {
		return fmt.Errorf("max_results must be between 1 and 1000")
	}
	return nil
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}

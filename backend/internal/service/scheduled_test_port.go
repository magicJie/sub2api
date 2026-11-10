package service

import (
	"context"
	"time"
)

// ScheduledTestPlan represents a scheduled test plan domain model.
type ScheduledTestPlan struct {
	ID               int64      `json:"id"`
	AccountID        int64      `json:"account_id"`
	ModelID          string     `json:"model_id"`
	TestType         string     `json:"test_type"`
	ExpectedModel    string     `json:"expected_model"`
	QualityThreshold int        `json:"quality_threshold"`
	CronExpression   string     `json:"cron_expression"`
	Enabled          bool       `json:"enabled"`
	MaxResults       int        `json:"max_results"`
	AutoRecover      bool       `json:"auto_recover"`
	LastRunAt        *time.Time `json:"last_run_at"`
	NextRunAt        *time.Time `json:"next_run_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type ScheduledTestBatchItem struct {
	AccountID int64              `json:"account_id"`
	Status    string             `json:"status"`
	Plan      *ScheduledTestPlan `json:"plan,omitempty"`
	Removed   int                `json:"removed,omitempty"`
	Error     string             `json:"error,omitempty"`
}

type ScheduledTestBatchResult struct {
	Created int                      `json:"created"`
	Updated int                      `json:"updated"`
	Skipped int                      `json:"skipped"`
	Failed  int                      `json:"failed"`
	Results []ScheduledTestBatchItem `json:"results"`
}

// ScheduledTestResult represents a single test execution result.
type ScheduledTestResult struct {
	ID           int64                 `json:"id"`
	PlanID       int64                 `json:"plan_id"`
	Status       string                `json:"status"`
	ResponseText string                `json:"response_text"`
	ErrorMessage string                `json:"error_message"`
	LatencyMs    int64                 `json:"latency_ms"`
	Diagnostics  *ModelDetectionReport `json:"diagnostics,omitempty"`
	StartedAt    time.Time             `json:"started_at"`
	FinishedAt   time.Time             `json:"finished_at"`
	CreatedAt    time.Time             `json:"created_at"`
}

type ScheduledTestLogFilter struct {
	Platform          string
	Status            string
	TestType          string
	FingerprintStatus string
	AccountID         int64
	Start             *time.Time
	End               *time.Time
	Page              int
	PageSize          int
}

type ScheduledTestLogItem struct {
	ScheduledTestResult
	AccountID     int64  `json:"account_id"`
	AccountName   string `json:"account_name"`
	Platform      string `json:"platform"`
	ModelID       string `json:"model_id"`
	TestType      string `json:"test_type"`
	ExpectedModel string `json:"expected_model"`
}

// ScheduledTestPlanRepository defines the data access interface for test plans.
type ScheduledTestPlanRepository interface {
	Create(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	GetByID(ctx context.Context, id int64) (*ScheduledTestPlan, error)
	ListByAccountID(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error)
	ListDue(ctx context.Context, now time.Time) ([]*ScheduledTestPlan, error)
	Update(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	Delete(ctx context.Context, id int64) error
	UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time) error
}

// ScheduledTestResultRepository defines the data access interface for test results.
type ScheduledTestResultRepository interface {
	Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error)
	ListByPlanID(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error)
	ListLogs(ctx context.Context, filter ScheduledTestLogFilter) ([]*ScheduledTestLogItem, int64, error)
	PruneOldResults(ctx context.Context, planID int64, keepCount int) error
}

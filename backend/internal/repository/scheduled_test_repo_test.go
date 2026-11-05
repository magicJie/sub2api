package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var scheduledPlanColumns = []string{"id", "account_id", "model_id", "cron_expression", "enabled", "max_results", "auto_recover", "test_type", "expected_model", "quality_threshold", "last_run_at", "next_run_at", "created_at", "updated_at"}
var scheduledResultColumns = []string{"id", "plan_id", "status", "response_text", "error_message", "latency_ms", "started_at", "finished_at", "created_at", "diagnostics"}

func TestScheduledTestPlanRepositoryDiagnosticFields(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	plan := &service.ScheduledTestPlan{ID: 7, AccountID: 9, ModelID: "model", TestType: "comprehensive", ExpectedModel: "reference", QualityThreshold: 90, CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 50, NextRunAt: &now, CreatedAt: now, UpdatedAt: now}
	values := []driver.Value{plan.ID, plan.AccountID, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.TestType, plan.ExpectedModel, plan.QualityThreshold, nil, now, now, now}
	selection := "id, account_id, model_id, cron_expression, enabled, max_results, auto_recover, test_type, expected_model, quality_threshold, last_run_at, next_run_at, created_at, updated_at"
	for _, operation := range []string{"create", "update", "get", "list account", "list due"} {
		t.Run(operation, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := NewScheduledTestPlanRepository(db)
			rows := sqlmock.NewRows(scheduledPlanColumns).AddRow(values...)
			var got *service.ScheduledTestPlan
			var err error
			switch operation {
			case "create":
				mock.ExpectQuery("(?s)INSERT INTO scheduled_test_plans.*"+regexp.QuoteMeta("test_type, expected_model, quality_threshold")+".*RETURNING "+regexp.QuoteMeta(selection)).
					WithArgs(plan.AccountID, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, plan.TestType, plan.ExpectedModel, plan.QualityThreshold).WillReturnRows(rows)
				got, err = repo.Create(context.Background(), plan)
			case "update":
				mock.ExpectQuery("(?s)UPDATE scheduled_test_plans.*"+regexp.QuoteMeta("test_type = $8, expected_model = $9, quality_threshold = $10")+".*RETURNING "+regexp.QuoteMeta(selection)).
					WithArgs(plan.ID, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, plan.TestType, plan.ExpectedModel, plan.QualityThreshold).WillReturnRows(rows)
				got, err = repo.Update(context.Background(), plan)
			case "get":
				mock.ExpectQuery(regexp.QuoteMeta("SELECT " + selection + " FROM scheduled_test_plans WHERE id = $1")).WithArgs(plan.ID).WillReturnRows(rows)
				got, err = repo.GetByID(context.Background(), plan.ID)
			case "list account":
				mock.ExpectQuery(regexp.QuoteMeta("SELECT " + selection + " FROM scheduled_test_plans WHERE account_id = $1")).WithArgs(plan.AccountID).WillReturnRows(rows)
				var plans []*service.ScheduledTestPlan
				plans, err = repo.ListByAccountID(context.Background(), plan.AccountID)
				require.NoError(t, err)
				require.Len(t, plans, 1)
				got = plans[0]
			case "list due":
				mock.ExpectQuery(regexp.QuoteMeta("SELECT " + selection + " FROM scheduled_test_plans WHERE enabled = true AND next_run_at <= $1")).WithArgs(now).WillReturnRows(rows)
				var plans []*service.ScheduledTestPlan
				plans, err = repo.ListDue(context.Background(), now)
				require.NoError(t, err)
				require.Len(t, plans, 1)
				got = plans[0]
			}
			require.NoError(t, err)
			require.Equal(t, plan, got)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestScheduledTestResultRepositoryDiagnosticsRoundTrip(t *testing.T) {
	var report service.ModelDetectionReport
	require.NoError(t, json.Unmarshal([]byte(`{"version":"quality-v1/bank-test","test_type":"comprehensive","model":"model","expected_model":"reference","fingerprint":{"status":"mismatch","prediction":"other","probability":0.95,"used_outputs":3,"candidates":[{"model":"other","probability":0.95,"family":"family"}],"error":"evidence"},"quality":{"status":"degraded","score":60,"threshold":80,"passed":3,"total":5,"checks":[{"id":"math","passed":false,"expected":"42","actual":"41"}],"error":"quality evidence"}}`), &report))
	for _, diagnostics := range []*service.ModelDetectionReport{nil, &report} {
		name := "legacy null"
		if diagnostics != nil {
			name = "structured report"
		}
		t.Run(name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := NewScheduledTestResultRepository(db)
			now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
			result := &service.ScheduledTestResult{ID: 11, PlanID: 7, Status: "warning", ResponseText: "response", ErrorMessage: "error", LatencyMs: 12, StartedAt: now, FinishedAt: now, CreatedAt: now, Diagnostics: diagnostics}
			var raw driver.Value
			if diagnostics != nil {
				encoded, err := json.Marshal(diagnostics)
				require.NoError(t, err)
				raw = string(encoded)
			}
			values := []driver.Value{result.ID, result.PlanID, result.Status, result.ResponseText, result.ErrorMessage, result.LatencyMs, now, now, now, raw}
			mock.ExpectQuery("(?s)INSERT INTO scheduled_test_results.*diagnostics.*RETURNING.*diagnostics").
				WithArgs(result.PlanID, result.Status, result.ResponseText, result.ErrorMessage, result.LatencyMs, now, now, raw).
				WillReturnRows(sqlmock.NewRows(scheduledResultColumns).AddRow(values...))
			created, err := repo.Create(context.Background(), result)
			require.NoError(t, err)
			require.Equal(t, result, created)
			mock.ExpectQuery("(?s)SELECT.*diagnostics.*FROM scheduled_test_results").WithArgs(result.PlanID, 50).
				WillReturnRows(sqlmock.NewRows(scheduledResultColumns).AddRow(values...))
			listed, err := repo.ListByPlanID(context.Background(), result.PlanID, 50)
			require.NoError(t, err)
			require.Equal(t, []*service.ScheduledTestResult{result}, listed)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

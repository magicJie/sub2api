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

func TestScheduledTestResultRepositoryListLogs(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewScheduledTestResultRepository(db)
	filter := service.ScheduledTestLogFilter{
		Platform: "openai", Status: "abnormal", FingerprintStatus: "mismatch", Page: 2, PageSize: 50,
	}
	from := ` FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id = r.plan_id
		JOIN accounts a ON a.id = p.account_id WHERE a.platform = $1 AND r.status IN ('warning','failed')
		AND r.diagnostics->'fingerprint'->>'status' = $2`
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)"+from)).
		WithArgs("openai", "mismatch").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(51))
	selection := `SELECT r.id, r.plan_id, r.status, r.error_message, r.latency_ms, r.started_at,
		r.finished_at, r.created_at, r.diagnostics, p.account_id, p.model_id, p.test_type,
		p.expected_model, a.name, a.platform`
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	columns := []string{"id", "plan_id", "status", "error_message", "latency_ms", "started_at",
		"finished_at", "created_at", "diagnostics", "account_id", "model_id", "test_type", "expected_model", "name", "platform"}
	mock.ExpectQuery(regexp.QuoteMeta(selection+from+" ORDER BY r.created_at DESC, r.id DESC LIMIT $3 OFFSET $4")).
		WithArgs("openai", "mismatch", 50, 50).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(11, 7, "warning", "mismatch evidence", 120,
			now, now, now, `{"version":"v1","test_type":"fingerprint","fingerprint":{"status":"mismatch","prediction":"other","probability":0.95}}`,
			9, "requested", "fingerprint", "expected", "Account Nine", "openai"))
	items, total, err := repo.ListLogs(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, int64(51), total)
	require.Len(t, items, 1)
	require.Equal(t, int64(9), items[0].AccountID)
	require.Equal(t, "Account Nine", items[0].AccountName)
	require.Equal(t, "requested", items[0].ModelID)
	require.Equal(t, "expected", items[0].ExpectedModel)
	require.Equal(t, "mismatch", items[0].Diagnostics.Fingerprint.Status)
	require.Equal(t, "other", items[0].Diagnostics.Fingerprint.Prediction)
	require.Empty(t, items[0].ResponseText)
	require.NoError(t, mock.ExpectationsWereMet())
}

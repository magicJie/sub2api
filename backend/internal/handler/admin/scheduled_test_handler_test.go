//go:build unit

package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type scheduledHandlerPlanRepo struct {
	service.ScheduledTestPlanRepository
	plan  *service.ScheduledTestPlan
	saved *service.ScheduledTestPlan
}

func (r *scheduledHandlerPlanRepo) GetByID(_ context.Context, _ int64) (*service.ScheduledTestPlan, error) {
	if r.plan == nil {
		return nil, sql.ErrNoRows
	}
	copy := *r.plan
	return &copy, nil
}

func (r *scheduledHandlerPlanRepo) Create(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func (r *scheduledHandlerPlanRepo) Update(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func (r *scheduledHandlerPlanRepo) ListByAccountID(_ context.Context, _ int64) ([]*service.ScheduledTestPlan, error) {
	if r.plan == nil {
		return nil, nil
	}
	return []*service.ScheduledTestPlan{r.plan}, nil
}

func TestScheduledTestHandlerCreateDiagnostics(t *testing.T) {
	for _, body := range []string{
		`{"account_id":9,"cron_expression":"*/30 * * * *"}`,
		`{"account_id":9,"cron_expression":"*/30 * * * *","test_type":"comprehensive","model_id":"model","expected_model":"reference","quality_threshold":90}`,
	} {
		repo := &scheduledHandlerPlanRepo{}
		h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil), nil)
		router := gin.New()
		router.POST("/plans", h.Create)
		request := httptest.NewRequest(http.MethodPost, "/plans", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var result service.ScheduledTestPlan
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
		require.Equal(t, repo.saved.TestType, result.TestType)
		require.Equal(t, repo.saved.ExpectedModel, result.ExpectedModel)
		require.Equal(t, repo.saved.QualityThreshold, result.QualityThreshold)
		require.Equal(t, 50, result.MaxResults)
		require.True(t, result.Enabled)
	}
}

func TestScheduledTestHandlerUpdatePartialFields(t *testing.T) {
	repo := &scheduledHandlerPlanRepo{plan: &service.ScheduledTestPlan{
		ID: 7, AccountID: 9, ModelID: "model", TestType: "connectivity", ExpectedModel: "reference",
		QualityThreshold: 90, CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 100, AutoRecover: true,
	}}
	h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil), nil)
	router := gin.New()
	router.PUT("/plans/:id", h.Update)
	request := httptest.NewRequest(http.MethodPut, "/plans/7", strings.NewReader(`{"model_id":"","expected_model":"","enabled":false,"auto_recover":false}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Empty(t, repo.saved.ModelID)
	require.Empty(t, repo.saved.ExpectedModel)
	require.False(t, repo.saved.Enabled)
	require.False(t, repo.saved.AutoRecover)
	require.Equal(t, "connectivity", repo.saved.TestType)
	require.Equal(t, 90, repo.saved.QualityThreshold)
	require.Equal(t, 100, repo.saved.MaxResults)
	require.Equal(t, "*/30 * * * *", repo.saved.CronExpression)
}

func TestScheduledTestHandlerUpdateRejectsExplicitInvalidValues(t *testing.T) {
	for _, body := range []string{
		`{"quality_threshold":0}`, `{"max_results":0}`, `{"cron_expression":""}`,
		`{"test_type":"quality","model_id":""}`, `{"test_type":"quality","auto_recover":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			repo := &scheduledHandlerPlanRepo{plan: &service.ScheduledTestPlan{ID: 7, ModelID: "model", TestType: "connectivity", QualityThreshold: 80, MaxResults: 50, CronExpression: "*/30 * * * *"}}
			h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil), nil)
			router := gin.New()
			router.PUT("/plans/:id", h.Update)
			request := httptest.NewRequest(http.MethodPut, "/plans/7", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			require.Nil(t, repo.saved)
		})
	}
}

func TestScheduledTestHandlerRunNowRejectsInvalidOrMissingPlan(t *testing.T) {
	for _, id := range []string{"invalid", "0", "-1", "7"} {
		t.Run(id, func(t *testing.T) {
			h := NewScheduledTestHandler(service.NewScheduledTestService(&scheduledHandlerPlanRepo{}, nil), nil)
			router := gin.New()
			router.POST("/plans/:id/run", h.RunNow)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/plans/"+id+"/run", nil))
			expected := http.StatusBadRequest
			if id == "7" {
				expected = http.StatusNotFound
			}
			require.Equal(t, expected, recorder.Code, recorder.Body.String())
		})
	}
}

func TestScheduledTestHandlerBatchEdit(t *testing.T) {
	repo := &scheduledHandlerPlanRepo{plan: &service.ScheduledTestPlan{
		ID: 7, AccountID: 9, ModelID: "model", TestType: "fingerprint",
		QualityThreshold: 80, CronExpression: "0 * * * *", Enabled: true, MaxResults: 50,
	}}
	h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil), nil)
	router := gin.New()
	router.POST("/plans/batch", h.BatchCreate)
	request := httptest.NewRequest(http.MethodPost, "/plans/batch", strings.NewReader(
		`{"account_ids":[9],"mode":"edit","test_type":"fingerprint","model_id":"model","cron_expression":"0 */6 * * *"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var result service.ScheduledTestBatchResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, 1, result.Updated)
	require.Zero(t, result.Created)
	require.Equal(t, int64(7), repo.saved.ID)
	require.Equal(t, "0 */6 * * *", repo.saved.CronExpression)
}

type scheduledHandlerResultRepo struct {
	service.ScheduledTestResultRepository
	filter service.ScheduledTestLogFilter
}

func (r *scheduledHandlerResultRepo) ListLogs(_ context.Context, filter service.ScheduledTestLogFilter) ([]*service.ScheduledTestLogItem, int64, error) {
	r.filter = filter
	return nil, 0, nil
}

func TestScheduledTestHandlerListLogsRejectsInvalidFilters(t *testing.T) {
	for _, query := range []string{
		"status=bogus", "test_type=bogus", "fingerprint_status=bogus",
		"account_id=0", "account_id=-1", "account_id=abc",
		"start_time=yesterday", "end_time=tomorrow",
	} {
		t.Run(query, func(t *testing.T) {
			h := NewScheduledTestHandler(service.NewScheduledTestService(nil, nil), nil)
			router := gin.New()
			router.GET("/results", h.ListLogs)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/results?"+query, nil))
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestScheduledTestHandlerListLogsCapsPageSizeAndReturnsEmptyArray(t *testing.T) {
	repo := &scheduledHandlerResultRepo{}
	h := NewScheduledTestHandler(service.NewScheduledTestService(nil, repo), nil)
	router := gin.New()
	router.GET("/results", h.ListLogs)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/results?page_size=1000&status=abnormal&account_id=9&start_time=2026-11-01T00:00:00Z", nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var result struct {
		Items    []service.ScheduledTestLogItem `json:"items"`
		Total    int64                          `json:"total"`
		Page     int                            `json:"page"`
		PageSize int                            `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, 200, result.PageSize)
	require.Equal(t, 1, result.Page)
	require.NotNil(t, result.Items)
	require.Empty(t, result.Items)
	require.Equal(t, 200, repo.filter.PageSize)
	require.Equal(t, "abnormal", repo.filter.Status)
	require.Equal(t, int64(9), repo.filter.AccountID)
	require.Equal(t, "2026-11-01T00:00:00Z", repo.filter.Start.Format("2006-01-02T15:04:05Z07:00"))
}

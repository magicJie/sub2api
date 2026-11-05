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

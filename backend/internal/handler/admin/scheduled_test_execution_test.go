//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type diagnosticExecutionAccountRepo struct {
	service.AccountRepository
	account *service.Account
}

func (r *diagnosticExecutionAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

type diagnosticExecutionPlanRepo struct{ *scheduledHandlerPlanRepo }

func (r *diagnosticExecutionPlanRepo) UpdateAfterRun(ctx context.Context, _ int64, last, next time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.plan.LastRunAt = &last
	r.plan.NextRunAt = &next
	return nil
}

type diagnosticExecutionResultRepo struct {
	service.ScheduledTestResultRepository
	saved *service.ScheduledTestResult
}

func (r *diagnosticExecutionResultRepo) Create(ctx context.Context, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	saved := *result
	saved.ID = 42
	r.saved = &saved
	return &saved, nil
}
func (r *diagnosticExecutionResultRepo) PruneOldResults(ctx context.Context, _ int64, _ int) error {
	return ctx.Err()
}

type diagnosticBlockingUpstream struct{ started chan struct{} }

func (u *diagnosticBlockingUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	close(u.started)
	<-req.Context().Done()
	return nil, req.Context().Err()
}
func (u *diagnosticBlockingUpstream) DoWithTLS(req *http.Request, p string, a int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, p, a, c)
}

// A long run may outlive its browser. Duplicate runs must be conflicts, and the
// cancelled upstream observation must still be retained with a next schedule.
func TestScheduledTestExecutionConflictAndCancelledEvidence(t *testing.T) {
	plan := &service.ScheduledTestPlan{ID: 7, AccountID: 9, ModelID: "gpt-5.5", TestType: "quality", QualityThreshold: 80, MaxResults: 50, CronExpression: "* * * * *"}
	plans := &diagnosticExecutionPlanRepo{&scheduledHandlerPlanRepo{plan: plan}}
	results := &diagnosticExecutionResultRepo{}
	scheduled := service.NewScheduledTestService(plans, results)
	upstream := &diagnosticBlockingUpstream{started: make(chan struct{})}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	account := &service.Account{ID: 9, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "test-key", "base_url": "http://model.example"}}
	testSvc := service.NewAccountTestService(&diagnosticExecutionAccountRepo{account: account}, nil, nil, nil, nil, upstream, cfg, nil)
	runner := service.NewScheduledTestRunnerService(plans, scheduled, testSvc, nil, cfg)
	handler := NewScheduledTestHandler(scheduled, runner)
	router := gin.New()
	router.POST("/plans/:id/run", handler.RunNow)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/plans/7/run", nil).WithContext(ctx))
	}()
	select {
	case <-upstream.started:
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic did not reach upstream")
	}
	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/plans/7/run", nil))
	require.Equal(t, http.StatusConflict, second.Code)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled execution did not finish")
	}
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var observed service.ScheduledTestResult
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &observed))
	require.Equal(t, "failed", observed.Status)
	require.Equal(t, int64(42), observed.ID)
	require.Equal(t, "inconclusive", observed.Diagnostics.Quality.Status)
	require.NotNil(t, results.saved)
	require.NotNil(t, plan.LastRunAt)
	require.NotNil(t, plan.NextRunAt)
	require.Equal(t, observed.FinishedAt.UnixNano(), plan.LastRunAt.UnixNano())
	require.True(t, plan.NextRunAt.After(*plan.LastRunAt))
}

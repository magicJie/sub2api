package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroUsageHandlerTestRepo struct {
	service.AccountRepository
	candidates   []service.KiroUsageRefreshCandidate
	requestCalls int
}

func (r *kiroUsageHandlerTestRepo) ListKiroUsageRefreshCandidates(context.Context) ([]service.KiroUsageRefreshCandidate, error) {
	return r.candidates, nil
}

func (r *kiroUsageHandlerTestRepo) UpdateKiroUsageSnapshot(context.Context, int64, *service.KiroUsageSnapshot, *time.Time) error {
	return nil
}

func (r *kiroUsageHandlerTestRepo) RequestKiroUsageRefreshAll(context.Context, time.Time) (int64, error) {
	r.requestCalls++
	return int64(len(r.candidates)), nil
}

func newKiroUsageHandlerTestRouter(repo service.AccountRepository) (*gin.Engine, *testSettingRepo) {
	gin.SetMode(gin.TestMode)
	settings := newTestSettingRepo()
	refresher := service.NewKiroUsageRefreshService(repo, nil, service.NewSettingService(settings, nil))
	h := NewKiroOAuthHandler(nil, service.KiroRuntimeBinding{}, refresher, nil)
	router := gin.New()
	router.GET("/kiro/usage-refresh/settings", h.GetUsageRefreshSettings)
	router.PUT("/kiro/usage-refresh/settings", h.UpdateUsageRefreshSettings)
	router.POST("/kiro/usage-refresh/refresh-all", h.RefreshAllUsage)
	return router, settings
}

func decodeKiroUsageHandlerData(t *testing.T, body []byte, out any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.NoError(t, json.Unmarshal(envelope.Data, out))
}

func TestKiroUsageRefreshSettingsHandlerRoundTrip(t *testing.T) {
	router, settingRepo := newKiroUsageHandlerTestRouter(&kiroUsageHandlerTestRepo{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kiro/usage-refresh/settings", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var got service.KiroUsageRefreshSettings
	decodeKiroUsageHandlerData(t, rec.Body.Bytes(), &got)
	require.Equal(t, service.KiroUsageRefreshSettings{Enabled: true, IntervalMinutes: 15, Concurrency: 2}, got)

	body, _ := json.Marshal(service.KiroUsageRefreshSettings{Enabled: true, IntervalMinutes: 20, Concurrency: 3})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/kiro/usage-refresh/settings", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)
	decodeKiroUsageHandlerData(t, rec.Body.Bytes(), &got)
	require.Equal(t, service.KiroUsageRefreshSettings{Enabled: true, IntervalMinutes: 20, Concurrency: 3}, got)
	require.Contains(t, settingRepo.values, service.SettingKeyKiroUsageRefreshSettings)

	body, _ = json.Marshal(service.KiroUsageRefreshSettings{Enabled: true, IntervalMinutes: 1, Concurrency: 3})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/kiro/usage-refresh/settings", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestKiroUsageRefreshAllHandlerQueuesAndReturns(t *testing.T) {
	repo := &kiroUsageHandlerTestRepo{candidates: []service.KiroUsageRefreshCandidate{{ID: 1}, {ID: 2}, {ID: 3}}}
	router, _ := newKiroUsageHandlerTestRouter(repo)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/kiro/usage-refresh/refresh-all", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var got struct {
		Queued int64 `json:"queued"`
	}
	decodeKiroUsageHandlerData(t, rec.Body.Bytes(), &got)
	require.EqualValues(t, 3, got.Queued)
	require.Equal(t, 1, repo.requestCalls)
}

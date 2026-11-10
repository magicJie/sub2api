//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestBatchRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{"cron_expression":"0 * * * *"}`,
		`{"account_ids":[1],"mode":"bogus","cron_expression":"0 * * * *"}`,
		`{"account_ids":[],"cron_expression":"0 * * * *"}`,
		`{"account_ids":[0],"cron_expression":"0 * * * *"}`,
		`{"account_ids":[1,-2],"cron_expression":"0 * * * *"}`,
		`{"account_ids":[1],"cron_expression":"invalid"}`,
		`{"account_ids":[1],"cron_expression":"0 * * * *","test_type":"quality"}`,
		`{"account_ids":[1],"cron_expression":"0 * * * *","quality_threshold":0}`,
		`{"account_ids":[1],"cron_expression":"0 * * * *","max_results":1001}`,
		`{"account_ids":[` + strings.Repeat("1,", 1000) + `1],"cron_expression":"0 * * * *"}`,
	} {
		h := NewScheduledTestHandler(service.NewScheduledTestService(nil, nil), nil)
		router := gin.New()
		router.POST("/plans/batch", h.BatchCreate)
		request := httptest.NewRequest(http.MethodPost, "/plans/batch", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
}

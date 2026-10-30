package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func serveReady(t *testing.T, h gin.HandlerFunc) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ready", h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body
}

func TestReadyHandler(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	maxWait := 120 * time.Second
	pending := []service.UpstreamPendingSlot{{GroupID: 1, SlotID: 10}}
	at := func(d time.Duration) func() time.Time { return func() time.Time { return start.Add(d) } }

	t.Run("ready", func(t *testing.T) {
		code, body := serveReady(t, readyHandler(start, maxWait, at(time.Second), func() (bool, bool, []service.UpstreamPendingSlot) {
			return true, true, nil
		}))
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, "ready", body["status"])
	})

	t.Run("pending returns 503 with slots", func(t *testing.T) {
		code, body := serveReady(t, readyHandler(start, maxWait, at(30*time.Second), func() (bool, bool, []service.UpstreamPendingSlot) {
			return false, true, pending
		}))
		require.Equal(t, http.StatusServiceUnavailable, code)
		require.Equal(t, "upstream_probe_pending", body["reason"])
		slots := body["pending_slots"].([]any)
		require.Len(t, slots, 1)
		require.Equal(t, float64(10), slots[0].(map[string]any)["slot_id"])
	})

	t.Run("config not loaded returns 503", func(t *testing.T) {
		code, body := serveReady(t, readyHandler(start, maxWait, at(time.Second), func() (bool, bool, []service.UpstreamPendingSlot) {
			return false, false, nil
		}))
		require.Equal(t, http.StatusServiceUnavailable, code)
		require.Equal(t, "upstream_config_not_loaded", body["reason"])
	})

	t.Run("max wait exceeded returns 200", func(t *testing.T) {
		code, body := serveReady(t, readyHandler(start, maxWait, at(maxWait), func() (bool, bool, []service.UpstreamPendingSlot) {
			return false, true, pending
		}))
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, "max_wait_exceeded", body["reason"])
	})

	t.Run("zero max wait never blocks", func(t *testing.T) {
		code, _ := serveReady(t, readyHandler(start, 0, at(0), func() (bool, bool, []service.UpstreamPendingSlot) {
			return false, true, pending
		}))
		require.Equal(t, http.StatusOK, code)
	})
}

package routes

import (
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// processStartedAt 是 /ready 等待上限的起点（包初始化即进程启动）。
var processStartedAt = time.Now()

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine, readyMaxWait time.Duration) {
	// 健康检查（liveness）：进程能响应即 200，不看依赖状态。
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 就绪检查（readiness）：所有启用前置组的每个槽位都选中了探测健康的当前端点才 200，供 K8s readinessProbe 使用。
	r.GET("/ready", readyHandler(processStartedAt, readyMaxWait, time.Now, service.ProxyUpstreamStartupReadiness))

	// Claude Code 遥测日志（忽略，直接返回200）
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})
}

// readyHandler 返回就绪检查处理器：每个启用槽位的当前端点已探测健康才返回 200，
// 否则 503 并列出不可服务槽位。探测过但全挂的槽位不会让实例提前就绪，
// 防止流量被切进「upstream group has no available slot」的 502。
// 自 startedAt 起超过 maxWait 后无论探测状态都返回 200（AIO 全挂时不让实例永远不就绪；请求本身由槽位 fail-closed）。
func readyHandler(startedAt time.Time, maxWait time.Duration, now func() time.Time,
	check func() (ready, loaded bool, pending []service.UpstreamPendingSlot)) gin.HandlerFunc {
	return func(c *gin.Context) {
		ready, loaded, pending := check()
		if ready {
			c.JSON(http.StatusOK, gin.H{"status": "ready"})
			return
		}
		if now().Sub(startedAt) >= maxWait {
			c.JSON(http.StatusOK, gin.H{"status": "ready", "reason": "max_wait_exceeded", "pending_slots": pending})
			return
		}
		body := gin.H{"status": "not_ready", "pending_slots": pending}
		if !loaded {
			body["reason"] = "upstream_config_not_loaded"
		} else {
			body["reason"] = "upstream_probe_pending"
		}
		c.JSON(http.StatusServiceUnavailable, body)
	}
}

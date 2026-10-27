package admin

import (
	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// GetUsageRefreshSettings 返回 Kiro 额度后台刷新设置。
// GET /api/v1/admin/kiro/usage-refresh/settings
func (h *KiroOAuthHandler) GetUsageRefreshSettings(c *gin.Context) {
	if h.kiroUsageRefresh == nil {
		response.ErrorFrom(c, service.ErrKiroUsageRefreshUnavailable)
		return
	}
	settings, err := h.kiroUsageRefresh.GetSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateUsageRefreshSettings 保存 Kiro 额度后台刷新设置。
// PUT /api/v1/admin/kiro/usage-refresh/settings
func (h *KiroOAuthHandler) UpdateUsageRefreshSettings(c *gin.Context) {
	if h.kiroUsageRefresh == nil {
		response.ErrorFrom(c, service.ErrKiroUsageRefreshUnavailable)
		return
	}
	var req service.KiroUsageRefreshSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings, err := h.kiroUsageRefresh.UpdateSettings(c.Request.Context(), &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// RefreshAllUsage 把所有 Kiro 账号排进后台刷新队列并立即返回，不在请求里打上游。
// POST /api/v1/admin/kiro/usage-refresh/refresh-all
func (h *KiroOAuthHandler) RefreshAllUsage(c *gin.Context) {
	if h.kiroUsageRefresh == nil {
		response.ErrorFrom(c, service.ErrKiroUsageRefreshUnavailable)
		return
	}
	queued, err := h.kiroUsageRefresh.RequestRefreshAll(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"queued": queued})
}

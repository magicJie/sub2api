package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// registerKiroOAuthRoutes 注册 Kiro（fork）账号 OAuth 管理接口。
func registerKiroOAuthRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	kiro := admin.Group("/kiro")
	{
		kiro.POST("/oauth/auth-url", h.Admin.KiroOAuth.GenerateAuthURL)
		kiro.POST("/oauth/idc-auth-url", h.Admin.KiroOAuth.GenerateIDCAuthURL)
		kiro.POST("/oauth/exchange-code", h.Admin.KiroOAuth.ExchangeCode)
		kiro.POST("/oauth/refresh-token", h.Admin.KiroOAuth.RefreshToken)
		kiro.POST("/oauth/import-token", h.Admin.KiroOAuth.ImportToken)
		kiro.GET("/routes", h.Admin.KiroOAuth.RecentRoutes)
		kiro.GET("/usage-refresh/settings", h.Admin.KiroOAuth.GetUsageRefreshSettings)
		kiro.PUT("/usage-refresh/settings", h.Admin.KiroOAuth.UpdateUsageRefreshSettings)
		kiro.POST("/usage-refresh/refresh-all", h.Admin.KiroOAuth.RefreshAllUsage)
		kiro.GET("/retirable-accounts", h.Admin.KiroOAuth.ListRetirableAccounts)
		kiro.POST("/retire", h.Admin.KiroOAuth.RetireAccounts)
	}
}

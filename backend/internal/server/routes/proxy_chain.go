package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// registerProxyChainRoutes 注册链式代理（fork）管理接口。
func registerProxyChainRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	proxies := admin.Group("/proxies")
	{
		proxies.POST("/parse-link", h.Admin.ProxyChain.ParseLink)
		proxies.GET("/chain-settings", h.Admin.ProxyChain.GetSettings)
		proxies.PUT("/chain-settings", h.Admin.ProxyChain.UpdateSettings)
	}
}

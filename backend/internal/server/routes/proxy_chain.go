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
	groups := admin.Group("/proxy-hop-groups")
	groups.GET("", h.Admin.ProxyChain.ListHopGroups)
	groups.POST("", h.Admin.ProxyChain.CreateHopGroup)
	groups.GET("/:id", h.Admin.ProxyChain.GetHopGroup)
	groups.PUT("/:id", h.Admin.ProxyChain.UpdateHopGroup)
	groups.DELETE("/:id", h.Admin.ProxyChain.DeleteHopGroup)
}

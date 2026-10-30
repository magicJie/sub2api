package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// registerProxyChainRoutes 注册链式代理（fork）管理接口。
func registerProxyChainRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	admin.POST("/proxies/parse-link", h.Admin.ProxyChain.ParseLink)

	aios := admin.Group("/proxy-upstream-aios")
	aios.GET("", h.Admin.ProxyChain.ListUpstreamAIOs)
	aios.POST("", h.Admin.ProxyChain.CreateUpstreamAIO)
	aios.GET("/:id", h.Admin.ProxyChain.GetUpstreamAIO)
	aios.PUT("/:id", h.Admin.ProxyChain.UpdateUpstreamAIO)
	aios.DELETE("/:id", h.Admin.ProxyChain.DeleteUpstreamAIO)

	endpoints := admin.Group("/proxy-upstream-endpoints")
	endpoints.GET("", h.Admin.ProxyChain.ListUpstreamEndpoints)
	endpoints.POST("", h.Admin.ProxyChain.CreateUpstreamEndpoint)
	endpoints.GET("/:id", h.Admin.ProxyChain.GetUpstreamEndpoint)
	endpoints.PUT("/:id", h.Admin.ProxyChain.UpdateUpstreamEndpoint)
	endpoints.DELETE("/:id", h.Admin.ProxyChain.DeleteUpstreamEndpoint)

	groups := admin.Group("/proxy-upstream-groups")
	groups.GET("", h.Admin.ProxyChain.ListUpstreamGroups)
	groups.POST("", h.Admin.ProxyChain.CreateUpstreamGroup)
	groups.GET("/:id", h.Admin.ProxyChain.GetUpstreamGroup)
	groups.PUT("/:id", h.Admin.ProxyChain.UpdateUpstreamGroup)
	groups.DELETE("/:id", h.Admin.ProxyChain.DeleteUpstreamGroup)
}

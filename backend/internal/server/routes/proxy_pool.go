package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// registerProxyPoolRoutes 注册代理池（fork）管理接口。
func registerProxyPoolRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	pools := admin.Group("/proxy-pools")
	{
		pools.GET("", h.Admin.ProxyPool.List)
		pools.POST("", h.Admin.ProxyPool.Create)
		pools.PUT("/:id", h.Admin.ProxyPool.Update)
		pools.DELETE("/:id", h.Admin.ProxyPool.Delete)
		pools.GET("/:id/proxies", h.Admin.ProxyPool.Members)
		pools.POST("/:id/proxies", h.Admin.ProxyPool.AssignProxies)
		pools.GET("/:id/preview", h.Admin.ProxyPool.PreviewProxy)
	}
	admin.GET("/proxies/:id/bindings", h.Admin.ProxyPool.ProxyBindings)
	accounts := admin.Group("/accounts")
	{
		accounts.GET("/:id/proxy-binding", h.Admin.ProxyPool.AccountBinding)
		accounts.POST("/:id/proxy-pool", h.Admin.ProxyPool.AssignAccount)
		accounts.POST("/:id/proxy-unbind", h.Admin.ProxyPool.UnbindAccount)
	}
}

package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// RecentRoutes 返回最近的 Kiro（fork）转发尝试，新的在前；limit 默认 50，最多 200。
func (h *KiroOAuthHandler) RecentRoutes(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit <= 0 {
		limit = 50
	}
	response.Success(c, gin.H{"items": service.KiroRecentRoutes(limit)})
}

package admin

// 链式代理（fork）：首跳组管理，订阅地址只在写入请求中接收，绝不回传。

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/hopgroup"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type proxyHopGroupDTO struct {
	ID                         int64                 `json:"id"`
	Name                       string                `json:"name"`
	Strategy                   string                `json:"strategy"`
	TestURL                    string                `json:"test_url"`
	IntervalSeconds            int                   `json:"interval_seconds"`
	ToleranceMS                int                   `json:"tolerance_ms"`
	SubscriptionURLs           []string              `json:"subscription_urls"`
	SubscriptionCount          int                   `json:"subscription_count"`
	MemberProxyIDs             []int64               `json:"member_proxy_ids"`
	SubscriptionUpdateInterval int                   `json:"subscription_update_interval"`
	Status                     string                `json:"status"`
	CreatedAt                  time.Time             `json:"created_at"`
	UpdatedAt                  time.Time             `json:"updated_at"`
	Runtime                    *hopgroup.GroupStatus `json:"runtime,omitempty"`
	RuntimeError               string                `json:"runtime_error,omitempty"`
}

func redactedHopGroup(group *service.ProxyHopGroup) proxyHopGroupDTO {
	masked := make([]string, len(group.SubscriptionURLs))
	for i := range masked {
		masked[i] = "[REDACTED]"
	}
	members := group.MemberProxyIDs
	if members == nil {
		members = []int64{}
	}
	return proxyHopGroupDTO{ID: group.ID, Name: group.Name, Strategy: group.Strategy, TestURL: group.TestURL, IntervalSeconds: group.IntervalSeconds, ToleranceMS: group.ToleranceMS, SubscriptionURLs: masked, SubscriptionCount: len(masked), MemberProxyIDs: members, SubscriptionUpdateInterval: group.SubscriptionUpdateInterval, Status: group.Status, CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt}
}

type proxyHopGroupRequest struct {
	Name                       *string   `json:"name"`
	Strategy                   *string   `json:"strategy"`
	TestURL                    *string   `json:"test_url"`
	IntervalSeconds            *int      `json:"interval_seconds"`
	ToleranceMS                *int      `json:"tolerance_ms"`
	SubscriptionURLs           *[]string `json:"subscription_urls"`
	MemberProxyIDs             *[]int64  `json:"member_proxy_ids"`
	SubscriptionUpdateInterval *int      `json:"subscription_update_interval"`
	Status                     *string   `json:"status"`
}

func (req *proxyHopGroupRequest) apply(group *service.ProxyHopGroup) {
	if req.Name != nil {
		group.Name = *req.Name
	}
	if req.Strategy != nil {
		group.Strategy = *req.Strategy
	}
	if req.TestURL != nil {
		group.TestURL = *req.TestURL
	}
	if req.IntervalSeconds != nil {
		group.IntervalSeconds = *req.IntervalSeconds
	}
	if req.ToleranceMS != nil {
		group.ToleranceMS = *req.ToleranceMS
	}
	if req.SubscriptionURLs != nil {
		group.SubscriptionURLs = *req.SubscriptionURLs
	}
	if req.MemberProxyIDs != nil {
		group.MemberProxyIDs = *req.MemberProxyIDs
	}
	if req.SubscriptionUpdateInterval != nil {
		group.SubscriptionUpdateInterval = *req.SubscriptionUpdateInterval
	}
	if req.Status != nil {
		group.Status = *req.Status
	}
}

func hopGroupID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid first-hop group ID")
		return 0, false
	}
	return id, true
}

func (h *ProxyChainHandler) ListHopGroups(c *gin.Context) {
	groups, err := h.chainService.ListHopGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]proxyHopGroupDTO, 0, len(groups))
	for i := range groups {
		out = append(out, redactedHopGroup(&groups[i]))
	}
	response.Success(c, out)
}

func (h *ProxyChainHandler) GetHopGroup(c *gin.Context) {
	id, ok := hopGroupID(c)
	if !ok {
		return
	}
	group, err := h.chainService.GetHopGroup(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := redactedHopGroup(group)
	status, err := h.chainService.HopGroupStatus(id)
	if err != nil {
		out.RuntimeError = "First-hop group runtime is unavailable"
	} else {
		out.Runtime = &status
	}
	response.Success(c, out)
}

func (h *ProxyChainHandler) CreateHopGroup(c *gin.Context) {
	var req proxyHopGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid first-hop group request")
		return
	}
	group := &service.ProxyHopGroup{ToleranceMS: 50}
	req.apply(group)
	if err := h.chainService.CreateHopGroup(c.Request.Context(), group); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, redactedHopGroup(group))
}

func (h *ProxyChainHandler) UpdateHopGroup(c *gin.Context) {
	id, ok := hopGroupID(c)
	if !ok {
		return
	}
	var req proxyHopGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid first-hop group request")
		return
	}
	group, err := h.chainService.GetHopGroup(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	req.apply(group)
	if err := h.chainService.UpdateHopGroup(c.Request.Context(), group); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, redactedHopGroup(group))
}

func (h *ProxyChainHandler) DeleteHopGroup(c *gin.Context) {
	id, ok := hopGroupID(c)
	if !ok {
		return
	}
	if err := h.chainService.DeleteHopGroup(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

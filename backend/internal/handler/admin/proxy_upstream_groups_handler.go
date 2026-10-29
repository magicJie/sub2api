package admin

// 链式代理（fork）：前置端点与前置组管理；端点密码只在写入请求中接收，绝不回传。

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type proxyUpstreamEndpointDTO struct {
	ID          int64                          `json:"id"`
	Name        string                         `json:"name"`
	Protocol    string                         `json:"protocol"`
	Host        string                         `json:"host"`
	Port        int                            `json:"port"`
	Username    string                         `json:"username"`
	HasPassword bool                           `json:"has_password"`
	Status      string                         `json:"status"`
	CreatedAt   time.Time                      `json:"created_at"`
	UpdatedAt   time.Time                      `json:"updated_at"`
	Health      service.UpstreamEndpointHealth `json:"health"`
	GroupNames  []string                       `json:"group_names"`
}

type proxyUpstreamEndpointRequest struct {
	Name     *string `json:"name"`
	Protocol *string `json:"protocol"`
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"` // 省略或空串表示保持不变
	Status   *string `json:"status"`
}

func (req *proxyUpstreamEndpointRequest) apply(e *service.ProxyUpstreamEndpoint) {
	if req.Name != nil {
		e.Name = *req.Name
	}
	if req.Protocol != nil {
		e.Protocol = *req.Protocol
	}
	if req.Host != nil {
		e.Host = *req.Host
	}
	if req.Port != nil {
		e.Port = *req.Port
	}
	if req.Username != nil {
		e.Username = *req.Username
	}
	if req.Password != nil && *req.Password != "" {
		e.Password = *req.Password
	}
	if req.Status != nil {
		e.Status = *req.Status
	}
}

type proxyUpstreamSlotDTO struct {
	ID          int64                        `json:"id"`
	Name        string                       `json:"name"`
	Position    int                          `json:"position"`
	EndpointIDs []int64                      `json:"endpoint_ids"`
	Runtime     *service.UpstreamSlotRuntime `json:"runtime,omitempty"`
}

type proxyUpstreamGroupDTO struct {
	ID                   int64                  `json:"id"`
	Name                 string                 `json:"name"`
	Status               string                 `json:"status"`
	ProbeURL             string                 `json:"probe_url"`
	ProbeIntervalSeconds int                    `json:"probe_interval_seconds"`
	FailThreshold        int                    `json:"fail_threshold"`
	RecoverThreshold     int                    `json:"recover_threshold"`
	FailbackSeconds      int                    `json:"failback_seconds"`
	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
	ProxyCount           int                    `json:"proxy_count"`
	Slots                []proxyUpstreamSlotDTO `json:"slots"`
}

type proxyUpstreamSlotRequest struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	EndpointIDs []int64 `json:"endpoint_ids"`
}

type proxyUpstreamGroupRequest struct {
	Name                 *string                     `json:"name"`
	Status               *string                     `json:"status"`
	ProbeURL             *string                     `json:"probe_url"`
	ProbeIntervalSeconds *int                        `json:"probe_interval_seconds"`
	FailThreshold        *int                        `json:"fail_threshold"`
	RecoverThreshold     *int                        `json:"recover_threshold"`
	FailbackSeconds      *int                        `json:"failback_seconds"`
	Slots                *[]proxyUpstreamSlotRequest `json:"slots"`
}

func (req *proxyUpstreamGroupRequest) apply(g *service.ProxyUpstreamGroup) {
	if req.Name != nil {
		g.Name = *req.Name
	}
	if req.Status != nil {
		g.Status = *req.Status
	}
	if req.ProbeURL != nil {
		g.ProbeURL = *req.ProbeURL
	}
	if req.ProbeIntervalSeconds != nil {
		g.ProbeIntervalSeconds = *req.ProbeIntervalSeconds
	}
	if req.FailThreshold != nil {
		g.FailThreshold = *req.FailThreshold
	}
	if req.RecoverThreshold != nil {
		g.RecoverThreshold = *req.RecoverThreshold
	}
	if req.FailbackSeconds != nil {
		g.FailbackSeconds = *req.FailbackSeconds
	}
	if req.Slots != nil {
		g.Slots = make([]service.ProxyUpstreamSlot, 0, len(*req.Slots))
		for _, s := range *req.Slots {
			g.Slots = append(g.Slots, service.ProxyUpstreamSlot{ID: s.ID, Name: s.Name, EndpointIDs: s.EndpointIDs})
		}
	}
}

func upstreamPathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid ID")
		return 0, false
	}
	return id, true
}

func (h *ProxyChainHandler) endpointDTO(e *service.ProxyUpstreamEndpoint, groups []service.ProxyUpstreamGroup) proxyUpstreamEndpointDTO {
	names := []string{}
	for _, g := range groups {
		used := false
		for _, s := range g.Slots {
			for _, id := range s.EndpointIDs {
				used = used || id == e.ID
			}
		}
		if used {
			names = append(names, g.Name)
		}
	}
	return proxyUpstreamEndpointDTO{ID: e.ID, Name: e.Name, Protocol: e.Protocol, Host: e.Host, Port: e.Port, Username: e.Username,
		HasPassword: e.Password != "", Status: e.Status, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		Health: h.chainService.UpstreamEndpointHealth(e.ID), GroupNames: names}
}

func (h *ProxyChainHandler) groupDTO(g *service.ProxyUpstreamGroup, counts map[int64]int) proxyUpstreamGroupDTO {
	slots := make([]proxyUpstreamSlotDTO, 0, len(g.Slots))
	for _, s := range g.Slots {
		ids := s.EndpointIDs
		if ids == nil {
			ids = []int64{}
		}
		slots = append(slots, proxyUpstreamSlotDTO{ID: s.ID, Name: s.Name, Position: s.Position, EndpointIDs: ids, Runtime: h.chainService.UpstreamSlotRuntime(s.ID)})
	}
	return proxyUpstreamGroupDTO{ID: g.ID, Name: g.Name, Status: g.Status, ProbeURL: g.ProbeURL, ProbeIntervalSeconds: g.ProbeIntervalSeconds,
		FailThreshold: g.FailThreshold, RecoverThreshold: g.RecoverThreshold, FailbackSeconds: g.FailbackSeconds,
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, ProxyCount: counts[g.ID], Slots: slots}
}

// ListUpstreamEndpoints GET /api/v1/admin/proxy-upstream-endpoints
func (h *ProxyChainHandler) ListUpstreamEndpoints(c *gin.Context) {
	ctx := c.Request.Context()
	endpoints, err := h.chainService.ListUpstreamEndpoints(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	groups, err := h.chainService.ListUpstreamGroups(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]proxyUpstreamEndpointDTO, 0, len(endpoints))
	for i := range endpoints {
		out = append(out, h.endpointDTO(&endpoints[i], groups))
	}
	response.Success(c, out)
}

func (h *ProxyChainHandler) GetUpstreamEndpoint(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	e, err := h.chainService.GetUpstreamEndpoint(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respondEndpoint(c, e)
}

func (h *ProxyChainHandler) respondEndpoint(c *gin.Context, e *service.ProxyUpstreamEndpoint) {
	groups, err := h.chainService.ListUpstreamGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.endpointDTO(e, groups))
}

func (h *ProxyChainHandler) CreateUpstreamEndpoint(c *gin.Context) {
	var req proxyUpstreamEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid upstream endpoint request")
		return
	}
	e := &service.ProxyUpstreamEndpoint{}
	req.apply(e)
	if err := h.chainService.CreateUpstreamEndpoint(c.Request.Context(), e); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respondEndpoint(c, e)
}

func (h *ProxyChainHandler) UpdateUpstreamEndpoint(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	var req proxyUpstreamEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid upstream endpoint request")
		return
	}
	e, err := h.chainService.GetUpstreamEndpoint(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	req.apply(e)
	if err := h.chainService.UpdateUpstreamEndpoint(c.Request.Context(), e); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respondEndpoint(c, e)
}

func (h *ProxyChainHandler) DeleteUpstreamEndpoint(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	if err := h.chainService.DeleteUpstreamEndpoint(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ListUpstreamGroups GET /api/v1/admin/proxy-upstream-groups
func (h *ProxyChainHandler) ListUpstreamGroups(c *gin.Context) {
	groups, err := h.chainService.ListUpstreamGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	counts := h.chainService.UpstreamGroupProxyCounts()
	out := make([]proxyUpstreamGroupDTO, 0, len(groups))
	for i := range groups {
		out = append(out, h.groupDTO(&groups[i], counts))
	}
	response.Success(c, out)
}

func (h *ProxyChainHandler) GetUpstreamGroup(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	g, err := h.chainService.GetUpstreamGroup(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.groupDTO(g, h.chainService.UpstreamGroupProxyCounts()))
}

func (h *ProxyChainHandler) CreateUpstreamGroup(c *gin.Context) {
	var req proxyUpstreamGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid upstream group request")
		return
	}
	g := &service.ProxyUpstreamGroup{
		ProbeURL: service.ProxyUpstreamDefaultProbeURL, ProbeIntervalSeconds: service.ProxyUpstreamDefaultProbeInterval,
		FailThreshold: service.ProxyUpstreamDefaultFailThreshold, RecoverThreshold: service.ProxyUpstreamDefaultRecover,
		FailbackSeconds: service.ProxyUpstreamDefaultFailback,
	}
	req.apply(g)
	if err := h.chainService.CreateUpstreamGroup(c.Request.Context(), g); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.groupDTO(g, h.chainService.UpstreamGroupProxyCounts()))
}

func (h *ProxyChainHandler) UpdateUpstreamGroup(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	var req proxyUpstreamGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid upstream group request")
		return
	}
	g, err := h.chainService.GetUpstreamGroup(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	req.apply(g)
	if err := h.chainService.UpdateUpstreamGroup(c.Request.Context(), g); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.groupDTO(g, h.chainService.UpstreamGroupProxyCounts()))
}

func (h *ProxyChainHandler) DeleteUpstreamGroup(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	if err := h.chainService.DeleteUpstreamGroup(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

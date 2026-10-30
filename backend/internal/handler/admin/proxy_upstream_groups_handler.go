package admin

// 链式代理（fork）：AIO、AIO 端口（前置端点）与前置组管理；AIO 密码只在写入请求中接收，绝不回传。

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type proxyUpstreamAIODTO struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Protocol    string    `json:"protocol"`
	Host        string    `json:"host"`
	Username    string    `json:"username"`
	HasPassword bool      `json:"has_password"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PortCount   int       `json:"port_count"`
}

type proxyUpstreamAIORequest struct {
	Name     *string `json:"name"`
	Protocol *string `json:"protocol"`
	Host     *string `json:"host"`
	Username *string `json:"username"`
	Password *string `json:"password"` // 省略或空串表示保持不变
	Status   *string `json:"status"`
}

func (req *proxyUpstreamAIORequest) apply(a *service.ProxyUpstreamAIO) {
	if req.Name != nil {
		a.Name = *req.Name
	}
	if req.Protocol != nil {
		a.Protocol = *req.Protocol
	}
	if req.Host != nil {
		a.Host = *req.Host
	}
	if req.Username != nil {
		a.Username = *req.Username
	}
	if req.Password != nil && *req.Password != "" {
		a.Password = *req.Password
	}
	if req.Status != nil {
		a.Status = *req.Status
	}
}

// proxyUpstreamEndpointDTO 是 AIO 的一个监听端口；protocol / host / username 取自所属 AIO，只读。
type proxyUpstreamEndpointDTO struct {
	ID         int64                          `json:"id"`
	AIOID      int64                          `json:"aio_id"`
	AIOName    string                         `json:"aio_name"`
	AIOStatus  string                         `json:"aio_status"`
	Name       string                         `json:"name"`
	Protocol   string                         `json:"protocol"`
	Host       string                         `json:"host"`
	Port       int                            `json:"port"`
	Username   string                         `json:"username"`
	Status     string                         `json:"status"`
	CreatedAt  time.Time                      `json:"created_at"`
	UpdatedAt  time.Time                      `json:"updated_at"`
	Health     service.UpstreamEndpointHealth `json:"health"`
	GroupNames []string                       `json:"group_names"`
}

type proxyUpstreamEndpointRequest struct {
	AIOID  *int64  `json:"aio_id"`
	Name   *string `json:"name"`
	Port   *int    `json:"port"`
	Status *string `json:"status"`
}

func (req *proxyUpstreamEndpointRequest) apply(e *service.ProxyUpstreamEndpoint) {
	if req.AIOID != nil {
		e.AIOID = *req.AIOID
	}
	if req.Name != nil {
		e.Name = *req.Name
	}
	if req.Port != nil {
		e.Port = *req.Port
	}
	if req.Status != nil {
		e.Status = *req.Status
	}
}

type proxyUpstreamSlotDTO struct {
	ID          int64                        `json:"id"`
	Name        string                       `json:"name"`
	Position    int                          `json:"position"`
	Weight      int                          `json:"weight"`
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
	Weight      int     `json:"weight"` // 省略或 0 取默认 1
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
			g.Slots = append(g.Slots, service.ProxyUpstreamSlot{ID: s.ID, Name: s.Name, Weight: s.Weight, EndpointIDs: s.EndpointIDs})
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
	return proxyUpstreamEndpointDTO{ID: e.ID, AIOID: e.AIOID, AIOName: e.AIOName, AIOStatus: e.AIOStatus, Name: e.Name,
		Protocol: e.Protocol, Host: e.Host, Port: e.Port, Username: e.Username, Status: e.Status,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, Health: h.chainService.UpstreamEndpointHealth(e.ID), GroupNames: names}
}

func aioDTO(a *service.ProxyUpstreamAIO, endpoints []service.ProxyUpstreamEndpoint) proxyUpstreamAIODTO {
	count := 0
	for i := range endpoints {
		if endpoints[i].AIOID == a.ID {
			count++
		}
	}
	return proxyUpstreamAIODTO{ID: a.ID, Name: a.Name, Protocol: a.Protocol, Host: a.Host, Username: a.Username,
		HasPassword: a.Password != "", Status: a.Status, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, PortCount: count}
}

// ListUpstreamAIOs GET /api/v1/admin/proxy-upstream-aios
func (h *ProxyChainHandler) ListUpstreamAIOs(c *gin.Context) {
	ctx := c.Request.Context()
	aios, err := h.chainService.ListUpstreamAIOs(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	endpoints, err := h.chainService.ListUpstreamEndpoints(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]proxyUpstreamAIODTO, 0, len(aios))
	for i := range aios {
		out = append(out, aioDTO(&aios[i], endpoints))
	}
	response.Success(c, out)
}

func (h *ProxyChainHandler) GetUpstreamAIO(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	a, err := h.chainService.GetUpstreamAIO(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respondAIO(c, a)
}

func (h *ProxyChainHandler) respondAIO(c *gin.Context, a *service.ProxyUpstreamAIO) {
	endpoints, err := h.chainService.ListUpstreamEndpoints(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, aioDTO(a, endpoints))
}

func (h *ProxyChainHandler) CreateUpstreamAIO(c *gin.Context) {
	var req proxyUpstreamAIORequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid AIO request")
		return
	}
	a := &service.ProxyUpstreamAIO{}
	req.apply(a)
	if err := h.chainService.CreateUpstreamAIO(c.Request.Context(), a); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respondAIO(c, a)
}

func (h *ProxyChainHandler) UpdateUpstreamAIO(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	var req proxyUpstreamAIORequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid AIO request")
		return
	}
	a, err := h.chainService.GetUpstreamAIO(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	req.apply(a)
	if err := h.chainService.UpdateUpstreamAIO(c.Request.Context(), a); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respondAIO(c, a)
}

func (h *ProxyChainHandler) DeleteUpstreamAIO(c *gin.Context) {
	id, ok := upstreamPathID(c)
	if !ok {
		return
	}
	if err := h.chainService.DeleteUpstreamAIO(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *ProxyChainHandler) groupDTO(g *service.ProxyUpstreamGroup, counts map[int64]int) proxyUpstreamGroupDTO {
	slots := make([]proxyUpstreamSlotDTO, 0, len(g.Slots))
	for _, s := range g.Slots {
		ids := s.EndpointIDs
		if ids == nil {
			ids = []int64{}
		}
		slots = append(slots, proxyUpstreamSlotDTO{ID: s.ID, Name: s.Name, Position: s.Position, Weight: s.Weight, EndpointIDs: ids, Runtime: h.chainService.UpstreamSlotRuntime(s.ID)})
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

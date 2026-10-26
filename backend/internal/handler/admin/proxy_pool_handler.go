package admin

import (
	"sort"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 代理池（fork）：代理池分组管理、池内代理与退役状态、账号-代理绑定记录、账号分配/解绑。

type ProxyPoolHandler struct {
	poolService *service.ProxyPoolService
}

func NewProxyPoolHandler(poolService *service.ProxyPoolService) *ProxyPoolHandler {
	return &ProxyPoolHandler{poolService: poolService}
}

type ProxyPoolDTO struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	PlatformLimits map[string]int `json:"platform_limits"`
	IsDefault      bool           `json:"is_default"`
	ProxyCount     int64          `json:"proxy_count"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func proxyPoolDTO(p *service.ProxyPool) ProxyPoolDTO {
	limits := p.PlatformLimits
	if limits == nil {
		limits = map[string]int{}
	}
	return ProxyPoolDTO{
		ID: p.ID, Name: p.Name, Description: p.Description, PlatformLimits: limits,
		IsDefault: p.IsDefault, ProxyCount: p.ProxyCount, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

type ProxyPoolMemberDTO struct {
	Proxy            dto.AdminProxy   `json:"proxy"`
	HistoryCounts    map[string]int64 `json:"history_counts"`
	ActiveCounts     map[string]int64 `json:"active_counts"`
	RetiredPlatforms []string         `json:"retired_platforms"`
}

type ProxyAccountBindingDTO struct {
	ID           int64      `json:"id"`
	ProxyID      int64      `json:"proxy_id"`
	ProxyName    string     `json:"proxy_name"`
	AccountID    int64      `json:"account_id"`
	AccountName  string     `json:"account_name"`
	Platform     string     `json:"platform"`
	PoolID       *int64     `json:"pool_id"`
	PoolName     string     `json:"pool_name"`
	Source       string     `json:"source"`
	Locked       bool       `json:"locked"`
	BoundAt      time.Time  `json:"bound_at"`
	UnlockedAt   *time.Time `json:"unlocked_at"`
	UnboundAt    *time.Time `json:"unbound_at"`
	UnbindReason string     `json:"unbind_reason"`
}

func proxyAccountBindingDTO(b *service.ProxyAccountBinding) ProxyAccountBindingDTO {
	return ProxyAccountBindingDTO{
		ID: b.ID, ProxyID: b.ProxyID, ProxyName: b.ProxyName, AccountID: b.AccountID, AccountName: b.AccountName,
		Platform: b.Platform, PoolID: b.PoolID, PoolName: b.PoolName, Source: b.Source, Locked: b.Locked,
		BoundAt: b.BoundAt, UnlockedAt: b.UnlockedAt, UnboundAt: b.UnboundAt, UnbindReason: b.UnbindReason,
	}
}

type proxyPoolUpsertRequest struct {
	Name           *string        `json:"name"`
	Description    *string        `json:"description"`
	PlatformLimits map[string]int `json:"platform_limits"`
}

func parseProxyPoolIDParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return 0, false
	}
	return id, true
}

// List GET /api/v1/admin/proxy-pools
func (h *ProxyPoolHandler) List(c *gin.Context) {
	pools, err := h.poolService.ListPools(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]ProxyPoolDTO, 0, len(pools))
	for i := range pools {
		out = append(out, proxyPoolDTO(&pools[i]))
	}
	response.Success(c, out)
}

// Create POST /api/v1/admin/proxy-pools
func (h *ProxyPoolHandler) Create(c *gin.Context) {
	var req proxyPoolUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool := &service.ProxyPool{PlatformLimits: req.PlatformLimits}
	if req.Name != nil {
		pool.Name = *req.Name
	}
	if req.Description != nil {
		pool.Description = *req.Description
	}
	created, err := h.poolService.CreatePool(c.Request.Context(), pool)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, proxyPoolDTO(created))
}

// Update PUT /api/v1/admin/proxy-pools/:id
func (h *ProxyPoolHandler) Update(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	var req proxyPoolUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	updated, err := h.poolService.UpdatePool(c.Request.Context(), id, req.Name, req.Description, req.PlatformLimits)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, proxyPoolDTO(updated))
}

// Delete DELETE /api/v1/admin/proxy-pools/:id
func (h *ProxyPoolHandler) Delete(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.poolService.DeletePool(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Proxy pool deleted"})
}

// Members GET /api/v1/admin/proxy-pools/:id/proxies
func (h *ProxyPoolHandler) Members(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	pool, members, err := h.poolService.ListMembers(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]ProxyPoolMemberDTO, 0, len(members))
	for i := range members {
		m := &members[i]
		out = append(out, ProxyPoolMemberDTO{
			Proxy:            *dto.ProxyFromServiceAdmin(&m.Proxy),
			HistoryCounts:    m.HistoryCounts,
			ActiveCounts:     m.ActiveCounts,
			RetiredPlatforms: service.ProxyPoolRetiredPlatforms(pool, m),
		})
	}
	response.Success(c, gin.H{"pool": proxyPoolDTO(pool), "members": out})
}

// AssignProxies POST /api/v1/admin/proxy-pools/:id/proxies  {"proxy_ids":[...]}
func (h *ProxyPoolHandler) AssignProxies(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		ProxyIDs []int64 `json:"proxy_ids" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.poolService.AssignProxies(c.Request.Context(), id, req.ProxyIDs); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"assigned": len(req.ProxyIDs)})
}

// PreviewProxy GET /api/v1/admin/proxy-pools/:id/preview?platform=kiro
// 返回下次分配会选中的代理，OAuth 流程据此先用该代理完成授权，创建账号时再校验并锁定。
func (h *ProxyPoolHandler) PreviewProxy(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	platform := c.Query("platform")
	if platform == "" {
		response.BadRequest(c, "platform is required")
		return
	}
	p, err := h.poolService.PreviewProxy(c.Request.Context(), id, platform)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ProxyFromServiceAdmin(p))
}

// ProxyBindings GET /api/v1/admin/proxies/:id/bindings
func (h *ProxyPoolHandler) ProxyBindings(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	bindings, err := h.poolService.ListBindingsByProxy(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]ProxyAccountBindingDTO, 0, len(bindings))
	summary := map[string]map[string]int{}
	seen := map[string]map[int64]struct{}{}
	for i := range bindings {
		b := &bindings[i]
		out = append(out, proxyAccountBindingDTO(b))
		if seen[b.Platform] == nil {
			seen[b.Platform] = map[int64]struct{}{}
			summary[b.Platform] = map[string]int{"history": 0, "active": 0}
		}
		if _, dup := seen[b.Platform][b.AccountID]; !dup {
			seen[b.Platform][b.AccountID] = struct{}{}
			summary[b.Platform]["history"]++
		}
		if b.UnboundAt == nil {
			summary[b.Platform]["active"]++
		}
	}
	platforms := make([]string, 0, len(summary))
	for p := range summary {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	response.Success(c, gin.H{"bindings": out, "summary": summary, "platforms": platforms})
}

// AccountBinding GET /api/v1/admin/accounts/:id/proxy-binding
func (h *ProxyPoolHandler) AccountBinding(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	b, err := h.poolService.GetAccountBinding(c.Request.Context(), id)
	if err != nil {
		if err == service.ErrProxyPoolBindingNotFound {
			response.Success(c, nil)
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	out := proxyAccountBindingDTO(b)
	response.Success(c, out)
}

// AssignAccount POST /api/v1/admin/accounts/:id/proxy-pool  {"pool_id":1}
func (h *ProxyPoolHandler) AssignAccount(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		PoolID int64 `json:"pool_id" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	p, err := h.poolService.AssignAccount(c.Request.Context(), id, req.PoolID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ProxyFromServiceAdmin(p))
}

// UnbindAccount POST /api/v1/admin/accounts/:id/proxy-unbind
// 解除代理池锁定，代理保持不变；之后可在账号编辑中更换或清除代理。
func (h *ProxyPoolHandler) UnbindAccount(c *gin.Context) {
	id, ok := parseProxyPoolIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.poolService.UnbindAccount(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Proxy binding unlocked"})
}

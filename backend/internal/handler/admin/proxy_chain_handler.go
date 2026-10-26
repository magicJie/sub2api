package admin

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 链式代理（fork）：前置代理、加密协议参数、分享链接解析与默认前置代理设置。

// ProxyChainRequest 是创建代理请求中的链式代理字段。
type ProxyChainRequest struct {
	UpstreamMode       string            `json:"upstream_mode" binding:"omitempty,oneof=direct default proxy hop_group"`
	UpstreamProxyID    *int64            `json:"upstream_proxy_id"`
	UpstreamHopGroupID *int64            `json:"upstream_hop_group_id"`
	Options            map[string]string `json:"options"`
}

func (r ProxyChainRequest) toInput() service.ProxyChainInput {
	return service.ProxyChainInput{
		UpstreamMode:       strings.TrimSpace(r.UpstreamMode),
		UpstreamProxyID:    r.UpstreamProxyID,
		UpstreamHopGroupID: r.UpstreamHopGroupID,
		Options:            r.Options,
	}
}

// ProxyChainUpdateRequest 是更新代理请求中的链式代理字段；省略的字段保持不变。
type ProxyChainUpdateRequest struct {
	UpstreamMode       string                 `json:"upstream_mode" binding:"omitempty,oneof=direct default proxy hop_group"`
	UpstreamProxyID    dto.NullableInt64Field `json:"upstream_proxy_id"`
	UpstreamHopGroupID dto.NullableInt64Field `json:"upstream_hop_group_id"`
	Options            nullableStringMap      `json:"options"`
}

func (r ProxyChainUpdateRequest) toInput() service.ProxyChainUpdateInput {
	return service.ProxyChainUpdateInput{
		UpstreamMode:            strings.TrimSpace(r.UpstreamMode),
		UpstreamProxyID:         r.UpstreamProxyID.Value,
		ClearUpstreamProxyID:    r.UpstreamProxyID.Set && r.UpstreamProxyID.Value == nil,
		UpstreamHopGroupID:      r.UpstreamHopGroupID.Value,
		ClearUpstreamHopGroupID: r.UpstreamHopGroupID.Set && r.UpstreamHopGroupID.Value == nil,
		Options:                 r.Options.Value,
		OptionsSet:              r.Options.Set,
	}
}

type nullableStringMap struct {
	Set   bool
	Value map[string]string
}

func (m *nullableStringMap) UnmarshalJSON(data []byte) error {
	m.Set = true
	if string(data) == "null" {
		m.Value = nil
		return nil
	}
	return json.Unmarshal(data, &m.Value)
}

// batchCreateFromLink 按分享链接创建代理；重复或无效时返回 false（计为跳过）。
func (h *ProxyHandler) batchCreateFromLink(ctx context.Context, link string) bool {
	parsed, err := proxychain.ParseLink(link)
	if err != nil {
		return false
	}
	exists, err := h.adminService.CheckProxyExists(ctx, parsed.Host, parsed.Port, parsed.Username, parsed.Password)
	if err != nil || exists {
		return false
	}
	_, err = h.adminService.CreateProxy(ctx, &service.CreateProxyInput{
		Name:     parsed.Name,
		Protocol: parsed.Protocol,
		Host:     parsed.Host,
		Port:     parsed.Port,
		Username: parsed.Username,
		Password: parsed.Password,

		ProxyChainInput: service.ProxyChainInput{Options: parsed.Options},
	})
	return err == nil
}

// ProxyChainHandler 处理链式代理设置与分享链接解析。
type ProxyChainHandler struct {
	chainService *service.ProxyChainService
}

func NewProxyChainHandler(chainService *service.ProxyChainService) *ProxyChainHandler {
	return &ProxyChainHandler{chainService: chainService}
}

// ParseLink 解析分享链接
// POST /api/v1/admin/proxies/parse-link
func (h *ProxyChainHandler) ParseLink(c *gin.Context) {
	var req struct {
		Link string `json:"link" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	parsed, err := h.chainService.ParseShareLink(req.Link)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if parsed.Options == nil {
		parsed.Options = map[string]string{}
	}
	response.Success(c, parsed)
}

type proxyChainSettingsBody struct {
	DefaultUpstreamProxyID    *int64 `json:"default_upstream_proxy_id"`
	DefaultUpstreamHopGroupID *int64 `json:"default_upstream_hop_group_id"`
}

// GetSettings 读取默认前置代理
// GET /api/v1/admin/proxies/chain-settings
func (h *ProxyChainHandler) GetSettings(c *gin.Context) {
	id, err := h.chainService.GetDefaultUpstreamProxyID(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	groupID, err := h.chainService.GetDefaultUpstreamHopGroupID(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, proxyChainSettingsBody{DefaultUpstreamProxyID: id, DefaultUpstreamHopGroupID: groupID})
}

// UpdateSettings 设置默认前置代理（null 清除）
// PUT /api/v1/admin/proxies/chain-settings
func (h *ProxyChainHandler) UpdateSettings(c *gin.Context) {
	var req proxyChainSettingsBody
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.DefaultUpstreamProxyID != nil && *req.DefaultUpstreamProxyID <= 0 {
		req.DefaultUpstreamProxyID = nil
	}
	if req.DefaultUpstreamHopGroupID != nil && *req.DefaultUpstreamHopGroupID <= 0 {
		req.DefaultUpstreamHopGroupID = nil
	}
	if err := h.chainService.SetDefaultUpstream(c.Request.Context(), req.DefaultUpstreamProxyID, req.DefaultUpstreamHopGroupID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, req)
}

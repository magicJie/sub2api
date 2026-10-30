package service

// 链式代理（fork）：AIO、AIO 端口（前置端点）与前置组的管理端操作，见 docs/PROXY_IP_MODEL.md 第 2.2、3.4 节。

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func upstreamInvalid(msg string) error {
	return infraerrors.BadRequest("PROXY_UPSTREAM_INVALID", msg)
}

func (s *ProxyChainService) ListUpstreamAIOs(ctx context.Context) ([]ProxyUpstreamAIO, error) {
	return s.upstreamRepo.ListAIOs(ctx)
}

func (s *ProxyChainService) GetUpstreamAIO(ctx context.Context, id int64) (*ProxyUpstreamAIO, error) {
	return s.upstreamRepo.GetAIO(ctx, id)
}

func (s *ProxyChainService) CreateUpstreamAIO(ctx context.Context, a *ProxyUpstreamAIO) error {
	if err := validateUpstreamAIO(a); err != nil {
		return err
	}
	return s.upstreamRepo.CreateAIO(ctx, a)
}

func (s *ProxyChainService) UpdateUpstreamAIO(ctx context.Context, a *ProxyUpstreamAIO) error {
	if err := validateUpstreamAIO(a); err != nil {
		return err
	}
	if err := s.upstreamRepo.UpdateAIO(ctx, a); err != nil {
		return err
	}
	return s.refreshAndProbe(ctx)
}

func (s *ProxyChainService) DeleteUpstreamAIO(ctx context.Context, id int64) error {
	if err := s.upstreamRepo.DeleteAIO(ctx, id); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

func validateUpstreamAIO(a *ProxyUpstreamAIO) error {
	a.Name = strings.TrimSpace(a.Name)
	a.Host = strings.TrimSpace(a.Host)
	a.Protocol = strings.ToLower(strings.TrimSpace(a.Protocol))
	if a.Name == "" || len(a.Name) > 255 {
		return upstreamInvalid("name must contain 1 to 255 bytes")
	}
	switch a.Protocol {
	case "socks5", "socks5h", "http", "https":
	default:
		return upstreamInvalid("protocol must be socks5, socks5h, http or https")
	}
	if a.Host == "" || len(a.Host) > 255 || strings.ContainsAny(a.Host, "/@ ") {
		return upstreamInvalid("host is invalid")
	}
	if len(a.Username) > 255 || len(a.Password) > 255 {
		return upstreamInvalid("username and password must be at most 255 bytes")
	}
	return validateUpstreamStatus(&a.Status)
}

func validateUpstreamStatus(status *string) error {
	if *status == "" {
		*status = StatusActive
	}
	if *status != StatusActive && *status != "inactive" {
		return upstreamInvalid("status must be active or inactive")
	}
	return nil
}

func (s *ProxyChainService) ListUpstreamEndpoints(ctx context.Context) ([]ProxyUpstreamEndpoint, error) {
	return s.upstreamRepo.ListEndpoints(ctx)
}

func (s *ProxyChainService) GetUpstreamEndpoint(ctx context.Context, id int64) (*ProxyUpstreamEndpoint, error) {
	return s.upstreamRepo.GetEndpoint(ctx, id)
}

func (s *ProxyChainService) CreateUpstreamEndpoint(ctx context.Context, e *ProxyUpstreamEndpoint) error {
	if err := validateUpstreamEndpoint(e); err != nil {
		return err
	}
	if err := s.upstreamRepo.CreateEndpoint(ctx, e); err != nil {
		return err
	}
	return s.refreshAndProbe(ctx)
}

func (s *ProxyChainService) UpdateUpstreamEndpoint(ctx context.Context, e *ProxyUpstreamEndpoint) error {
	if err := validateUpstreamEndpoint(e); err != nil {
		return err
	}
	if err := s.upstreamRepo.UpdateEndpoint(ctx, e); err != nil {
		return err
	}
	return s.refreshAndProbe(ctx)
}

func (s *ProxyChainService) DeleteUpstreamEndpoint(ctx context.Context, id int64) error {
	if err := s.upstreamRepo.DeleteEndpoint(ctx, id); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

// validateUpstreamEndpoint 只校验端口自身字段；协议、主机与认证由所属 AIO 提供。
func validateUpstreamEndpoint(e *ProxyUpstreamEndpoint) error {
	e.Name = strings.TrimSpace(e.Name)
	if e.AIOID <= 0 {
		return upstreamInvalid("aio_id is required")
	}
	if e.Name == "" || len(e.Name) > 255 {
		return upstreamInvalid("name must contain 1 to 255 bytes")
	}
	if e.Port < 1 || e.Port > 65535 {
		return upstreamInvalid("port must be 1-65535")
	}
	return validateUpstreamStatus(&e.Status)
}

func (s *ProxyChainService) ListUpstreamGroups(ctx context.Context) ([]ProxyUpstreamGroup, error) {
	return s.upstreamRepo.ListGroups(ctx)
}

func (s *ProxyChainService) GetUpstreamGroup(ctx context.Context, id int64) (*ProxyUpstreamGroup, error) {
	return s.upstreamRepo.GetGroup(ctx, id)
}

func (s *ProxyChainService) CreateUpstreamGroup(ctx context.Context, g *ProxyUpstreamGroup) error {
	if err := validateUpstreamGroup(g); err != nil {
		return err
	}
	if err := s.upstreamRepo.CreateGroup(ctx, g); err != nil {
		return err
	}
	return s.refreshAndProbe(ctx)
}

func (s *ProxyChainService) UpdateUpstreamGroup(ctx context.Context, g *ProxyUpstreamGroup) error {
	if err := validateUpstreamGroup(g); err != nil {
		return err
	}
	if err := s.upstreamRepo.UpdateGroup(ctx, g); err != nil {
		return err
	}
	return s.refreshAndProbe(ctx)
}

func (s *ProxyChainService) DeleteUpstreamGroup(ctx context.Context, id int64) error {
	if err := s.upstreamRepo.DeleteGroup(ctx, id); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

// refreshAndProbe 在端点/组变更后刷新快照并立即探测一轮，让新配置尽快有健康结果。
func (s *ProxyChainService) refreshAndProbe(ctx context.Context) error {
	if err := s.Refresh(ctx); err != nil {
		return err
	}
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), 2*proxyUpstreamProbeTimeout)
		defer cancel()
		s.runtime.ProbeAll(pctx)
	}()
	return nil
}

func validateUpstreamGroup(g *ProxyUpstreamGroup) error {
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" || len(g.Name) > 255 {
		return upstreamInvalid("name must contain 1 to 255 bytes")
	}
	if g.Status == "" {
		g.Status = StatusActive
	}
	if g.Status != StatusActive && g.Status != "inactive" {
		return upstreamInvalid("status must be active or inactive")
	}
	if g.ProbeURL == "" {
		g.ProbeURL = ProxyUpstreamDefaultProbeURL
	}
	if parsed, err := url.Parse(g.ProbeURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return upstreamInvalid("probe URL must be an HTTP(S) URL without user information")
	}
	if g.ProbeIntervalSeconds < 5 || g.ProbeIntervalSeconds > 3600 {
		return upstreamInvalid("probe_interval_seconds must be 5-3600")
	}
	if g.FailThreshold < 1 || g.FailThreshold > 20 || g.RecoverThreshold < 1 || g.RecoverThreshold > 20 {
		return upstreamInvalid("fail_threshold and recover_threshold must be 1-20")
	}
	if g.FailbackSeconds < 0 || g.FailbackSeconds > 86400 {
		return upstreamInvalid("failback_seconds must be 0-86400")
	}
	if len(g.Slots) == 0 {
		return upstreamInvalid("at least one slot is required")
	}
	if len(g.Slots) > 64 {
		return upstreamInvalid("at most 64 slots are allowed")
	}
	for i := range g.Slots {
		slot := &g.Slots[i]
		slot.Name = strings.TrimSpace(slot.Name)
		if slot.Name == "" {
			slot.Name = "slot-" + strconv.Itoa(i+1)
		}
		if len(slot.Name) > 255 {
			return upstreamInvalid("slot name must be at most 255 bytes")
		}
		if slot.Weight == 0 {
			slot.Weight = ProxyUpstreamDefaultSlotWeight
		}
		if slot.Weight < 1 || slot.Weight > ProxyUpstreamMaxSlotWeight {
			return upstreamInvalid("slot weight must be 1-100")
		}
		if len(slot.EndpointIDs) == 0 {
			return upstreamInvalid("each slot requires at least one endpoint")
		}
		seen := map[int64]bool{}
		for _, id := range slot.EndpointIDs {
			if id <= 0 || seen[id] {
				return upstreamInvalid("slot endpoints must be distinct existing endpoints")
			}
			seen[id] = true
		}
	}
	return nil
}

// UpstreamSlotRuntime 返回槽位运行时状态（无运行时时为 nil）。
func (s *ProxyChainService) UpstreamSlotRuntime(slotID int64) *UpstreamSlotRuntime {
	return s.runtime.SlotRuntime(slotID)
}

// UpstreamEndpointHealth 返回端点健康汇总。
func (s *ProxyChainService) UpstreamEndpointHealth(endpointID int64) UpstreamEndpointHealth {
	return s.runtime.EndpointHealth(endpointID)
}

// UpstreamGroupProxyCounts 返回各前置组被未删除代理引用的数量。
func (s *ProxyChainService) UpstreamGroupProxyCounts() map[int64]int {
	out := map[int64]int{}
	for _, p := range currentProxyChainSnapshot().byID {
		if NormalizeUpstreamMode(p.UpstreamMode) == UpstreamModeGroup && p.UpstreamGroupID != nil {
			out[*p.UpstreamGroupID]++
		}
	}
	return out
}

// ProxyUpstreamGroupName 返回前置组名称（导出用，不含端点信息）。
func ProxyUpstreamGroupName(id *int64) string {
	rt := activeUpstreamRuntime()
	if id == nil || rt == nil {
		return ""
	}
	return rt.group(*id).Name
}

// ResolveProxyUpstreamGroupName 按名称查找前置组 id（导入用）。
func ResolveProxyUpstreamGroupName(name string) *int64 {
	rt := activeUpstreamRuntime()
	if name == "" || rt == nil {
		return nil
	}
	return rt.groupIDByName(name)
}

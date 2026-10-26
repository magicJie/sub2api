package service

// 链式代理（fork）：首跳组生命周期、mihomo 运行时与默认前置代理设置。

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/hopgroup"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
)

const SettingKeyProxyChainDefaultHopGroupID = "proxy_chain_default_upstream_hop_group_id"

var ErrProxyHopGroupInUse = infraerrors.Conflict("PROXY_HOP_GROUP_IN_USE", "first-hop group is referenced by a proxy or global default")

func (s *ProxyChainService) ListHopGroups(ctx context.Context) ([]ProxyHopGroup, error) {
	return s.hopGroupRepo.List(ctx)
}

func (s *ProxyChainService) GetHopGroup(ctx context.Context, id int64) (*ProxyHopGroup, error) {
	return s.hopGroupRepo.Get(ctx, id)
}

func (s *ProxyChainService) CreateHopGroup(ctx context.Context, group *ProxyHopGroup) error {
	if err := s.validateHopGroup(ctx, group); err != nil {
		return err
	}
	if err := s.hopGroupRepo.Create(ctx, group); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

func (s *ProxyChainService) UpdateHopGroup(ctx context.Context, group *ProxyHopGroup) error {
	if _, err := s.hopGroupRepo.Get(ctx, group.ID); err != nil {
		return err
	}
	if err := s.validateHopGroup(ctx, group); err != nil {
		return err
	}
	if err := s.hopGroupRepo.Update(ctx, group); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

func (s *ProxyChainService) DeleteHopGroup(ctx context.Context, id int64) error {
	proxies, err := loadProxyMap(ctx, s.proxyRepo)
	if err != nil {
		return err
	}
	for _, p := range proxies {
		if NormalizeUpstreamMode(p.UpstreamMode) == UpstreamModeHopGroup && p.UpstreamHopGroupID != nil && *p.UpstreamHopGroupID == id {
			return ErrProxyHopGroupInUse
		}
	}
	defaultID, err := readDefaultHopGroupID(ctx, s.settingRepo)
	if err != nil {
		return err
	}
	if defaultID != nil && *defaultID == id {
		return ErrProxyHopGroupInUse
	}
	if err := s.hopGroupRepo.Delete(ctx, id); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

func (s *ProxyChainService) HopGroupStatus(id int64) (hopgroup.GroupStatus, error) {
	if s.hopRuntime == nil {
		return hopgroup.GroupStatus{}, errors.New("first-hop runtime unavailable")
	}
	return s.hopRuntime.Status(id)
}

func (s *ProxyChainService) validateHopGroup(ctx context.Context, group *ProxyHopGroup) error {
	group.Name = strings.TrimSpace(group.Name)
	if group.Name == "" || len(group.Name) > 255 {
		return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "name must contain 1 to 255 bytes")
	}
	if group.Strategy == "" {
		group.Strategy = "fallback"
	}
	if group.Strategy != "fallback" && group.Strategy != "url-test" {
		return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "strategy must be fallback or url-test")
	}
	if group.TestURL == "" {
		group.TestURL = "https://www.gstatic.com/generate_204"
	}
	if !validHopGroupHTTPURL(group.TestURL) {
		return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "test URL must be an HTTP(S) URL")
	}
	if group.IntervalSeconds == 0 {
		group.IntervalSeconds = 60
	}
	if group.SubscriptionUpdateInterval == 0 {
		group.SubscriptionUpdateInterval = 3600
	}
	if group.IntervalSeconds < 1 || group.SubscriptionUpdateInterval < 1 || group.ToleranceMS < 0 {
		return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "intervals must be positive and tolerance nonnegative")
	}
	if group.Status == "" {
		group.Status = StatusActive
	}
	if group.Status != StatusActive && group.Status != "inactive" {
		return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "status must be active or inactive")
	}
	for _, raw := range group.SubscriptionURLs {
		if !validHopGroupHTTPURL(raw) {
			return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "subscription URLs must use HTTP(S) without user information")
		}
	}
	if len(group.SubscriptionURLs) == 0 && len(group.MemberProxyIDs) == 0 {
		return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "at least one subscription or manual member is required")
	}
	byID, err := loadProxyMap(ctx, s.proxyRepo)
	if err != nil {
		return err
	}
	seen := make(map[int64]bool, len(group.MemberProxyIDs))
	for _, id := range group.MemberProxyIDs {
		p, ok := byID[id]
		if !ok || id <= 0 || seen[id] {
			return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "manual members must be distinct existing proxies")
		}
		seen[id] = true
		if NormalizeUpstreamMode(p.UpstreamMode) != UpstreamModeDirect {
			return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "manual members must be direct proxies without upstreams")
		}
		if err := p.chainHop().Validate(); err != nil {
			return infraerrors.BadRequest("PROXY_HOP_GROUP_INVALID", "manual member has invalid proxy configuration")
		}
	}
	return nil
}

func validHopGroupHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
}

func (s *ProxyChainService) refreshHopGroups(ctx context.Context, byID map[int64]Proxy) (map[int64]ProxyHopGroup, *int64, error) {
	groups := make(map[int64]ProxyHopGroup)
	if s.hopGroupRepo != nil {
		list, err := s.hopGroupRepo.List(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, group := range list {
			groups[group.ID] = group
		}
	}
	defaultID, err := readDefaultHopGroupID(ctx, s.settingRepo)
	if err != nil {
		return nil, nil, err
	}
	runtimeGroups := make([]hopgroup.Group, 0, len(groups))
	now := time.Now()
	for _, group := range groups {
		if group.Status != StatusActive {
			continue
		}
		runtimeGroup := hopgroup.Group{ID: group.ID, Name: group.Name, Strategy: group.Strategy, TestURL: group.TestURL, IntervalSeconds: group.IntervalSeconds, ToleranceMS: group.ToleranceMS, SubscriptionURLs: group.SubscriptionURLs, SubscriptionUpdateInterval: group.SubscriptionUpdateInterval}
		for _, id := range group.MemberProxyIDs {
			p, ok := byID[id]
			if ok && p.IsActive() && !p.IsExpired(now) && NormalizeUpstreamMode(p.UpstreamMode) == UpstreamModeDirect {
				runtimeGroup.Members = append(runtimeGroup.Members, p.chainHop())
			}
		}
		runtimeGroups = append(runtimeGroups, runtimeGroup)
	}
	if s.hopRuntime == nil {
		if len(runtimeGroups) != 0 {
			return nil, nil, errors.New("first-hop runtime unavailable")
		}
		return groups, defaultID, nil
	}
	if err := s.hopRuntime.Replace(ctx, runtimeGroups); err != nil {
		return nil, nil, errors.New("first-hop runtime configuration could not be applied")
	}
	return groups, defaultID, nil
}

func proxyChainRuntimeHops(chain []Proxy) ([]proxychain.Hop, error) {
	hops := make([]proxychain.Hop, len(chain))
	for i := range chain {
		if chain[i].Protocol == UpstreamModeHopGroup {
			if i != 0 || chain[i].ID >= 0 {
				return nil, errors.New("first-hop group must be first in chain")
			}
			service := activeProxyChainService.Load()
			if service == nil || service.hopRuntime == nil {
				return nil, errors.New("first-hop runtime unavailable")
			}
			endpoint, err := service.hopRuntime.Endpoint(-chain[i].ID)
			if err != nil {
				return nil, errors.New("first-hop group is unavailable")
			}
			hops[i] = endpoint
		} else {
			hops[i] = chain[i].chainHop()
		}
	}
	return hops, nil
}

func readDefaultHopGroupID(ctx context.Context, repo SettingRepository) (*int64, error) {
	if repo == nil {
		return currentProxyChainSnapshot().defaultHopGroupID, nil
	}
	raw, err := repo.GetValue(ctx, SettingKeyProxyChainDefaultHopGroupID)
	if errors.Is(err, ErrSettingNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("invalid default first-hop group setting")
	}
	return &id, nil
}

func loadHopGroupValidation(ctx context.Context, settings SettingRepository) (map[int64]ProxyHopGroup, *int64, error) {
	groups := currentProxyChainSnapshot().hopGroups
	if s := activeProxyChainService.Load(); s != nil && s.hopGroupRepo != nil {
		list, err := s.hopGroupRepo.List(ctx)
		if err != nil {
			return nil, nil, err
		}
		groups = make(map[int64]ProxyHopGroup, len(list))
		for _, group := range list {
			groups[group.ID] = group
		}
	}
	defaultID, err := readDefaultHopGroupID(ctx, settings)
	return groups, defaultID, err
}

func (s *ProxyChainService) GetDefaultUpstreamHopGroupID(ctx context.Context) (*int64, error) {
	return readDefaultHopGroupID(ctx, s.settingRepo)
}

func (s *ProxyChainService) SetDefaultUpstream(ctx context.Context, proxyID, groupID *int64) error {
	if proxyID != nil && groupID != nil {
		return infraerrors.BadRequest("PROXY_UPSTREAM_MODE_MISMATCH", "select either a default proxy or first-hop group")
	}
	byID, err := loadProxyMap(ctx, s.proxyRepo)
	if err != nil {
		return err
	}
	groups, _, err := loadHopGroupValidation(ctx, s.settingRepo)
	if err != nil {
		return err
	}
	if proxyID != nil {
		target, ok := byID[*proxyID]
		if !ok {
			return ErrProxyNotFound
		}
		if !target.IsActive() || target.IsExpired(time.Now()) {
			return infraerrors.BadRequest("PROXY_UPSTREAM_INACTIVE", "default upstream proxy must be active")
		}
	}
	if groupID != nil {
		group, ok := groups[*groupID]
		if !ok || group.Status != StatusActive {
			return infraerrors.BadRequest("PROXY_UPSTREAM_INACTIVE", "default first-hop group must exist and be active")
		}
	}
	now := time.Now()
	for _, p := range byID {
		if NormalizeUpstreamMode(p.UpstreamMode) == UpstreamModeDirect || !p.IsActive() || p.IsExpired(now) {
			continue
		}
		if _, err := resolveProxyChainWithGroups(&p, byID, proxyID, groups, groupID, now); err != nil {
			return infraerrors.BadRequest("PROXY_CHAIN_INVALID", fmt.Sprintf("proxy %q: %v", p.Name, err))
		}
	}
	values := map[string]string{SettingKeyProxyChainDefaultUpstreamID: "", SettingKeyProxyChainDefaultHopGroupID: ""}
	if proxyID != nil {
		values[SettingKeyProxyChainDefaultUpstreamID] = strconv.FormatInt(*proxyID, 10)
	}
	if groupID != nil {
		values[SettingKeyProxyChainDefaultHopGroupID] = strconv.FormatInt(*groupID, 10)
	}
	if err := s.settingRepo.SetMultiple(ctx, values); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

func validateHopGroupMemberChange(ctx context.Context, p *Proxy) error {
	if NormalizeUpstreamMode(p.UpstreamMode) == UpstreamModeDirect {
		return nil
	}
	return checkHopGroupMemberInUse(ctx, p.ID)
}

func checkHopGroupMemberInUse(ctx context.Context, id int64) error {
	s := activeProxyChainService.Load()
	if s == nil || s.hopGroupRepo == nil {
		return nil
	}
	groups, err := s.hopGroupRepo.List(ctx)
	if err != nil {
		return err
	}
	for _, group := range groups {
		for _, memberID := range group.MemberProxyIDs {
			if memberID == id {
				return infraerrors.Conflict("PROXY_HOP_GROUP_MEMBER_IN_USE", "proxy is a manual member of a first-hop group")
			}
		}
	}
	return nil
}

func DefaultUpstreamHopGroupID() *int64 { return currentProxyChainSnapshot().defaultHopGroupID }

// ProxyHopGroupName exports only a non-secret reference, never subscription data.
func ProxyHopGroupName(id *int64) string {
	if id == nil {
		return ""
	}
	return currentProxyChainSnapshot().hopGroups[*id].Name
}

func ResolveProxyHopGroupName(name string) *int64 {
	for id, group := range currentProxyChainSnapshot().hopGroups {
		if name != "" && group.Name == name && group.Status == StatusActive {
			value := id
			return &value
		}
	}
	return nil
}

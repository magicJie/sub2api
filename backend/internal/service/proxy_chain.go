package service

// 链式代理（fork）：前置代理解析、默认前置代理设置与 Proxy.URL() 挂钩。
//
// Proxy.URL() 没有 ctx/仓储依赖，且被 60+ 处调用；为了不改这些调用点，这里维护一个
// 进程级的代理快照（id -> Proxy + 默认前置代理 id），由 ProxyChainService 定期刷新、
// 并在管理端增删改代理后立即刷新。需要链路的代理（有前置代理，或协议是 ss/vmess/vless/trojan）
// 的 URL() 返回本地 Xray 入口的 socks5h:// 地址；普通直连 http/socks 代理的 URL() 保持不变。
//
// 链路无法解析（前置代理缺失/停用/过期、成环、超长）时 URL() 返回一个无法解析的地址，
// 让调用方 fail-fast，而不是悄悄绕过前置代理直连出口（会暴露真实 IP）。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
)

// 前置代理模式。
const (
	UpstreamModeDirect  = "direct"  // 不经前置代理
	UpstreamModeDefault = "default" // 跟随全局默认前置代理
	UpstreamModeProxy   = "proxy"   // 使用 UpstreamProxyID 指定的代理
)

// SettingKeyProxyChainDefaultUpstreamID 是全局默认前置代理 id（空表示未设置）。
const SettingKeyProxyChainDefaultUpstreamID = "proxy_chain_default_upstream_proxy_id"

const (
	proxyChainRefreshInterval = 30 * time.Second
	// 无效 scheme 让 proxyurl.Parse 直接报错；绕过校验的调用方会拨 127.0.0.1:0 立即失败。
	proxyChainInvalidURL = "proxychain-unavailable://127.0.0.1:0"
)

var (
	ErrProxyUpstreamInUse = infraerrors.Conflict("PROXY_UPSTREAM_IN_USE", "proxy is used as upstream proxy by other proxies or as default upstream proxy")
	errProxyChainInvalid  = errors.New("invalid proxy chain")
)

// ProxyChainHop 是展示用的链路节点。
type ProxyChainHop struct {
	ID       int64
	Name     string
	Protocol string
}

type proxyChainSnapshot struct {
	byID              map[int64]Proxy
	defaultUpstreamID *int64
	loaded            bool
	urls              sync.Map // proxy id -> 已解析的链 URL（快照不可变，替换快照即失效）
}

type cachedChainURL struct {
	url string
	gen uint64
}

var proxyChainState atomic.Pointer[proxyChainSnapshot]

func currentProxyChainSnapshot() *proxyChainSnapshot {
	if s := proxyChainState.Load(); s != nil {
		return s
	}
	return &proxyChainSnapshot{byID: map[int64]Proxy{}}
}

// NormalizeUpstreamMode 规范化前置代理模式，空值视为直连。
func NormalizeUpstreamMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case UpstreamModeDefault:
		return UpstreamModeDefault
	case UpstreamModeProxy:
		return UpstreamModeProxy
	default:
		return UpstreamModeDirect
	}
}

func (p *Proxy) chainHop() proxychain.Hop {
	return proxychain.Hop{
		Protocol: p.Protocol,
		Host:     p.Host,
		Port:     p.Port,
		Username: p.Username,
		Password: p.Password,
		Options:  p.Options,
	}
}

// resolveProxyChain 返回 p 的完整链路（按拨号顺序，最后一个是 p 自身）。
// 前置代理从 byID 快照中取；p 自身使用传入的值（可能比快照新）。
func resolveProxyChain(p *Proxy, byID map[int64]Proxy, defaultUpstreamID *int64, now time.Time) ([]Proxy, error) {
	chain := []Proxy{*p}
	visited := map[int64]struct{}{}
	if p.ID > 0 {
		visited[p.ID] = struct{}{}
	}
	cur := *p
	for {
		var nextID *int64
		switch NormalizeUpstreamMode(cur.UpstreamMode) {
		case UpstreamModeProxy:
			if cur.UpstreamProxyID == nil {
				return nil, fmt.Errorf("%w: proxy %q has no upstream proxy selected", errProxyChainInvalid, cur.Name)
			}
			nextID = cur.UpstreamProxyID
		case UpstreamModeDefault:
			// 未配置默认前置代理时 fail-closed（不能悄悄直连出口）；默认前置代理自身按直连处理。
			if defaultUpstreamID == nil {
				return nil, fmt.Errorf("%w: proxy %q follows default upstream proxy but none is configured", errProxyChainInvalid, cur.Name)
			}
			if *defaultUpstreamID != cur.ID {
				nextID = defaultUpstreamID
			}
		}
		if nextID == nil {
			break
		}
		if _, seen := visited[*nextID]; seen {
			return nil, fmt.Errorf("%w: upstream proxy loop detected at proxy id %d", errProxyChainInvalid, *nextID)
		}
		next, ok := byID[*nextID]
		if !ok {
			return nil, fmt.Errorf("%w: upstream proxy id %d not found", errProxyChainInvalid, *nextID)
		}
		if !next.IsActive() || next.IsExpired(now) {
			return nil, fmt.Errorf("%w: upstream proxy %q is not active", errProxyChainInvalid, next.Name)
		}
		visited[next.ID] = struct{}{}
		chain = append([]Proxy{next}, chain...)
		if len(chain) > proxychain.MaxHops {
			return nil, fmt.Errorf("%w: chain exceeds %d hops", errProxyChainInvalid, proxychain.MaxHops)
		}
		cur = next
	}
	return chain, nil
}

func proxyChainNeedsXray(chain []Proxy) bool {
	return len(chain) > 1 || proxychain.IsXrayOnlyProtocol(chain[0].Protocol)
}

var proxyChainErrLogAt sync.Map // proxy id -> time.Time

// proxyChainURL 是 Proxy.URL() 的挂钩：ok=false 表示不需要链路，沿用原始 URL。
func proxyChainURL(p *Proxy) (string, bool) {
	snap := currentProxyChainSnapshot()
	// 调用方的 Proxy 可能来自调度器 Redis 缓存（链字段滞后），自身配置一律以快照为准。
	self := p
	if fresh, ok := snap.byID[p.ID]; ok && p.ID > 0 {
		self = &fresh
	}
	mode := NormalizeUpstreamMode(self.UpstreamMode)
	if mode == UpstreamModeDirect && !proxychain.IsXrayOnlyProtocol(self.Protocol) {
		if self != p {
			// 快照里是直连普通代理：用快照值构造 URL，避免沿用缓存里过期的链配置。
			return self.directURL(), true
		}
		return "", false
	}
	if !snap.loaded {
		logProxyChainError(p.ID, errors.New("proxy chain snapshot not loaded yet"))
		return proxyChainInvalidURL, true
	}
	cacheable := self != p // 只缓存快照里的代理；调用方临时构造的配置每次重新解析
	gen := proxychain.Default().Generation()
	if cacheable {
		if c, ok := snap.urls.Load(p.ID); ok && c.(cachedChainURL).gen == gen {
			return c.(cachedChainURL).url, true
		}
	}
	p = self
	chain, err := resolveProxyChain(p, snap.byID, snap.defaultUpstreamID, time.Now())
	if err == nil && !proxyChainNeedsXray(chain) {
		return p.directURL(), true
	}
	if err == nil {
		hops := make([]proxychain.Hop, len(chain))
		for i := range chain {
			hops[i] = chain[i].chainHop()
		}
		var u string
		if u, err = proxychain.Default().URL(hops); err == nil {
			if cacheable {
				snap.urls.Store(p.ID, cachedChainURL{url: u, gen: gen})
			}
			return u, true
		}
	}
	logProxyChainError(p.ID, err)
	return proxyChainInvalidURL, true
}

func logProxyChainError(id int64, err error) {
	now := time.Now()
	if last, ok := proxyChainErrLogAt.Load(id); ok && now.Sub(last.(time.Time)) < time.Minute {
		return
	}
	proxyChainErrLogAt.Store(id, now)
	slog.Warn("proxy_chain_unavailable", "proxy_id", id, "error", err) // err 已由 proxychain 脱敏
}

// DescribeProxyChain 返回 p 的前置链路（不含 p 自身）与解析错误，供列表展示。
func DescribeProxyChain(p *Proxy) ([]ProxyChainHop, string) {
	snap := currentProxyChainSnapshot()
	chain, err := resolveProxyChain(p, snap.byID, snap.defaultUpstreamID, time.Now())
	if err != nil {
		return nil, err.Error()
	}
	hops := make([]ProxyChainHop, 0, len(chain)-1)
	for _, c := range chain[:len(chain)-1] {
		hops = append(hops, ProxyChainHop{ID: c.ID, Name: c.Name, Protocol: c.Protocol})
	}
	return hops, ""
}

// DefaultUpstreamProxyID 返回当前默认前置代理 id（来自快照）。
func DefaultUpstreamProxyID() *int64 {
	return currentProxyChainSnapshot().defaultUpstreamID
}

// ProxyChainService 负责刷新代理快照、维护默认前置代理设置。
type ProxyChainService struct {
	proxyRepo   ProxyRepository
	settingRepo SettingRepository
	mu          sync.Mutex
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func NewProxyChainService(proxyRepo ProxyRepository, settingRepo SettingRepository) *ProxyChainService {
	return &ProxyChainService{proxyRepo: proxyRepo, settingRepo: settingRepo, stopCh: make(chan struct{})}
}

var activeProxyChainService atomic.Pointer[ProxyChainService]

// RefreshProxyChainSnapshot 在代理增删改后调用（admin_proxy 中的一行挂钩）。
func RefreshProxyChainSnapshot(ctx context.Context) {
	if s := activeProxyChainService.Load(); s != nil {
		if err := s.Refresh(ctx); err != nil {
			slog.Warn("proxy_chain_refresh_failed", "error", err)
		}
	}
}

// Start 同步加载一次快照，然后周期刷新（代理过期等非管理端变化靠周期刷新感知）。
func (s *ProxyChainService) Start() {
	if s == nil || s.proxyRepo == nil {
		return
	}
	activeProxyChainService.Store(s)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := s.Refresh(ctx); err != nil {
		slog.Warn("proxy_chain_initial_refresh_failed", "error", err)
	}
	cancel()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(proxyChainRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := s.Refresh(ctx); err != nil {
					slog.Warn("proxy_chain_refresh_failed", "error", err)
				}
				cancel()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *ProxyChainService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	activeProxyChainService.CompareAndSwap(s, nil)
}

func (s *ProxyChainService) loadDefaultUpstreamID(ctx context.Context) (*int64, error) {
	if s.settingRepo == nil {
		return nil, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyProxyChainDefaultUpstreamID)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil, nil
		}
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, nil
	}
	return &id, nil
}

// GetDefaultUpstreamProxyID 读取当前默认前置代理设置。
func (s *ProxyChainService) GetDefaultUpstreamProxyID(ctx context.Context) (*int64, error) {
	return s.loadDefaultUpstreamID(ctx)
}

// ParseShareLink 解析分享链接为代理配置。
func (s *ProxyChainService) ParseShareLink(link string) (*proxychain.ParsedLink, error) {
	parsed, err := proxychain.ParseLink(link)
	if err != nil {
		return nil, infraerrors.BadRequest("PROXY_LINK_INVALID", err.Error())
	}
	return parsed, nil
}

// ProxyChainInput 是创建代理时的链式代理字段（嵌入 CreateProxyInput）。
type ProxyChainInput struct {
	UpstreamMode    string
	UpstreamProxyID *int64
	Options         map[string]string
}

// ProxyChainUpdateInput 是更新代理时的链式代理字段（嵌入 UpdateProxyInput）。
// 空 UpstreamMode 与 nil Options 表示保持不变；ClearUpstreamProxyID 显式清除前置代理。
type ProxyChainUpdateInput struct {
	UpstreamMode         string
	UpstreamProxyID      *int64
	ClearUpstreamProxyID bool
	Options              map[string]string
	OptionsSet           bool
}

func applyProxyChainCreate(ctx context.Context, repo ProxyRepository, settingRepo SettingRepository, p *Proxy, in *ProxyChainInput) error {
	p.UpstreamMode = in.UpstreamMode
	p.UpstreamProxyID = in.UpstreamProxyID
	if in.UpstreamProxyID != nil && NormalizeUpstreamMode(in.UpstreamMode) != UpstreamModeProxy {
		return infraerrors.BadRequest("PROXY_UPSTREAM_MODE_MISMATCH", "upstream_proxy_id requires upstream_mode=proxy")
	}
	p.Options = in.Options
	return validateProxyChainConfig(ctx, repo, settingRepo, p)
}

func (p *Proxy) directURL() string {
	u := &url.URL{
		Scheme: p.Protocol,
		Host:   net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
	}
	if p.Username != "" && p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u.String()
}

// Refresh 重新加载全部代理与默认前置代理设置，并注销不再被任何代理使用的链。
func (s *ProxyChainService) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID, err := loadProxyMap(ctx, s.proxyRepo)
	if err != nil {
		return err
	}
	defaultID, err := s.loadDefaultUpstreamID(ctx)
	if err != nil {
		return err
	}
	proxyChainState.Store(&proxyChainSnapshot{byID: byID, defaultUpstreamID: defaultID, loaded: true})
	proxychain.Default().Retain(activeChainHops(byID, defaultID))
	return nil
}

// activeChainHops 收集当前所有需要 Xray 的链，供 Manager 回收过期链。
func activeChainHops(byID map[int64]Proxy, defaultID *int64) [][]proxychain.Hop {
	now := time.Now()
	var out [][]proxychain.Hop
	for id := range byID {
		p := byID[id]
		chain, err := resolveProxyChain(&p, byID, defaultID, now)
		if err != nil || !proxyChainNeedsXray(chain) {
			continue
		}
		hops := make([]proxychain.Hop, len(chain))
		for i := range chain {
			hops[i] = chain[i].chainHop()
		}
		out = append(out, hops)
	}
	return out
}

func loadProxyMap(ctx context.Context, repo ProxyRepository) (map[int64]Proxy, error) {
	proxies, err := repo.ListAllForFallback(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Proxy, len(proxies))
	for _, p := range proxies {
		byID[p.ID] = p
	}
	return byID, nil
}

// SetDefaultUpstreamProxyID 设置（或清除）默认前置代理，并校验所有跟随默认的代理链路仍然有效。
func (s *ProxyChainService) SetDefaultUpstreamProxyID(ctx context.Context, id *int64) error {
	byID, err := loadProxyMap(ctx, s.proxyRepo)
	if err != nil {
		return err
	}
	if id != nil {
		target, ok := byID[*id]
		if !ok {
			return ErrProxyNotFound
		}
		if !target.IsActive() || target.IsExpired(time.Now()) {
			return infraerrors.BadRequest("PROXY_UPSTREAM_INACTIVE", "default upstream proxy must be active")
		}
	}
	if err := validateDependentChains(byID, id, 0); err != nil {
		return err
	}
	value := ""
	if id != nil {
		value = strconv.FormatInt(*id, 10)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyProxyChainDefaultUpstreamID, value); err != nil {
		return err
	}
	return s.Refresh(ctx)
}

// validateDependentChains 校验 byID 中所有带前置代理的代理链路都可解析；
// changedID>0 时只检查链路经过 changedID 的代理（修改/新建某个代理时使用）。
// 已停用/过期的代理不参与（它们本来就不出站）。
func validateDependentChains(byID map[int64]Proxy, defaultID *int64, changedID int64) error {
	now := time.Now()
	for id := range byID {
		p := byID[id]
		if NormalizeUpstreamMode(p.UpstreamMode) == UpstreamModeDirect || !p.IsActive() || p.IsExpired(now) {
			continue
		}
		chain, err := resolveProxyChain(&p, byID, defaultID, now)
		if changedID > 0 && err == nil && !chainContains(chain, changedID) {
			continue
		}
		if err != nil {
			return infraerrors.BadRequest("PROXY_CHAIN_INVALID", fmt.Sprintf("proxy %q: %v", p.Name, err))
		}
	}
	return nil
}

func chainContains(chain []Proxy, id int64) bool {
	for _, c := range chain {
		if c.ID == id {
			return true
		}
	}
	return false
}

// validateProxyChainConfig 在创建/更新代理前校验协议参数与前置链路（admin_proxy 中的挂钩）。
func validateProxyChainConfig(ctx context.Context, repo ProxyRepository, settingRepo SettingRepository, p *Proxy) error {
	p.UpstreamMode = NormalizeUpstreamMode(p.UpstreamMode)
	if p.UpstreamMode != UpstreamModeProxy {
		p.UpstreamProxyID = nil
	}
	p.Options = proxychain.NormalizeOptions(p.Protocol, p.Options)
	// 基础字段（协议白名单、host、port）由 handler binding 校验；这里只校验需要 Xray 承载的协议参数。
	if proxychain.IsXrayOnlyProtocol(p.Protocol) {
		p.Username = ""
		if err := p.chainHop().Validate(); err != nil {
			return infraerrors.BadRequest("PROXY_CONFIG_INVALID", err.Error())
		}
	}
	if p.UpstreamMode == UpstreamModeProxy && p.UpstreamProxyID == nil {
		return infraerrors.BadRequest("PROXY_UPSTREAM_REQUIRED", "upstream proxy required when upstream_mode=proxy")
	}
	if p.UpstreamProxyID != nil && p.ID > 0 && *p.UpstreamProxyID == p.ID {
		return infraerrors.BadRequest("PROXY_UPSTREAM_SELF", "upstream proxy cannot be itself")
	}
	if repo == nil {
		return nil
	}
	byID, err := loadProxyMap(ctx, repo)
	if err != nil {
		return err
	}
	defaultID, err := readDefaultUpstreamID(ctx, settingRepo)
	if err != nil {
		return err
	}
	if p.UpstreamMode != UpstreamModeDirect {
		if _, err := resolveProxyChain(p, byID, defaultID, time.Now()); err != nil {
			return infraerrors.BadRequest("PROXY_CHAIN_INVALID", err.Error())
		}
	}
	// 修改已有代理时，依赖它的其他代理链路也必须仍然有效（成环、超长）。
	// 停用代理不受此限制：允许紧急下线坏节点，依赖它的链路在运行时 fail-closed。
	if p.ID > 0 && p.IsActive() {
		byID[p.ID] = *p
		if err := validateDependentChains(byID, defaultID, p.ID); err != nil {
			return err
		}
	}
	return nil
}

// checkProxyUpstreamInUse 阻止删除仍被引用为前置代理（或为默认前置代理）的代理。
func checkProxyUpstreamInUse(ctx context.Context, repo ProxyRepository, settingRepo SettingRepository, id int64) error {
	defaultID, err := readDefaultUpstreamID(ctx, settingRepo)
	if err != nil {
		return err
	}
	if defaultID != nil && *defaultID == id {
		return ErrProxyUpstreamInUse
	}
	proxies, err := repo.ListAllForFallback(ctx)
	if err != nil {
		return err
	}
	for _, p := range proxies {
		if p.ID != id && NormalizeUpstreamMode(p.UpstreamMode) == UpstreamModeProxy &&
			p.UpstreamProxyID != nil && *p.UpstreamProxyID == id {
			return ErrProxyUpstreamInUse
		}
	}
	return nil
}

func readDefaultUpstreamID(ctx context.Context, settingRepo SettingRepository) (*int64, error) {
	if settingRepo == nil {
		return DefaultUpstreamProxyID(), nil
	}
	return (&ProxyChainService{settingRepo: settingRepo}).loadDefaultUpstreamID(ctx)
}

func applyProxyChainUpdate(ctx context.Context, repo ProxyRepository, settingRepo SettingRepository, p *Proxy, in *ProxyChainUpdateInput) error {
	if in.UpstreamMode != "" {
		p.UpstreamMode = in.UpstreamMode
	}
	if in.UpstreamProxyID != nil || in.ClearUpstreamProxyID {
		p.UpstreamProxyID = in.UpstreamProxyID
	}
	if in.UpstreamProxyID != nil && NormalizeUpstreamMode(p.UpstreamMode) != UpstreamModeProxy {
		return infraerrors.BadRequest("PROXY_UPSTREAM_MODE_MISMATCH", "upstream_proxy_id requires upstream_mode=proxy")
	}
	if in.OptionsSet {
		p.Options = in.Options
	}
	return validateProxyChainConfig(ctx, repo, settingRepo, p)
}

// proxyChainSettingRepo 返回读取默认前置代理设置用的仓储（未注入时回退到进程快照）。
func (s *adminServiceImpl) proxyChainSettingRepo() SettingRepository {
	if s == nil || s.settingService == nil {
		return nil
	}
	return s.settingService.settingRepo
}

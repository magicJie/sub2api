package service

// 链式代理（fork）：前置组解析与 Proxy.URL() 挂钩，见 docs/PROXY_IP_MODEL.md 第 2.2 节。
//
// Proxy.URL() 没有 ctx/仓储依赖，且被 60+ 处调用；为了不改这些调用点，这里维护一个
// 进程级的代理快照（id -> Proxy），由 ProxyChainService 定期刷新、并在管理端增删改代理或前置组后立即刷新。
// 需要链路的代理（前置组，或协议是 ss/vmess/vless/trojan）的 URL() 返回本地 Xray 入口的 socks5h:// 地址；
// 普通直连 http/socks 代理的 URL() 保持不变。
//
// 前置只有两种：直连，或前置组。前置组由 ProxyUpstreamRuntime 选出当前端点，链路为「端点 → 落地」。
// 链路无法解析（组缺失/停用、没有可用槽位）时 URL() 返回一个无法解析的地址，
// 让调用方 fail-fast，而不是悄悄绕过前置直连出口（会暴露真实 IP）。

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

// 前置模式。
const (
	UpstreamModeDirect = "direct" // 不经前置
	UpstreamModeGroup  = "group"  // 经 UpstreamGroupID 指定的前置组
)

const (
	proxyChainRefreshInterval = 30 * time.Second
	// 无效 scheme 让 proxyurl.Parse 直接报错；绕过校验的调用方会拨 127.0.0.1:0 立即失败。
	proxyChainInvalidURL = "proxychain-unavailable://127.0.0.1:0"
)

var errProxyChainInvalid = errors.New("invalid proxy chain")

// ProxyChainHop 是展示用的链路节点。前置端点的 ID 为端点 id，Name 形如「组/槽位/端点」。
type ProxyChainHop struct {
	ID       int64
	Name     string
	Protocol string
}

type proxyChainSnapshot struct {
	byID   map[int64]Proxy
	loaded bool
	urls   sync.Map // proxy id -> 已解析的链 URL（快照不可变，替换快照即失效）
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

// NormalizeUpstreamMode 规范化前置模式，空值与未知值视为直连。
func NormalizeUpstreamMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), UpstreamModeGroup) {
		return UpstreamModeGroup
	}
	return UpstreamModeDirect
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

func (e *ProxyUpstreamEndpoint) chainHop() proxychain.Hop {
	return proxychain.Hop{Protocol: e.Protocol, Host: e.Host, Port: e.Port, Username: e.Username, Password: e.Password}
}

// resolvedProxyChain 是解析后的链路：Upstream 为空表示直连。
type resolvedProxyChain struct {
	Upstream *ProxyUpstreamEndpoint
	Group    *ProxyUpstreamGroup
	Slot     *ProxyUpstreamSlot
}

func (c resolvedProxyChain) hops(p *Proxy) []proxychain.Hop {
	if c.Upstream == nil {
		return []proxychain.Hop{p.chainHop()}
	}
	return []proxychain.Hop{c.Upstream.chainHop(), p.chainHop()}
}

func (c resolvedProxyChain) needsXray(p *Proxy) bool {
	return c.Upstream != nil || proxychain.IsXrayOnlyProtocol(p.Protocol)
}

// resolveProxyChain 解析 p 的前置：前置组模式下由运行时按 rendezvous hash 选槽位当前端点。
func resolveProxyChain(p *Proxy, rt *ProxyUpstreamRuntime) (resolvedProxyChain, error) {
	if NormalizeUpstreamMode(p.UpstreamMode) != UpstreamModeGroup {
		return resolvedProxyChain{}, nil
	}
	if p.UpstreamGroupID == nil {
		return resolvedProxyChain{}, fmt.Errorf("%w: proxy %q has no upstream group selected", errProxyChainInvalid, p.Name)
	}
	if rt == nil {
		return resolvedProxyChain{}, fmt.Errorf("%w: upstream runtime unavailable", errProxyChainInvalid)
	}
	endpoint, slot, err := rt.SelectEndpoint(*p.UpstreamGroupID, p.ID)
	if err != nil {
		return resolvedProxyChain{}, fmt.Errorf("%w: %v", errProxyChainInvalid, err)
	}
	g := rt.group(*p.UpstreamGroupID)
	return resolvedProxyChain{Upstream: &endpoint, Group: &g, Slot: &slot}, nil
}

func activeUpstreamRuntime() *ProxyUpstreamRuntime {
	if s := activeProxyChainService.Load(); s != nil {
		return s.runtime
	}
	return nil
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
	chain, err := resolveProxyChain(p, activeUpstreamRuntime())
	if err == nil && !chain.needsXray(p) {
		return p.directURL(), true
	}
	if err == nil {
		var u string
		if u, err = proxychain.Default().URL(chain.hops(p)); err == nil {
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

// DescribeProxyChain 返回 p 当前的前置端点（不含 p 自身）与解析错误，供列表展示。
func DescribeProxyChain(p *Proxy) ([]ProxyChainHop, string) {
	chain, err := resolveProxyChain(p, activeUpstreamRuntime())
	if err != nil {
		return nil, err.Error()
	}
	if chain.Upstream == nil {
		return []ProxyChainHop{}, ""
	}
	name := chain.Group.Name + "/" + chain.Slot.Name + "/" + chain.Upstream.Name
	return []ProxyChainHop{{ID: chain.Upstream.ID, Name: name, Protocol: chain.Upstream.Protocol}}, ""
}

// ProxyChainService 负责刷新代理快照、管理前置端点与前置组、驱动前置组运行时。
type ProxyChainService struct {
	proxyRepo    ProxyRepository
	upstreamRepo ProxyUpstreamRepository
	runtime      *ProxyUpstreamRuntime
	mu           sync.Mutex
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

func NewProxyChainService(proxyRepo ProxyRepository, upstreamRepo ProxyUpstreamRepository) *ProxyChainService {
	s := &ProxyChainService{proxyRepo: proxyRepo, upstreamRepo: upstreamRepo, runtime: NewProxyUpstreamRuntime(), stopCh: make(chan struct{})}
	s.runtime.onChange = s.onRuntimeChange
	return s
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

// Start 同步加载一次快照，后台立即探测一轮端点，然后周期刷新配置、按各组间隔探测端点。
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
		// 启动首轮立即探测，不等第一个 tick；/ready 以这一轮为准。
		ctx, cancel := context.WithTimeout(context.Background(), 2*proxyUpstreamProbeTimeout)
		go func() {
			select {
			case <-s.stopCh:
				cancel()
			case <-ctx.Done():
			}
		}()
		s.runtime.ProbeAll(ctx)
		cancel()

		refresh := time.NewTicker(proxyChainRefreshInterval)
		defer refresh.Stop()
		probe := time.NewTicker(proxyUpstreamTick)
		defer probe.Stop()
		for {
			select {
			case <-refresh.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := s.Refresh(ctx); err != nil {
					slog.Warn("proxy_chain_refresh_failed", "error", err)
				}
				cancel()
			case <-probe.C:
				ctx, cancel := context.WithTimeout(context.Background(), 2*proxyUpstreamProbeTimeout)
				s.runtime.ProbeDue(ctx)
				cancel()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// ProxyUpstreamStartupReadiness 报告本实例前置组启动首轮探测是否完成（/ready 用）。
// 链式代理服务未启动时视为就绪。
func ProxyUpstreamStartupReadiness() (ready, loaded bool, pending []UpstreamPendingSlot) {
	rt := activeUpstreamRuntime()
	if rt == nil {
		return true, true, nil
	}
	return rt.StartupReadiness()
}

func (s *ProxyChainService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	activeProxyChainService.CompareAndSwap(s, nil)
}

// onRuntimeChange 在槽位切换后重建快照：旧链 URL 缓存随快照失效，旧链由 Retain 回收。
func (s *ProxyChainService) onRuntimeChange() {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := currentProxyChainSnapshot()
	if !snap.loaded {
		return
	}
	proxyChainState.Store(&proxyChainSnapshot{byID: snap.byID, loaded: true})
	proxychain.Default().Retain(activeChainHops(snap.byID, s.runtime))
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
	UpstreamGroupID *int64
	Options         map[string]string
}

// ProxyChainUpdateInput 是更新代理时的链式代理字段（嵌入 UpdateProxyInput）。
// 空 UpstreamMode 与 nil Options 表示保持不变；ClearUpstreamGroupID 显式清除前置组。
type ProxyChainUpdateInput struct {
	UpstreamMode         string
	UpstreamGroupID      *int64
	ClearUpstreamGroupID bool
	Options              map[string]string
	OptionsSet           bool
}

func applyProxyChainCreate(ctx context.Context, p *Proxy, in *ProxyChainInput) error {
	p.UpstreamMode = in.UpstreamMode
	p.UpstreamGroupID = in.UpstreamGroupID
	p.Options = in.Options
	return validateProxyChainConfig(ctx, p)
}

func applyProxyChainUpdate(ctx context.Context, p *Proxy, in *ProxyChainUpdateInput) error {
	if in.UpstreamMode != "" {
		p.UpstreamMode = in.UpstreamMode
	}
	if in.UpstreamGroupID != nil || in.ClearUpstreamGroupID {
		p.UpstreamGroupID = in.UpstreamGroupID
	}
	if in.OptionsSet {
		p.Options = in.Options
	}
	return validateProxyChainConfig(ctx, p)
}

// validateProxyChainConfig 在创建/更新代理前校验协议参数与前置组（admin_proxy 中的挂钩）。
func validateProxyChainConfig(ctx context.Context, p *Proxy) error {
	p.UpstreamMode = NormalizeUpstreamMode(p.UpstreamMode)
	if p.UpstreamMode == UpstreamModeDirect {
		if p.UpstreamGroupID != nil {
			return infraerrors.BadRequest("PROXY_UPSTREAM_MODE_MISMATCH", "upstream_group_id requires upstream_mode=group")
		}
	} else if p.UpstreamGroupID == nil {
		return infraerrors.BadRequest("PROXY_UPSTREAM_REQUIRED", "upstream group required when upstream_mode=group")
	}
	p.Options = proxychain.NormalizeOptions(p.Protocol, p.Options)
	// 基础字段（协议白名单、host、port）由 handler binding 校验；这里只校验需要 Xray 承载的协议参数。
	if proxychain.IsXrayOnlyProtocol(p.Protocol) {
		p.Username = ""
		if err := p.chainHop().Validate(); err != nil {
			return infraerrors.BadRequest("PROXY_CONFIG_INVALID", err.Error())
		}
	}
	if p.UpstreamGroupID == nil {
		return nil
	}
	s := activeProxyChainService.Load()
	if s == nil || s.upstreamRepo == nil {
		return nil
	}
	if _, err := s.upstreamRepo.GetGroup(ctx, *p.UpstreamGroupID); err != nil {
		if errors.Is(err, ErrProxyUpstreamGroupNotFound) {
			return infraerrors.BadRequest("PROXY_UPSTREAM_GROUP_INVALID", "upstream group not found")
		}
		return err
	}
	return nil
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

// Refresh 重新加载全部代理、前置端点与前置组，并注销不再被任何代理使用的链。
func (s *ProxyChainService) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID, err := loadProxyMap(ctx, s.proxyRepo)
	if err != nil {
		return err
	}
	if s.upstreamRepo != nil {
		groups, err := s.upstreamRepo.ListGroups(ctx)
		if err != nil {
			return err
		}
		endpoints, err := s.upstreamRepo.ListEndpoints(ctx)
		if err != nil {
			return err
		}
		s.runtime.Replace(groups, endpoints)
	} else {
		s.runtime.Replace(nil, nil)
	}
	proxyChainState.Store(&proxyChainSnapshot{byID: byID, loaded: true})
	proxychain.Default().Retain(activeChainHops(byID, s.runtime))
	return nil
}

// activeChainHops 收集当前所有需要 Xray 的链，供 Manager 回收过期链。
func activeChainHops(byID map[int64]Proxy, rt *ProxyUpstreamRuntime) [][]proxychain.Hop {
	var out [][]proxychain.Hop
	for id := range byID {
		p := byID[id]
		chain, err := resolveProxyChain(&p, rt)
		if err != nil || !chain.needsXray(&p) {
			continue
		}
		out = append(out, chain.hops(&p))
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

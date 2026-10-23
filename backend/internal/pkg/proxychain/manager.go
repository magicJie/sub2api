package proxychain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/policy"
	"github.com/xtls/xray-core/app/proxyman"
	xlog "github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
)

// maxChains 限制同时注册在 Xray 实例中的链数量，超出后淘汰最久未用的链。
const maxChains = 512

// Manager 持有进程内唯一的 Xray 实例与本地 SOCKS5 入口。
//
// Xray 的 InitSystemDialer 把 outbound manager 存在包级全局变量里（dialerProxy 依赖它），
// 因此整个进程只能有一个 Xray 实例：所有链都以带前缀 tag 的 outbound 组注册到这个实例上，
// 本地 SOCKS5 入口按用户名把连接分派到对应链的落地 outbound。
// 进程内不应再创建其他 Xray 实例（会覆盖全局 outbound manager，使 dialerProxy 查不到 tag 而失败）。
type Manager struct {
	mu       sync.Mutex
	instance *core.Instance
	listener net.Listener
	secret   []byte
	chains   map[string]*chainEntry // username -> chain
	seq      int
	closed   bool
	gen      atomic.Uint64 // 每次注销链递增，上层据此让缓存的 URL 失效
}

type chainEntry struct {
	user     string
	pass     string
	prefix   string
	tags     []string
	exitTag  string
	lastUsed time.Time
	conns    map[net.Conn]struct{} // 正在经该链转发的本地入口连接，注销链时一并关闭
}

var (
	defaultManager     *Manager
	defaultManagerOnce sync.Once
)

// Default 返回进程级 Manager（惰性启动 Xray 与本地入口）。
func Default() *Manager {
	defaultManagerOnce.Do(func() {
		defaultManager = &Manager{chains: make(map[string]*chainEntry)}
	})
	return defaultManager
}

// URL 返回经该链出站的本地 socks5h 代理 URL；同一条链（内容相同）始终返回相同 URL，
// 便于上层按代理 URL 复用连接池。
func (m *Manager) URL(hops []Hop) (string, error) {
	if len(hops) == 0 {
		return "", fmt.Errorf("empty proxy chain")
	}
	if len(hops) > MaxHops {
		return "", fmt.Errorf("proxy chain exceeds %d hops", MaxHops)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("proxy chain manager closed")
	}
	if err := m.startLocked(); err != nil {
		return "", err
	}

	user, pass := m.credentialsLocked(hops)
	entry, ok := m.chains[user]
	if !ok {
		var err error
		entry, err = m.registerLocked(user, pass, hops)
		if err != nil {
			return "", err
		}
	}
	entry.lastUsed = time.Now()

	u := &url.URL{
		Scheme: "socks5h",
		User:   url.UserPassword(entry.user, entry.pass),
		Host:   m.listener.Addr().String(),
	}
	return u.String(), nil
}

// credentialsLocked 由链内容派生本地入口凭据：用户名是内容摘要，密码是带进程随机密钥的摘要。
func (m *Manager) credentialsLocked(hops []Hop) (string, string) {
	h := sha256.New()
	for _, hop := range hops {
		h.Write([]byte(hop.key()))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	mac := sha256.New()
	mac.Write(m.secret)
	mac.Write(sum)
	return "c" + hex.EncodeToString(sum[:12]), hex.EncodeToString(mac.Sum(nil)[:24])
}

func (m *Manager) startLocked() error {
	if m.instance != nil {
		return nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("generate proxy chain secret: %w", err)
	}
	installLogHandler()
	cfg := &core.Config{App: []*serial.TypedMessage{
		serial.ToTypedMessage(&dispatcher.Config{}),
		serial.ToTypedMessage(&proxyman.OutboundConfig{}),
		serial.ToTypedMessage(chainPolicy()),
	}}
	instance, err := core.New(cfg)
	if err != nil {
		return fmt.Errorf("create xray instance: %w", err)
	}
	if err := instance.Start(); err != nil {
		return fmt.Errorf("start xray instance: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = instance.Close()
		return fmt.Errorf("listen proxy chain entry: %w", err)
	}
	m.secret = secret
	m.instance = instance
	m.listener = ln
	go m.serve(ln)
	slog.Info("proxy_chain_started", "listen", ln.Addr().String())
	return nil
}

func (m *Manager) registerLocked(user, pass string, hops []Hop) (*chainEntry, error) {
	if len(m.chains) >= maxChains {
		m.evictOldestLocked()
	}
	m.seq++
	prefix := "c" + strconv.Itoa(m.seq)
	configs, err := buildChainOutbounds(prefix, hops)
	if err != nil {
		return nil, err
	}
	entry := &chainEntry{user: user, pass: pass, prefix: prefix, conns: make(map[net.Conn]struct{})}
	for _, c := range configs {
		if err := core.AddOutboundHandler(m.instance, c); err != nil {
			m.removeTagsLocked(entry.tags)
			return nil, fmt.Errorf("register proxy chain: %w", err)
		}
		entry.tags = append(entry.tags, c.Tag)
	}
	entry.exitTag = entry.tags[len(entry.tags)-1]
	m.chains[user] = entry
	return entry, nil
}

func (m *Manager) evictOldestLocked() {
	var oldest *chainEntry
	for _, e := range m.chains {
		if oldest == nil || e.lastUsed.Before(oldest.lastUsed) {
			oldest = e
		}
	}
	if oldest == nil {
		return
	}
	m.unregisterLocked(oldest)
}

// Generation 返回链注销代数；值变化说明之前返回的某些 URL 可能已失效。
func (m *Manager) Generation() uint64 { return m.gen.Load() }

func (m *Manager) unregisterLocked(e *chainEntry) {
	m.gen.Add(1)
	delete(m.chains, e.user)
	for c := range e.conns {
		_ = c.Close()
	}
	m.removeTagsLocked(e.tags)
}

// Retain 注销不在 active 中的链（链内容变化后的旧链），并断开仍经旧链转发的连接，
// 确保更换前置代理后不会有流量继续走旧链路。
func (m *Manager) Retain(active [][]Hop) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.instance == nil {
		return
	}
	keep := make(map[string]struct{}, len(active))
	for _, hops := range active {
		user, _ := m.credentialsLocked(hops)
		keep[user] = struct{}{}
	}
	for user, e := range m.chains {
		if _, ok := keep[user]; !ok {
			m.unregisterLocked(e)
		}
	}
}

func (m *Manager) trackConn(user string, c net.Conn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.chains[user]
	if !ok {
		return false
	}
	e.conns[c] = struct{}{}
	return true
}

func (m *Manager) untrackConn(user string, c net.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.chains[user]; ok {
		delete(e.conns, c)
	}
}

func (m *Manager) removeTagsLocked(tags []string) {
	om, ok := m.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	if !ok {
		return
	}
	ctx := context.Background()
	for _, tag := range tags {
		if h := om.GetHandler(tag); h != nil {
			_ = om.RemoveHandler(ctx, tag)
			_ = h.Close()
		}
	}
}

func (m *Manager) lookup(user, pass string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.chains[user]
	if !ok || !constantTimeEqual(e.pass, pass) || e.exitTag == "" {
		return "", false
	}
	e.lastUsed = time.Now()
	return e.exitTag, true
}

// dial 经指定链的落地 outbound 建立到目标的 TCP 连接。
func (m *Manager) dial(ctx context.Context, exitTag string, host string, port uint16) (net.Conn, error) {
	m.mu.Lock()
	instance := m.instance
	m.mu.Unlock()
	if instance == nil {
		return nil, fmt.Errorf("proxy chain not started")
	}
	var addr xnet.Address
	if ip := net.ParseIP(host); ip != nil {
		addr = xnet.IPAddress(ip)
	} else {
		addr = xnet.DomainAddress(host)
	}
	ctx = session.SetForcedOutboundTagToContext(ctx, exitTag)
	return core.Dial(ctx, instance, xnet.TCPDestination(addr, xnet.Port(port)))
}

// Close 停止本地入口与 Xray 实例（仅用于测试与进程退出）。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.listener != nil {
		_ = m.listener.Close()
	}
	if m.instance != nil {
		return m.instance.Close()
	}
	return nil
}

// ListenAddr 返回本地入口地址（未启动时为空）。
func (m *Manager) ListenAddr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener == nil {
		return ""
	}
	return m.listener.Addr().String()
}

// chainPolicy 放宽 Xray 默认超时：默认 connIdle=300s 会掐断长时间无数据的 SSE/WS，
// uplinkOnly/downlinkOnly=1s 会在单向关闭后过早断开另一方向。
func chainPolicy() *policy.Config {
	return &policy.Config{Level: map[uint32]*policy.Policy{0: {
		Timeout: &policy.Policy_Timeout{
			Handshake:      &policy.Second{Value: 10},
			ConnectionIdle: &policy.Second{Value: 3600},
			UplinkOnly:     &policy.Second{Value: 30},
			DownlinkOnly:   &policy.Second{Value: 30},
		},
	}}}
}

var logHandlerOnce sync.Once

// installLogHandler 把 Xray 日志转到 slog。Xray 每次连接失败都会打 Warning/Error，
// 按原级别输出会刷屏，统一降为 Debug；真正的错误由调用方从拨号结果里拿到。
func installLogHandler() {
	logHandlerOnce.Do(func() {
		xlog.RegisterHandler(slogHandler{})
	})
}

type slogHandler struct{}

func (slogHandler) Handle(msg xlog.Message) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	slog.Debug("xray", "msg", msg.String())
}

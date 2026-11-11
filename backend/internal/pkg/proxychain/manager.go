package proxychain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

// maxChains bounds the number of cached chains; the least recently used is evicted.
const maxChains = 512

// Manager owns reusable mihomo outbound adapters and an authenticated loopback SOCKS5 entry.
// Adapters are wired directly to their predecessor, without global tunnel or DNS services.
type Manager struct {
	mu       sync.Mutex
	listener net.Listener
	secret   []byte
	chains   map[string]*chainEntry
	closed   bool
	gen      atomic.Uint64
}

type chainEntry struct {
	user     string
	pass     string
	adapters []C.ProxyAdapter
	lastUsed time.Time
	conns    map[net.Conn]struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	hops     []Hop
}

var (
	defaultManager     *Manager
	defaultManagerOnce sync.Once
)

// Default returns the lazily started process-wide manager.
func Default() *Manager {
	defaultManagerOnce.Do(func() { defaultManager = &Manager{} })
	return defaultManager
}

// URL returns a stable authenticated socks5h URL for the same chain content.
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
	return (&url.URL{Scheme: "socks5h", User: url.UserPassword(entry.user, entry.pass), Host: m.listener.Addr().String()}).String(), nil
}

// credentialsLocked derives local credentials without exposing upstream identities.
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
	if m.listener != nil {
		return nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("generate proxy chain secret: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen proxy chain entry: %w", err)
	}
	m.secret = secret
	m.listener = ln
	if m.chains == nil {
		m.chains = make(map[string]*chainEntry)
	}
	go m.serve(ln)
	slog.Info("proxy_chain_started", "listen", ln.Addr().String())
	return nil
}

func (m *Manager) registerLocked(user, pass string, hops []Hop) (*chainEntry, error) {
	adapters, err := buildChainAdapters(hops)
	if err != nil {
		return nil, err
	}
	if len(m.chains) >= maxChains {
		m.evictOldestLocked()
	}
	ctx, cancel := context.WithCancel(context.Background())
	entry := &chainEntry{user: user, pass: pass, adapters: adapters, conns: make(map[net.Conn]struct{}), ctx: ctx, cancel: cancel}
	// Retain immutable copies for redaction after the caller changes its configuration.
	for _, h := range hops {
		o := make(map[string]string, len(h.Options))
		for k, v := range h.Options {
			o[k] = v
		}
		h.Options = o
		entry.hops = append(entry.hops, h)
	}
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
	if oldest != nil {
		m.unregisterLocked(oldest)
	}
}

// Generation changes whenever a previously returned chain URL is unregistered.
func (m *Manager) Generation() uint64 { return m.gen.Load() }

func (m *Manager) unregisterLocked(e *chainEntry) {
	m.gen.Add(1)
	delete(m.chains, e.user)
	e.cancel()
	for c := range e.conns {
		_ = c.Close()
	}
	for i := len(e.adapters) - 1; i >= 0; i-- {
		_ = e.adapters[i].Close()
	}
}

// Retain closes stale chains, their active streams, pooled sessions and pending dials.
func (m *Manager) Retain(active [][]Hop) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener == nil {
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

func (m *Manager) lookup(user, pass string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.chains[user]
	if !ok || !constantTimeEqual(e.pass, pass) {
		return "", false
	}
	e.lastUsed = time.Now()
	return user, true
}

func (m *Manager) dial(ctx context.Context, user, host string, port uint16) (net.Conn, error) {
	m.mu.Lock()
	e := m.chains[user]
	m.mu.Unlock()
	if e == nil {
		return nil, fmt.Errorf("proxy chain unavailable")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	// Bound setup only. The established stream has no idle or overall timeout.
	timer := time.AfterFunc(handshakeTimeout, cancel)
	metadata := &C.Metadata{NetWork: C.TCP, Host: host, DstPort: port}
	if ip, err := netip.ParseAddr(host); err == nil {
		metadata.DstIP = ip
		metadata.Host = ""
	}
	c, err := e.adapters[len(e.adapters)-1].DialContext(ctx, metadata)
	timer.Stop()
	if err != nil {
		stop()
		cancel()
		for _, h := range e.hops {
			err = redactError(err, h)
		}
		return nil, err
	}
	if ctx.Err() != nil {
		_ = c.Close()
		stop()
		cancel()
		return nil, fmt.Errorf("proxy chain dial canceled")
	}
	up := &chainConn{Conn: c, cancel: cancel, stop: stop}
	m.mu.Lock()
	if m.chains[user] != e {
		m.mu.Unlock()
		_ = up.Close()
		return nil, fmt.Errorf("proxy chain unavailable")
	}
	e.conns[up] = struct{}{}
	m.mu.Unlock()
	return up, nil
}

type chainConn struct {
	net.Conn
	cancel context.CancelFunc
	stop   func() bool
}

func (c *chainConn) Close() error      { c.stop(); c.cancel(); return c.Conn.Close() }
func (c *chainConn) CloseWrite() error { return closeWrite(c.Conn) }

// Close stops the entry and releases every chain and its connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.listener != nil {
		_ = m.listener.Close()
	}
	for _, e := range m.chains {
		m.unregisterLocked(e)
	}
	return nil
}

// ListenAddr returns the local entry address, or an empty string before startup.
func (m *Manager) ListenAddr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener == nil {
		return ""
	}
	return m.listener.Addr().String()
}

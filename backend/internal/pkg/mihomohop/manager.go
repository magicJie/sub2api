// Package mihomohop embeds the process-wide mihomo kernel for fail-closed first-hop groups (fork).
package mihomohop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/hopgroup"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
	_ "github.com/metacubex/mihomo/hub/executor" // config's linkname dependency
	"github.com/metacubex/mihomo/listener/inner"
	mlog "github.com/metacubex/mihomo/log"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http/httpproxy"
	"gopkg.in/yaml.v3"
)

type groupEntry struct {
	mu            sync.Mutex
	proxy         C.Proxy
	testURL       string
	bridge        *proxychain.SOCKSBridge
	providerNames []string
}
type providerState struct {
	mu        sync.Mutex
	updatedAt time.Time
	lastError string
}

type Manager struct {
	mu        sync.Mutex
	digest    [32]byte
	loaded    bool
	directory string
	groups    map[int64]*groupEntry
	providers map[string]P.ProxyProvider
	proxies   map[string]C.Proxy
	states    map[string]*providerState
	cancel    context.CancelFunc
	initWG    sync.WaitGroup
}

var defaultManager = &Manager{}
var providerEvents sync.Map // generated provider name -> *providerState; never contains URLs
func Default() *Manager     { return defaultManager }

// Mihomo's init changes the global logrus sink. Never print its payloads: provider
// errors can contain subscription tokens or node credentials, including in Debug.
func init() {
	mlog.SetLevel(mlog.SILENT)
	logrus.SetOutput(safeLogSink{})
	sub := mlog.Subscribe()
	go func() {
		for event := range sub {
			recordProviderEvent(event.Payload)
			slog.Debug("mihomo_event", "level", event.Type())
		}
	}()
}

type safeLogSink struct{}

func (safeLogSink) Write(payload []byte) (int, error) {
	slog.Debug("mihomo_log_event")
	return len(payload), nil
}

// The fetcher's UpdatedAt accessor is not synchronized with automatic updates.
// Record successful provider events instead of racing that library field.
func recordProviderEvent(payload string) {
	body, ok := strings.CutPrefix(payload, "[Provider] ")
	if !ok {
		return
	}
	name, detail, _ := strings.Cut(body, " ")
	name = strings.TrimSuffix(name, "'s")
	value, ok := providerEvents.Load(name)
	if !ok {
		return
	}
	state := value.(*providerState)
	state.mu.Lock()
	defer state.mu.Unlock()
	switch {
	case strings.Contains(detail, "pull error:"):
		state.lastError = "Subscription update failed"
	case strings.Contains(detail, "content update"), strings.Contains(detail, "content doesn't change"):
		state.updatedAt = time.Now()
		state.lastError = ""
	}
}

// Replace rebuilds the one global configuration; identical snapshots keep their
// bridges, while a cutover closes every old bridge/connection before publishing.
func (m *Manager) Replace(ctx context.Context, groups []hopgroup.Group) error {
	groups = append([]hopgroup.Group(nil), groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	raw, err := json.Marshal(groups)
	if err != nil {
		return errors.New("invalid first-hop group configuration")
	}
	digest := sha256.Sum256(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded && digest == m.digest {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.closeLocked()
	if len(groups) == 0 {
		m.digest = digest
		m.loaded = true
		return nil
	}
	if m.directory == "" {
		m.directory, err = os.MkdirTemp("", "sub2api-mihomo-")
		if err != nil {
			return errors.New("cannot create private mihomo cache directory")
		}
		C.SetHomeDir(m.directory)
	}
	mapping, providerNames, err := buildConfig(groups)
	if err != nil {
		return err
	}
	encoded, err := yaml.Marshal(mapping)
	if err != nil {
		return errors.New("cannot encode first-hop configuration")
	}
	cfg, err := config.Parse(encoded)
	if err != nil {
		return errors.New("mihomo rejected first-hop configuration")
	}
	m.providers = cfg.Providers
	m.proxies = cfg.Proxies
	m.states = make(map[string]*providerState, len(cfg.Providers))
	for name := range cfg.Providers {
		m.states[name] = &providerState{}
	}
	tunnel.UpdateProxies(cfg.Proxies, cfg.Providers)
	inner.New(tunnel.Tunnel)
	// 作为库使用时 mihomo 不读 /etc/hosts；容器里的抓取代理常是 host.docker.internal 这类只在 hosts 里的名字。
	resolver.UseSystemHosts = true
	tunnel.OnRunning()
	m.groups = make(map[int64]*groupEntry, len(groups))
	for _, group := range groups {
		entry := &groupEntry{proxy: cfg.Proxies[groupName(group.ID)], testURL: group.TestURL, providerNames: providerNames[group.ID]}
		if entry.proxy == nil {
			m.closeLocked()
			return errors.New("first-hop group missing from runtime")
		}
		entry.bridge, err = proxychain.NewSOCKSBridge(entry.dial)
		if err != nil {
			m.closeLocked()
			return errors.New("cannot bind first-hop SOCKS bridge")
		}
		m.groups[group.ID] = entry
	}
	lifecycle, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	for name, pd := range cfg.Providers {
		state := m.states[name]
		providerEvents.Store(name, state)
		m.initWG.Add(1)
		go func(pd P.ProxyProvider, state *providerState) {
			defer m.initWG.Done()
			if lifecycle.Err() != nil {
				return
			}
			err := pd.Initial()
			state.mu.Lock()
			if err != nil {
				state.lastError = "Subscription initialization failed"
			} else {
				state.updatedAt = time.Now()
				if cached, ok := pd.(interface{ Vehicle() P.Vehicle }); ok {
					if info, err := os.Stat(cached.Vehicle().Path()); err == nil {
						state.updatedAt = info.ModTime()
					}
				}
			}
			state.mu.Unlock()
			if lifecycle.Err() != nil {
				if closer, ok := pd.(io.Closer); ok {
					_ = closer.Close()
				}
				return
			}
			pd.HealthCheck()
		}(pd, state)
	}
	m.digest = digest
	m.loaded = true
	return nil
}

func (m *Manager) Endpoint(id int64) (proxychain.Hop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	group := m.groups[id]
	if group == nil {
		return proxychain.Hop{}, errors.New("first-hop group unavailable")
	}
	return group.bridge.Endpoint(), nil
}

func (m *Manager) Status(id int64) (hopgroup.GroupStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.groups[id]
	if entry == nil {
		return hopgroup.GroupStatus{}, errors.New("first-hop group unavailable")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	result := hopgroup.GroupStatus{Nodes: []hopgroup.NodeStatus{}, Providers: []hopgroup.ProviderStatus{}}
	group, ok := entry.proxy.Adapter().(interface{ Proxies() []C.Proxy })
	if !ok {
		return hopgroup.GroupStatus{}, errors.New("invalid first-hop group adapter")
	}
	for _, node := range group.Proxies() {
		if !transportProxy(node) {
			continue
		}
		result.Nodes = append(result.Nodes, hopgroup.NodeStatus{Name: node.Name(), Alive: node.AliveForTestUrl(entry.testURL), DelayMS: node.LastDelayForTestUrl(entry.testURL)})
	}
	selected := entry.proxy.Unwrap(&C.Metadata{NetWork: C.TCP}, false)
	if transportProxy(selected) && selected.AliveForTestUrl(entry.testURL) {
		result.Selected = selected.Name()
	}
	for _, name := range entry.providerNames {
		pd := m.providers[name]
		status := hopgroup.ProviderStatus{Name: name, NodeCount: pd.Count()}
		if state := m.states[name]; state != nil {
			state.mu.Lock()
			status.LastError = state.lastError
			status.UpdatedAt = state.updatedAt
			state.mu.Unlock()
		}
		result.Providers = append(result.Providers, status)
	}
	return result, nil
}

func (entry *groupEntry) dial(ctx context.Context, host string, port uint16) (net.Conn, error) {
	metadata := &C.Metadata{NetWork: C.TCP, DstPort: port}
	if ip, err := netipAddress(host); err == nil {
		metadata.DstIP = ip
	} else {
		metadata.Host = host
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	selected := entry.proxy.Unwrap(metadata, false)
	if !transportProxy(selected) || !selected.AliveForTestUrl(entry.testURL) {
		return nil, errors.New("no healthy first-hop group member")
	}
	// Only the group chooses nodes; no direct dial path exists here.
	conn, err := entry.proxy.DialContext(ctx, metadata)
	if err != nil {
		return nil, errors.New("first-hop group dial failed")
	}
	return conn, nil
}

func transportProxy(proxy C.Proxy) bool { return proxy != nil && proxy.Type() >= C.Shadowsocks }
func groupName(id int64) string         { return fmt.Sprintf("hop-group-%d", id) }

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
	if m.directory != "" {
		_ = os.RemoveAll(m.directory)
		m.directory = ""
	}
	return nil
}
func (m *Manager) closeLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	for _, entry := range m.groups {
		_ = entry.bridge.Close()
	}
	for _, pd := range m.providers {
		if closer, ok := pd.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	m.initWG.Wait()
	for _, pd := range m.providers {
		if pd.VehicleType() != P.Compatible {
			for _, node := range pd.Proxies() {
				_ = node.Close()
			}
		}
	}
	for _, proxy := range m.proxies {
		_ = proxy.Close()
	}
	for name := range m.states {
		providerEvents.Delete(name)
	}
	m.groups = nil
	m.providers = nil
	m.proxies = nil
	m.states = nil
	m.loaded = false
	tunnel.UpdateProxies(map[string]C.Proxy{}, map[string]P.ProxyProvider{})
}

// Mihomo's resource.HTTPVehicle constructs a Transport without ProxyFromEnvironment.
// Explicit special-proxy outbounds preserve HTTP(S)_PROXY and NO_PROXY semantics.
func buildConfig(groups []hopgroup.Group) (map[string]any, map[int64][]string, error) {
	proxies := []map[string]any{}
	providers := map[string]any{}
	proxyGroups := []map[string]any{}
	providerNames := make(map[int64][]string, len(groups))
	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	for _, group := range groups {
		if group.ID <= 0 || (group.Strategy != "fallback" && group.Strategy != "url-test") || group.IntervalSeconds <= 0 || group.SubscriptionUpdateInterval <= 0 || group.ToleranceMS < 0 {
			return nil, nil, errors.New("invalid first-hop group configuration")
		}
		members := []string{}
		for index, hop := range group.Members {
			name := fmt.Sprintf("%s-manual-%d", groupName(group.ID), index+1)
			mapping, err := manualProxy(name, hop)
			if err != nil {
				return nil, nil, err
			}
			proxies = append(proxies, mapping)
			members = append(members, name)
		}
		use := []string{}
		for index, rawURL := range group.SubscriptionURLs {
			parsed, err := url.Parse(rawURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
				return nil, nil, errors.New("invalid subscription URL")
			}
			name := fmt.Sprintf("%s-subscription-%d", groupName(group.ID), index+1)
			mapping := map[string]any{"type": "http", "url": rawURL, "proxy": "DIRECT", "interval": group.SubscriptionUpdateInterval, "exclude-type": "direct|compatible|pass|pass-rule|rematch|dns", "health-check": map[string]any{"enable": true, "url": group.TestURL, "interval": group.IntervalSeconds, "timeout": 5000, "lazy": false}}
			envProxy, err := proxyFunc(parsed)
			if err != nil {
				return nil, nil, errors.New("invalid subscription environment proxy")
			}
			if envProxy != nil {
				proxyName := name + "-fetch-proxy"
				hop, err := environmentProxy(envProxy)
				if err != nil {
					return nil, nil, err
				}
				proxyMapping, err := manualProxy(proxyName, hop)
				if err != nil {
					return nil, nil, err
				}
				proxies = append(proxies, proxyMapping)
				mapping["proxy"] = proxyName
			}
			providers[name] = mapping
			use = append(use, name)
		}
		providerNames[group.ID] = use
		if len(members) == 0 && len(use) == 0 {
			members = append(members, "REJECT")
		}
		proxyGroups = append(proxyGroups, map[string]any{"name": groupName(group.ID), "type": group.Strategy, "url": group.TestURL, "interval": group.IntervalSeconds, "timeout": 5000, "tolerance": group.ToleranceMS, "lazy": false, "proxies": members, "use": use, "empty-fallback": "REJECT"})
	}
	return map[string]any{"mode": "rule", "log-level": "silent", "proxies": proxies, "proxy-providers": providers, "proxy-groups": proxyGroups, "rules": []string{"MATCH,REJECT"}}, providerNames, nil
}

func environmentProxy(parsed *url.URL) (proxychain.Hop, error) {
	hop := proxychain.Hop{Protocol: parsed.Scheme, Host: parsed.Hostname()}
	switch hop.Protocol {
	case "http":
		hop.Port = 80
	case "https":
		hop.Port = 443
	case "socks5", "socks5h":
		hop.Port = 1080
	default:
		return hop, errors.New("unsupported subscription environment proxy protocol")
	}
	if raw := parsed.Port(); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port <= 0 || port > 65535 {
			return hop, errors.New("invalid subscription environment proxy port")
		}
		hop.Port = port
	}
	if parsed.User != nil {
		hop.Username = parsed.User.Username()
		hop.Password, _ = parsed.User.Password()
	}
	return hop, nil
}

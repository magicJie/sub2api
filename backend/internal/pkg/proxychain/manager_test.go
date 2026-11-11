package proxychain

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sscore "github.com/metacubex/mihomo/transport/shadowsocks/core"
	"github.com/metacubex/mihomo/transport/socks5"
)

// startSSServer serves real authenticated AEAD Shadowsocks on loopback.
func startSSServer(t *testing.T, method, password string) (int, func()) {
	t.Helper()
	// SS "none" carries the destination and payload without a cipher.
	var cipher sscore.Cipher
	if method != "none" {
		var err error
		cipher, err = sscore.PickCipher(method, nil, password)
		if err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer raw.Close()
				c := raw
				if cipher != nil {
					c = cipher.StreamConn(raw)
				}
				addr, err := socks5.ReadAddr0(c)
				if err != nil {
					return
				}
				up, err := net.Dial("tcp", addr.String())
				if err != nil {
					return
				}
				defer up.Close()
				pipe(c, up)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { _ = ln.Close() }
}

// countingForwarder 在 target 前面加一层 TCP 转发并统计连接数，用来证明流量确实经过该跳。
func countingForwarder(t *testing.T, target string) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var count atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			go func() {
				up, err := net.Dial("tcp", target)
				if err != nil {
					_ = c.Close()
					return
				}
				pipe(c, up)
				_ = c.Close()
				_ = up.Close()
			}()
		}
	}()
	return ln.Addr().String(), &count
}

// startConnectProxy 启动一个最小 HTTP CONNECT 代理，要求 Basic 认证。
func startConnectProxy(t *testing.T, user, pass string) (string, *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "connect only", http.StatusMethodNotAllowed)
			return
		}
		u, p, ok := parseProxyAuth(r.Header.Get("Proxy-Authorization"))
		if !ok || u != user || p != pass {
			w.Header().Set("Proxy-Authenticate", `Basic realm="t"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		count.Add(1)
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, _ := w.(http.Hijacker)
		c, _, err := hj.Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		pipe(c, up)
		_ = c.Close()
		_ = up.Close()
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &count
}

func parseProxyAuth(h string) (string, string, bool) {
	req := &http.Request{Header: http.Header{"Authorization": {h}}}
	return req.BasicAuth()
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	_, _ = fmt.Sscan(p, &port)
	return h, port
}

func TestManagerChainSSThenHTTP(t *testing.T) {
	const ssPass = "test-password"
	ssPort, stopSS := startSSServer(t, "aes-256-gcm", ssPass)
	defer stopSS()
	ssAddr, ssCount := countingForwarder(t, fmt.Sprintf("127.0.0.1:%d", ssPort))
	httpAddr, httpCount := startConnectProxy(t, "alice", "secret")

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello-through-chain")
	}))
	defer target.Close()

	ssHost, ssP := splitHostPort(t, ssAddr)
	httpHost, httpP := splitHostPort(t, httpAddr)
	hops := []Hop{
		{Protocol: ProtocolSS, Host: ssHost, Port: ssP, Password: ssPass, Options: map[string]string{OptMethod: "aes-256-gcm"}},
		{Protocol: ProtocolHTTP, Host: httpHost, Port: httpP, Username: "alice", Password: "secret"},
	}
	for _, h := range hops {
		if err := h.Validate(); err != nil {
			t.Fatalf("validate %s: %v", h.Protocol, err)
		}
	}

	m := &Manager{chains: make(map[string]*chainEntry)}
	defer func() { _ = m.Close() }()
	proxyURL, err := m.URL(hops)
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	again, err := m.URL(hops)
	if err != nil || again != proxyURL {
		t.Fatalf("URL not stable: %q vs %q (%v)", proxyURL, again, err)
	}
	if !strings.HasPrefix(proxyURL, "socks5h://") {
		t.Fatalf("unexpected url %q", proxyURL)
	}

	pu, _ := url.Parse(proxyURL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   15 * time.Second,
	}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET through chain: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "hello-through-chain" {
		t.Fatalf("unexpected body %q", body)
	}
	if ssCount.Load() == 0 {
		t.Fatalf("traffic did not pass through ss hop")
	}
	if httpCount.Load() == 0 {
		t.Fatalf("traffic did not pass through http hop")
	}

	// 错误凭据必须被拒绝。
	bad := *pu
	bad.User = url.UserPassword(pu.User.Username(), "wrong")
	badClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&bad)}, Timeout: 5 * time.Second}
	if _, err := badClient.Get(target.URL); err == nil {
		t.Fatalf("expected auth failure with wrong password")
	}
}

func TestManagerEvictsOldest(t *testing.T) {
	m := &Manager{chains: make(map[string]*chainEntry)}
	defer func() { _ = m.Close() }()
	for i := 0; i < maxChains+3; i++ {
		_, err := m.URL([]Hop{{Protocol: ProtocolSOCKS5, Host: "127.0.0.1", Port: 1000 + i}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(m.chains) != maxChains {
		t.Fatalf("chains=%d want %d", len(m.chains), maxChains)
	}
}

func TestSocksRejectsNoAuth(t *testing.T) {
	m := &Manager{chains: make(map[string]*chainEntry)}
	defer func() { _ = m.Close() }()
	if _, err := m.URL([]Hop{{Protocol: ProtocolSOCKS5, Host: "127.0.0.1", Port: 1}}); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", m.ListenAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte{0x05, 0x01, 0x00})
	resp := make([]byte, 2)
	if _, err := io.ReadFull(bufio.NewReader(c), resp); err != nil {
		t.Fatal(err)
	}
	if resp[1] != socksAuthNoAccept {
		t.Fatalf("expected no-acceptable-methods, got %v", resp)
	}
}

func TestManagerRetainClosesStaleChains(t *testing.T) {
	m := &Manager{chains: make(map[string]*chainEntry)}
	defer func() { _ = m.Close() }()
	a := []Hop{{Protocol: ProtocolSOCKS5, Host: "127.0.0.1", Port: 1}}
	b := []Hop{{Protocol: ProtocolSOCKS5, Host: "127.0.0.1", Port: 2}}
	ua, err := m.URL(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.URL(b); err != nil {
		t.Fatal(err)
	}
	m.Retain([][]Hop{b})
	if len(m.chains) != 1 {
		t.Fatalf("chains=%d want 1", len(m.chains))
	}
	// 旧链凭据必须失效。
	pu, _ := url.Parse(ua)
	pass, _ := pu.User.Password()
	if _, ok := m.lookup(pu.User.Username(), pass); ok {
		t.Fatalf("stale chain credentials still accepted")
	}
}

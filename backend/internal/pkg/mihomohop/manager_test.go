//go:build unit

package mihomohop

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/hopgroup"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
	mlog "github.com/metacubex/mihomo/log"
	xproxy "golang.org/x/net/proxy"
)

func localMember(t *testing.T) *proxychain.SOCKSBridge {
	t.Helper()
	bridge, err := proxychain.NewSOCKSBridge(func(ctx context.Context, host string, port uint16) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	return bridge
}
func endpointURL(hop proxychain.Hop) *url.URL {
	return &url.URL{Scheme: "socks5", Host: net.JoinHostPort(hop.Host, fmt.Sprint(hop.Port)), User: url.UserPassword(hop.Username, hop.Password)}
}
func clientFor(hop proxychain.Hop) *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(endpointURL(hop)), DisableKeepAlives: true}, Timeout: 3 * time.Second}
}
func deadMember(t *testing.T) proxychain.Hop {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return proxychain.Hop{Protocol: "socks5", Host: "127.0.0.1", Port: port}
}
func waitStatus(t *testing.T, m *Manager, id int64, check func(hopgroup.GroupStatus) bool) hopgroup.GroupStatus {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		status, err := m.Status(id)
		if err == nil && check(status) {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("first-hop group did not reach expected health state")
	return hopgroup.GroupStatus{}
}
func baseGroup(testURL string, members ...proxychain.Hop) hopgroup.Group {
	return hopgroup.Group{ID: 1, Name: "test", Strategy: "fallback", TestURL: testURL, IntervalSeconds: 1, ToleranceMS: 50, SubscriptionUpdateInterval: 3600, Members: members}
}

func TestFallbackAndXrayLanding(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(204)
			return
		}
		requests.Add(1)
		_, _ = io.WriteString(w, "through-chain")
	}))
	defer target.Close()
	live := localMember(t)
	m := &Manager{}
	defer m.Close()
	group := baseGroup(target.URL+"/health", deadMember(t), live.Endpoint())
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, 1, func(s hopgroup.GroupStatus) bool {
		return len(s.Nodes) == 2 && !s.Nodes[0].Alive && s.Nodes[1].Alive && strings.HasSuffix(s.Selected, "manual-2")
	})
	endpoint, err := m.Endpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	landing := localMember(t)
	chainURL, err := proxychain.Default().URL([]proxychain.Hop{endpoint, landing.Endpoint()})
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := url.Parse(chainURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}, Timeout: 5 * time.Second}
	resp, err := client.Get(target.URL + "/request")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "through-chain" || requests.Load() != 1 {
		t.Fatalf("unexpected landing response %q, requests %d", body, requests.Load())
	}
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	same, _ := m.Endpoint(1)
	if same.Port != endpoint.Port || same.Password != endpoint.Password {
		t.Fatal("unchanged configuration rotated endpoint")
	}
	group.ToleranceMS++
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Endpoint(1)
	if updated.Password == endpoint.Password {
		t.Fatal("changed configuration retained old endpoint credentials")
	}
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("old first-hop endpoint still accepts connections")
	}
}

func TestAllUnavailableNeverDialsDirect(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	m := &Manager{}
	defer m.Close()
	group := baseGroup(target.URL, deadMember(t), deadMember(t))
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, 1, func(s hopgroup.GroupStatus) bool {
		return len(s.Nodes) == 2 && !s.Nodes[0].Alive && !s.Nodes[1].Alive && s.Selected == ""
	})
	endpoint, _ := m.Endpoint(1)
	if response, err := clientFor(endpoint).Get(target.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("all-dead group unexpectedly reached target")
	}
	if requests.Load() != 0 {
		t.Fatalf("target was contacted directly %d times", requests.Load())
	}
	group.Members = nil
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	endpoint, _ = m.Endpoint(1)
	if response, err := clientFor(endpoint).Get(target.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("empty group unexpectedly reached target")
	}
	if requests.Load() != 0 {
		t.Fatal("empty group dialed direct")
	}
}

func TestClashSubscriptionViaEnvironmentProxyAndSafeLogs(t *testing.T) {
	live := localMember(t)
	member := live.Endpoint()
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer health.Close()
	var fetches atomic.Int32
	// The fake hostname must never resolve locally; only this HTTP proxy serves it.
	fetchProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("subscription proxy expected CONNECT, got %s", r.Method)
			w.WriteHeader(400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		request, err := http.ReadRequest(rw.Reader)
		if err != nil {
			return
		}
		defer request.Body.Close()
		fetches.Add(1)
		payload := fmt.Sprintf("proxies:\n - {name: subscribed-socks, type: socks5, server: 127.0.0.1, port: %d, username: %s, password: %s}\n - {name: anytls-node, type: anytls, server: 127.0.0.1, port: 1, password: test-only, sni: example.invalid}\n", member.Port, member.Username, member.Password)
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nContent-Type: application/yaml\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(payload), payload)
		_ = rw.Flush()
	}))
	defer fetchProxy.Close()
	t.Setenv("HTTP_PROXY", fetchProxy.URL)
	t.Setenv("http_proxy", fetchProxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)
	secretURL := "http://subscription.invalid/clash?token=local-unit-secret"
	m := &Manager{}
	defer m.Close()
	group := baseGroup(health.URL)
	group.SubscriptionURLs = []string{secretURL}
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, 1, func(s hopgroup.GroupStatus) bool {
		return len(s.Providers) == 1 && s.Providers[0].NodeCount == 2 && !s.Providers[0].UpdatedAt.IsZero() && len(s.Nodes) == 2 && s.Nodes[0].Alive && s.Selected == "subscribed-socks"
	})
	if fetches.Load() == 0 {
		t.Fatal("subscription did not update through environment proxy")
	}
	mlog.Errorln("subscription fetch failed: %s", secretURL)
	if strings.Contains(logs.String(), secretURL) || strings.Contains(logs.String(), "local-unit-secret") {
		t.Fatal("subscription credential leaked into logs")
	}
	endpoint, _ := m.Endpoint(1)
	resp, err := clientFor(endpoint).Get(health.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
}

func TestURLTestSelectsHealthyNode(t *testing.T) {
	live := localMember(t)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer health.Close()
	group := baseGroup(health.URL, deadMember(t), live.Endpoint())
	group.Strategy = "url-test"
	m := &Manager{}
	defer m.Close()
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, 1, func(s hopgroup.GroupStatus) bool {
		return len(s.Nodes) == 2 && !s.Nodes[0].Alive && s.Nodes[1].Alive && strings.HasSuffix(s.Selected, "manual-2")
	})
	endpoint, _ := m.Endpoint(1)
	resp, err := clientFor(endpoint).Get(health.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
}

func TestConfigurationCutoverClosesExistingConnections(t *testing.T) {
	live := localMember(t)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer health.Close()
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	group := baseGroup(health.URL, live.Endpoint())
	m := &Manager{}
	defer m.Close()
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, 1, func(s hopgroup.GroupStatus) bool {
		return len(s.Nodes) == 1 && s.Nodes[0].Alive && s.Nodes[0].DelayMS != 65535
	})
	endpoint, _ := m.Endpoint(1)
	dialer, err := xproxy.SOCKS5("tcp", net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), &xproxy.Auth{User: endpoint.Username, Password: endpoint.Password}, xproxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("keep")); err != nil {
		t.Fatal(err)
	}
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "keep" {
		t.Fatalf("unchanged config interrupted live connection: %v", err)
	}
	group.Name = "changed"
	if err := m.Replace(context.Background(), []hopgroup.Group{group}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(reply[:]); err == nil {
		t.Fatal("changed group retained old connection")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("old group connection was not closed promptly")
	}
}

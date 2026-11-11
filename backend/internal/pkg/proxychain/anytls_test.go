package proxychain

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/third_party/mihomo/transport/anytls/session"
	"github.com/gorilla/websocket"
	"github.com/metacubex/mihomo/transport/anytls/padding"
	M "github.com/metacubex/sing/common/metadata"
	"golang.org/x/net/proxy"
)

// serveTestTCP owns all accepted sockets so test teardown also closes idle sessions.
func serveTestTCP(t *testing.T, wrap func(net.Listener) net.Listener, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return serveTestListener(t, ln, wrap, handle)
}

func serveTestListener(t *testing.T, ln net.Listener, wrap func(net.Listener) net.Listener, handle func(net.Conn)) string {
	t.Helper()
	if wrap != nil {
		ln = wrap(ln)
	}
	var conns sync.Map
	var workers sync.WaitGroup
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Store(c, struct{}{})
			workers.Add(1)
			go func() { defer workers.Done(); defer conns.Delete(c); defer c.Close(); handle(c) }()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-stopped
		conns.Range(func(c, _ any) bool { _ = c.(net.Conn).Close(); return true })
		workers.Wait()
	})
	return ln.Addr().String()
}

func startAnyTLSServer(t *testing.T, password string) (Hop, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return startAnyTLSServerOn(t, password, ln)
}

func startAnyTLSServerOn(t *testing.T, password string, ln net.Listener) (Hop, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	// Chrome's real ClientHello advertises ECDSA P-256, but not Ed25519.
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	if err != nil {
		t.Fatal(err)
	}
	var sessions, active atomic.Int64
	var pad atomic.Pointer[padding.PaddingFactory]
	padding.UpdatePaddingScheme(padding.DefaultPaddingScheme, &pad)
	expected := sha256.Sum256([]byte(password))
	addr := serveTestListener(t, ln, func(ln net.Listener) net.Listener {
		return tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}, func(c net.Conn) {
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		var auth [34]byte
		if _, err := io.ReadFull(c, auth[:]); err != nil {
			return
		}
		if string(auth[:32]) != string(expected[:]) {
			return
		}
		if _, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(auth[32:]))); err != nil {
			return
		}
		_ = c.SetDeadline(time.Time{})
		sessions.Add(1)
		active.Add(1)
		defer active.Add(-1)
		s := session.NewServerSession(c, func(stream *session.Stream) {
			defer stream.Close()
			destination, err := M.SocksaddrSerializer.ReadAddrPort(stream)
			if err != nil {
				return
			}
			up, err := net.DialTimeout("tcp", destination.String(), 5*time.Second)
			if err != nil {
				_ = stream.HandshakeFailure(err)
				return
			}
			defer up.Close()
			if err := stream.HandshakeSuccess(); err != nil {
				return
			}
			pipe(stream, up)
		}, &pad)
		s.Run()
		_ = s.Close()
	})
	host, port := splitHostPort(t, addr)
	return Hop{Protocol: ProtocolAnyTLS, Host: host, Port: port, Password: password, Options: map[string]string{OptSNI: "localhost", OptSkipCertVerify: "true"}}, &sessions, &active
}

func proxyClient(t *testing.T, m *Manager, hops []Hop) *http.Client {
	t.Helper()
	u, err := m.URL(hops)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(parsed), DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}
}

func TestAnyTLSTLSAuthenticationAndChains(t *testing.T) {
	anytls, _, _ := startAnyTLSServer(t, "synthetic-anytls-password")
	second, _, _ := startAnyTLSServer(t, "synthetic-second-password")
	httpAddr, httpCount := startConnectProxy(t, "synthetic-user", "synthetic-http-password")
	host, port := splitHostPort(t, httpAddr)
	httpHop := Hop{Protocol: ProtocolHTTP, Host: host, Port: port, Username: "synthetic-user", Password: "synthetic-http-password"}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "authenticated-origin-response")
	}))
	defer target.Close()
	for _, tc := range []struct {
		name      string
		hops      []Hop
		wantError bool
	}{
		{"single", []Hop{anytls}, false},
		{"anytls-http", []Hop{anytls, httpHop}, false},
		{"http-anytls", []Hop{httpHop, anytls}, false},
		{"anytls-anytls", []Hop{anytls, second}, false},
		{"untrusted-certificate-default", []Hop{{Protocol: ProtocolAnyTLS, Host: anytls.Host, Port: anytls.Port, Password: anytls.Password, Options: map[string]string{OptSNI: "localhost"}}}, true},
		{"untrusted-certificate-explicit-false", []Hop{{Protocol: ProtocolAnyTLS, Host: anytls.Host, Port: anytls.Port, Password: anytls.Password, Options: map[string]string{OptSNI: "localhost", OptSkipCertVerify: "false"}}}, true},
		{"chrome-client-fingerprint", []Hop{{Protocol: ProtocolAnyTLS, Host: anytls.Host, Port: anytls.Port, Password: anytls.Password, Options: map[string]string{OptSNI: "localhost", OptSkipCertVerify: "true", OptFingerprint: "chrome"}}}, false},
		{"none-client-fingerprint", []Hop{{Protocol: ProtocolAnyTLS, Host: anytls.Host, Port: anytls.Port, Password: anytls.Password, Options: map[string]string{OptSNI: "localhost", OptSkipCertVerify: "true", OptFingerprint: "none"}}}, false},
		{"none-client-fingerprint-verifies-certificate", []Hop{{Protocol: ProtocolAnyTLS, Host: anytls.Host, Port: anytls.Port, Password: anytls.Password, Options: map[string]string{OptSNI: "localhost", OptFingerprint: "none"}}}, true},
		{"wrong-password", []Hop{{Protocol: ProtocolAnyTLS, Host: anytls.Host, Port: anytls.Port, Password: "wrong-synthetic-password", Options: anytls.Options}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{}
			defer m.Close()
			resp, err := proxyClient(t, m, tc.hops).Get(target.URL)
			if err != nil {
				if tc.wantError {
					return
				}
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if tc.wantError {
				if err == nil && string(body) == "authenticated-origin-response" {
					t.Fatal("unauthenticated request reached origin")
				}
				return
			}
			if err != nil || string(body) != "authenticated-origin-response" {
				t.Fatalf("body=%q error=%v", body, err)
			}
		})
	}
	if httpCount.Load() != 2 {
		t.Fatalf("HTTP hop traversals=%d, want 2", httpCount.Load())
	}
}

func TestAnyTLSSessionReuseAndRetain(t *testing.T) {
	hop, sessions, active := startAnyTLSServer(t, "synthetic-reuse-password")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "reused-session-response") }))
	defer target.Close()
	m := &Manager{}
	defer m.Close()
	u, err := m.URL([]Hop{hop})
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.URL([]Hop{hop})
	if err != nil || u != again {
		t.Fatal("same chain URL changed")
	}
	parsed, _ := url.Parse(u)
	host, port := splitHostPort(t, strings.TrimPrefix(target.URL, "http://"))
	for range 2 {
		c, err := m.dial(context.Background(), parsed.User.Username(), host, uint16(port))
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, err = fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: origin\r\nConnection: close\r\n\r\n")
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		_ = c.Close()
		m.untrackConn(parsed.User.Username(), c)
		if err != nil || string(body) != "reused-session-response" {
			t.Fatalf("body=%q error=%v", body, err)
		}
	}
	if sessions.Load() != 1 {
		t.Fatalf("TLS sessions=%d, want one reused session", sessions.Load())
	}
	before := m.Generation()
	m.Retain(nil)
	if m.Generation() == before {
		t.Fatal("retention did not invalidate URL generation")
	}
	deadline := time.After(5 * time.Second)
	for active.Load() != 0 {
		select {
		case <-deadline:
			t.Fatal("retention left TLS session open")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := proxyClient(t, m, []Hop{hop}).Get(target.URL); err != nil {
		t.Fatal(err)
	}
}

func TestAnyTLSRetainClosesActiveStream(t *testing.T) {
	hop, _, _ := startAnyTLSServer(t, "synthetic-retain-password")
	ready := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: connected\n\n")
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
	}))
	defer target.Close()
	m := &Manager{}
	defer m.Close()
	resp, err := proxyClient(t, m, []Hop{hop}).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	<-ready
	reader := bufio.NewReader(resp.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: connected\n" {
		t.Fatalf("line=%q error=%v", line, err)
	}
	m.Retain(nil)
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); done <- err }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stale active stream remained open")
	}
}

func TestAnyTLSStreamingAndWebSocket(t *testing.T) {
	hop, _, _ := startAnyTLSServer(t, "synthetic-stream-password")
	upgrader := websocket.Upgrader{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			_, body, err := c.ReadMessage()
			if err != nil || string(body) != "subscribe" {
				return
			}
			_ = c.WriteMessage(websocket.TextMessage, []byte("event:ready"))
			time.Sleep(handshakeTimeout + time.Second)
			_ = c.WriteMessage(websocket.TextMessage, []byte("event:complete"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(handshakeTimeout + time.Second):
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: after-setup-timeout\n\n")
	}))
	defer target.Close()
	m := &Manager{}
	defer m.Close()
	client := proxyClient(t, m, []Hop{hop})
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "data: first\n\ndata: after-setup-timeout\n\n" {
		t.Fatalf("SSE=%q error=%v", body, err)
	}
	u, err := m.URL([]Hop{hop})
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	password, _ := pu.User.Password()
	d, err := proxy.SOCKS5("tcp", pu.Host, &proxy.Auth{User: pu.User.Username(), Password: password}, &net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	wsDialer := websocket.Dialer{NetDial: d.Dial, HandshakeTimeout: 5 * time.Second}
	ws, _, err := wsDialer.Dial("ws"+strings.TrimPrefix(target.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(handshakeTimeout + 3*time.Second))
	if err := ws.WriteMessage(websocket.TextMessage, []byte("subscribe")); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"event:ready", "event:complete"} {
		_, message, err := ws.ReadMessage()
		if err != nil || string(message) != expected {
			t.Fatalf("WS=%q, want %q, error=%v", message, expected, err)
		}
	}
}

func TestManagerHalfCloseKeepsDelayedResponse(t *testing.T) {
	target := serveTestTCP(t, nil, func(c net.Conn) {
		request, err := io.ReadAll(c)
		if err != nil || string(request) != "complete-request" {
			return
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(c, "complete-delayed-response")
	})
	addr, _ := startConnectProxy(t, "halfclose-user", "halfclose-password")
	host, port := splitHostPort(t, addr)
	m := &Manager{}
	defer m.Close()
	u, err := m.URL([]Hop{{Protocol: ProtocolHTTP, Host: host, Port: port, Username: "halfclose-user", Password: "halfclose-password"}})
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	password, _ := pu.User.Password()
	d, err := proxy.SOCKS5("tcp", pu.Host, &proxy.Auth{User: pu.User.Username(), Password: password}, &net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Dial("tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "complete-request")
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(c)
	if err != nil || string(response) != "complete-delayed-response" {
		t.Fatalf("response=%q error=%v", response, err)
	}
}

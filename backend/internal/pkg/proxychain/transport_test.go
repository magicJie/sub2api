package proxychain

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mhhttp "github.com/metacubex/http"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/transport/gun"
	"github.com/metacubex/mihomo/transport/socks5"
	"github.com/metacubex/mihomo/transport/trojan"
	"github.com/metacubex/mihomo/transport/vmess"
	"github.com/metacubex/mihomo/transport/xhttp"
	singvmess "github.com/metacubex/sing-vmess"
	singvless "github.com/metacubex/sing-vmess/vless"
	M "github.com/metacubex/sing/common/metadata"
	N "github.com/metacubex/sing/common/network"
)

type originHandler struct{ errors chan error }

func (h *originHandler) NewConnection(_ context.Context, c net.Conn, metadata M.Metadata) error {
	up, err := net.DialTimeout("tcp", metadata.Destination.String(), 5*time.Second)
	if err != nil {
		return err
	}
	defer up.Close()
	pipe(c, up)
	return nil
}
func (h *originHandler) NewPacketConnection(context.Context, N.PacketConn, M.Metadata) error {
	return C.ErrNotSupport
}
func (h *originHandler) NewError(_ context.Context, err error) {
	select {
	case h.errors <- err:
	default:
	}
}

func startProtocolServer(t *testing.T, protocol, network, mode string) Hop {
	t.Helper()
	handler := &originHandler{errors: make(chan error, 8)}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for {
			select {
			case err := <-handler.errors:
				t.Logf("synthetic protocol server: %v", err)
			default:
				return
			}
		}
	})
	var serve func(net.Conn)
	password := testUUID
	switch protocol {
	case ProtocolVMess:
		s := singvmess.NewService[string](handler)
		if err := s.UpdateUsers([]string{"synthetic-user"}, []string{testUUID}, []int{0}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		serve = func(c net.Conn) {
			if err := s.NewConnection(context.Background(), c, M.Metadata{}); err != nil {
				handler.NewError(context.Background(), err)
			}
		}
	case ProtocolVLESS:
		s := singvless.NewService[string](nil, handler)
		s.UpdateUsers([]string{"synthetic-user"}, []string{testUUID}, []string{""})
		serve = func(c net.Conn) {
			if err := s.NewConnection(context.Background(), c, M.Metadata{}); err != nil {
				handler.NewError(context.Background(), err)
			}
		}
	case ProtocolTrojan:
		password = "synthetic-trojan-password"
		key := trojan.Key(password)
		serve = func(c net.Conn) {
			var header [trojan.KeyLength + 3]byte
			if _, err := io.ReadFull(c, header[:]); err != nil {
				return
			}
			if string(header[:trojan.KeyLength]) != string(key[:]) || header[trojan.KeyLength] != '\r' || header[trojan.KeyLength+1] != '\n' || header[trojan.KeyLength+2] != trojan.CommandTCP {
				return
			}
			destination, err := socks5.ReadAddr0(c)
			if err != nil {
				return
			}
			var ending [2]byte
			if _, err := io.ReadFull(c, ending[:]); err != nil || ending != [2]byte{'\r', '\n'} {
				return
			}
			_ = handler.NewConnection(context.Background(), c, M.Metadata{Destination: M.ParseSocksaddr(destination.String())})
		}
	}
	var conns sync.Map
	handle := func(c net.Conn) { conns.Store(c, struct{}{}); defer conns.Delete(c); defer c.Close(); serve(c) }
	options := map[string]string{OptNetwork: network, OptTLS: "none"}
	var addr string
	if network == "tcp" {
		addr = serveTestTCP(t, nil, serve)
	} else {
		var httpHandler mhhttp.Handler
		switch network {
		case "ws", "httpupgrade":
			options[OptPath] = "/synthetic-websocket"
			options[OptHost] = "transport.example.invalid"
			httpHandler = mhhttp.HandlerFunc(func(w mhhttp.ResponseWriter, r *mhhttp.Request) {
				if r.URL.Path != options[OptPath] || r.Host != options[OptHost] {
					w.WriteHeader(400)
					return
				}
				c, err := vmess.StreamUpgradedWebsocketConn(w, r)
				if err == nil {
					handle(c)
				}
			})
		case "grpc":
			options[OptServiceName] = "synthetic-grpc"
			httpHandler = gun.NewServerHandler(gun.ServerOption{ServiceName: options[OptServiceName], ConnHandler: handle})
		case "xhttp":
			options[OptPath] = "/synthetic-xhttp"
			options[OptHost] = "transport.example.invalid"
			options[OptMode] = mode
			options[OptALPN] = "http/1.1"
			var err error
			httpHandler, err = xhttp.NewServerHandler(xhttp.ServerOption{Config: xhttp.Config{Path: options[OptPath], Host: options[OptHost], Mode: options[OptMode]}, ConnHandler: handle})
			if err != nil {
				t.Fatal(err)
			}
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		protocols := new(mhhttp.Protocols)
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)
		server := &mhhttp.Server{Handler: httpHandler, Protocols: protocols}
		go func() { _ = server.Serve(ln) }()
		t.Cleanup(func() { _ = server.Close(); conns.Range(func(c, _ any) bool { _ = c.(net.Conn).Close(); return true }) })
		addr = ln.Addr().String()
	}
	host, port := splitHostPort(t, addr)
	return Hop{Protocol: protocol, Host: host, Port: port, Password: password, Options: options}
}

// Requests terminate at an HTTP origin after real protocol authentication and
// transport negotiation; the synthetic servers decode their actual wire formats.
func TestProtocolTransportPreservation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resource" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "authenticated-transport-resource")
	}))
	defer target.Close()
	for _, protocol := range []string{ProtocolVMess, ProtocolVLESS, ProtocolTrojan} {
		for _, network := range []string{"tcp", "ws", "httpupgrade", "HTTPUpgrade", "grpc", "xhttp"} {
			t.Run(fmt.Sprintf("%s/%s", protocol, network), func(t *testing.T) {
				hop := startProtocolServer(t, protocol, strings.ToLower(network), "packet-up")
				hop.Options[OptNetwork] = network
				m := &Manager{}
				defer m.Close()
				resp, err := proxyClient(t, m, []Hop{hop}).Get(target.URL + "/resource")
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || string(body) != "authenticated-transport-resource" {
					t.Fatalf("body=%q error=%v", body, err)
				}
			})
		}
	}
}

func TestHTTPProxyEmptyPassword(t *testing.T) {
	addr, _ := startConnectProxy(t, "synthetic-user", "")
	host, port := splitHostPort(t, addr)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "empty-password-authenticated") }))
	defer target.Close()
	m := &Manager{}
	defer m.Close()
	resp, err := proxyClient(t, m, []Hop{{Protocol: ProtocolHTTP, Host: host, Port: port, Username: "synthetic-user"}}).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "empty-password-authenticated" {
		t.Fatalf("body=%q error=%v", body, err)
	}
}

func TestProtocolXHTTPStreamingModes(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "xhttp-streaming-resource") }))
	defer target.Close()
	for _, protocol := range []string{ProtocolVMess, ProtocolVLESS, ProtocolTrojan} {
		for _, mode := range []string{"stream-up", "stream-one"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				hop := startProtocolServer(t, protocol, "xhttp", mode)
				m := &Manager{}
				defer m.Close()
				resp, err := proxyClient(t, m, []Hop{hop}).Get(target.URL)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || string(body) != "xhttp-streaming-resource" {
					t.Fatalf("body=%q error=%v", body, err)
				}
			})
		}
	}
}

func TestShadowsocksCipherAliases(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "shadowsocks-resource") }))
	defer target.Close()
	for _, pair := range [][2]string{
		{"AEAD_AES_128_GCM", "aes-128-gcm"},
		{"aead_aes_256_gcm", "aes-256-gcm"},
		{"chacha20-poly1305", "chacha20-ietf-poly1305"},
		{"aead_chacha20_poly1305", "chacha20-ietf-poly1305"},
		{"xchacha20-poly1305", "xchacha20-ietf-poly1305"},
		{"aead_xchacha20_poly1305", "xchacha20-ietf-poly1305"},
		{"plain", "none"},
	} {
		t.Run(pair[0], func(t *testing.T) {
			port, stop := startSSServer(t, pair[1], "synthetic-shadowsocks-password")
			defer stop()
			m := &Manager{}
			defer m.Close()
			hop := Hop{Protocol: ProtocolSS, Host: "127.0.0.1", Port: port, Password: "synthetic-shadowsocks-password", Options: map[string]string{OptMethod: pair[0]}}
			resp, err := proxyClient(t, m, []Hop{hop}).Get(target.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || string(body) != "shadowsocks-resource" {
				t.Fatalf("body=%q error=%v", body, err)
			}
		})
	}
}

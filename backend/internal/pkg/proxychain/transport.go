package proxychain

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/metacubex/http"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/dialer"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/transport/gun"
	"github.com/metacubex/mihomo/transport/socks5"
	"github.com/metacubex/mihomo/transport/trojan"
	"github.com/metacubex/mihomo/transport/tuic/common"
	"github.com/metacubex/mihomo/transport/vmess"
	"github.com/metacubex/mihomo/transport/xhttp"
	"github.com/metacubex/quic-go"
)

// streamTransport composes the engine's public transport primitives for the
// combinations absent from its outbound option structs (VMess XHTTP and plain Trojan).
type streamTransport struct {
	dialer  C.Dialer
	addr    string
	network string
	ws      vmess.WebsocketConfig
	tls     *vmess.TLSConfig
	gun     *gun.Client
	xhttp   *xhttp.Client
}

func newStreamTransport(h Hop, previous C.Dialer, network, security, sni, fp string, reality outbound.RealityOptions) (*streamTransport, error) {
	if previous == nil {
		previous = dialer.NewDialer()
	}
	t := &streamTransport{dialer: previous, addr: net.JoinHostPort(h.Host, strconv.Itoa(h.Port)), network: network}
	if security != "none" {
		r, err := reality.Parse()
		if err != nil {
			return nil, err
		}
		t.tls = &vmess.TLSConfig{Host: sni, ClientFingerprint: fp, NextProtos: splitCSV(h.Options[OptALPN]), Reality: r}
	}
	switch network {
	case "ws":
		t.ws = vmess.WebsocketConfig{Host: h.Host, Port: strconv.Itoa(h.Port), Path: optOr(h.Options, OptPath, "/"), Headers: http.Header{}, V2rayHttpUpgrade: strings.EqualFold(h.Options[OptNetwork], "httpupgrade")}
		if host := h.Options[OptHost]; host != "" {
			t.ws.Headers.Set("Host", host)
		}
	case "grpc":
		var tlsConfig *vmess.TLSConfig
		if t.tls != nil {
			copy := *t.tls
			copy.NextProtos = []string{"h2"}
			tlsConfig = &copy
		}
		gunConfig := &gun.Config{ServiceName: h.Options[OptServiceName], Host: sni}
		t.gun = gun.NewClient(func() *gun.Transport {
			return gun.NewTransport(func(ctx context.Context, _, _ string) (net.Conn, error) {
				return previous.DialContext(ctx, "tcp", t.addr)
			}, tlsConfig, gunConfig)
		}, 0, 0, 0)
	case "xhttp":
		alpn := splitCSV(h.Options[OptALPN])
		if t.tls == nil {
			alpn = []string{"http/1.1"}
		}
		if security == "reality" {
			alpn = []string{"h2"}
		}
		requestHost := optOr(h.Options, OptHost, sni)
		if ip, err := netip.ParseAddr(requestHost); err == nil && ip.Is6() {
			requestHost = "[" + requestHost + "]"
		}
		cfg := &xhttp.Config{Host: requestHost, Path: optOr(h.Options, OptPath, "/"), Mode: optOr(h.Options, OptMode, "auto"), ReuseConfig: &xhttp.ReuseConfig{MaxConcurrency: "1", HMaxRequestTimes: "600-900", HMaxReusableSecs: "1800-3000"}}
		makeTransport := func() http.RoundTripper {
			return xhttp.NewTransport(
				func(ctx context.Context) (net.Conn, error) { return previous.DialContext(ctx, "tcp", t.addr) },
				func(ctx context.Context, c net.Conn, h2 bool) (net.Conn, error) {
					if t.tls == nil {
						return c, nil
					}
					cfg := *t.tls
					if h2 {
						cfg.NextProtos = []string{"h2"}
					}
					return vmess.StreamTLSConn(ctx, c, &cfg)
				},
				func(ctx context.Context, cfg *quic.Config) (*quic.Conn, error) {
					if t.tls == nil {
						return nil, errors.New("xhttp HTTP/3 requires TLS")
					}
					if t.tls.Reality != nil {
						return nil, errors.New("xhttp HTTP/3 does not support REALITY")
					}
					tlsOptions := *t.tls
					tlsOptions.NextProtos = []string{"h3"}
					tlsConfig, err := tlsOptions.ToStdConfig()
					if err != nil {
						return nil, err
					}
					_, c, err := common.DialQuic(ctx, t.addr, nil, previous, tlsConfig, cfg, common.DialQuicOption{Early: true})
					return c, err
				}, alpn, 0)
		}
		client, err := xhttp.NewClient(cfg, makeTransport, nil, security == "reality")
		if err != nil {
			return nil, err
		}
		t.xhttp = client
	}
	return t, nil
}

func (t *streamTransport) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	if t.gun != nil {
		return t.gun.Dial()
	}
	if t.xhttp != nil {
		return t.xhttp.Dial(ctx)
	}
	c, err := t.dialer.DialContext(ctx, "tcp", t.addr)
	if err != nil {
		return nil, err
	}
	raw := c
	if t.tls != nil {
		cfg := *t.tls
		if t.network == "ws" {
			cfg.NextProtos = []string{"http/1.1"}
		}
		c, err = vmess.StreamTLSConn(ctx, c, &cfg)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
	}
	if t.network == "ws" {
		c, err = vmess.StreamWebsocketConn(ctx, c, &t.ws)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
	}
	return c, nil
}

func (t *streamTransport) ListenPacket(context.Context, string, string, netip.AddrPort) (net.PacketConn, error) {
	return nil, C.ErrNotSupport
}
func (t *streamTransport) Close() error {
	var errs []error
	if t.gun != nil {
		errs = append(errs, t.gun.Close())
	}
	if t.xhttp != nil {
		errs = append(errs, t.xhttp.Close())
	}
	return errors.Join(errs...)
}

type trojanAdapter struct {
	*outbound.Base
	transport *streamTransport
	key       [trojan.KeyLength]byte
}

func newTrojanAdapter(name string, h Hop, transport *streamTransport) *trojanAdapter {
	return &trojanAdapter{Base: outbound.NewBase(outbound.BaseOption{Name: name, Addr: transport.addr, Type: C.Trojan, UDP: true}), transport: transport, key: trojan.Key(h.Password)}
}

func (a *trojanAdapter) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	c, err := a.stream(ctx, metadata, trojan.CommandTCP)
	if err != nil {
		return nil, err
	}
	return outbound.NewConn(c, a), nil
}

// A later XHTTP/HTTP3 hop can require UDP even though the local entry is TCP-only.
func (a *trojanAdapter) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	if err := a.ResolveUDP(ctx, metadata); err != nil {
		return nil, err
	}
	c, err := a.stream(ctx, metadata, trojan.CommandUDP)
	if err != nil {
		return nil, err
	}
	return outbound.NewPacketConn(trojan.NewPacketConn(c), a), nil
}

func (a *trojanAdapter) SupportUOT() bool { return true }

func (a *trojanAdapter) stream(ctx context.Context, metadata *C.Metadata, command byte) (net.Conn, error) {
	c, err := a.transport.DialContext(ctx, "tcp", a.Addr())
	if err != nil {
		return nil, err
	}
	if err := trojan.WriteHeader(c, a.key, command, socks5.ParseAddr(metadata.RemoteAddress())); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (a *trojanAdapter) Close() error { return a.transport.Close() }

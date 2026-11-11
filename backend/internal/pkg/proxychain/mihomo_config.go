package proxychain

import (
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/proxydialer"
	"github.com/metacubex/mihomo/component/resolver"
	tlsC "github.com/metacubex/mihomo/component/tls"
	C "github.com/metacubex/mihomo/constant"
)

func init() {
	// The daemon executor normally configures this gate. Embedded adapters use
	// both address families; initialize once before any concurrent dialing.
	resolver.DisableIPv6 = false
}

func buildChainAdapters(hops []Hop) ([]C.ProxyAdapter, error) {
	adapters := make([]C.ProxyAdapter, 0, len(hops))
	var previous C.Dialer
	for i, h := range hops {
		a, err := buildAdapter(h, fmt.Sprintf("hop%d", i), previous)
		if err != nil {
			for j := len(adapters) - 1; j >= 0; j-- {
				_ = adapters[j].Close()
			}
			return nil, fmt.Errorf("hop %d: %w", i+1, err)
		}
		adapters = append(adapters, a)
		previous = proxydialer.New(a, false)
	}
	return adapters, nil
}

// buildAdapter uses explicit option structs: credentials never enter a global config or logger.
func buildAdapter(h Hop, name string, previous C.Dialer) (adapter C.ProxyAdapter, err error) {
	h.Protocol = strings.ToLower(h.Protocol)
	defer func() {
		if err != nil {
			err = fmt.Errorf("invalid %s settings: %w", h.Protocol, redactError(err, h))
		}
	}()
	if !IsSupportedProtocol(h.Protocol) {
		return nil, fmt.Errorf("unsupported proxy protocol")
	}
	if strings.TrimSpace(h.Host) == "" {
		return nil, fmt.Errorf("proxy host is required")
	}
	if h.Port < 1 || h.Port > 65535 {
		return nil, fmt.Errorf("proxy port is invalid")
	}
	if IsEngineProtocol(h.Protocol) && strings.TrimSpace(h.Password) == "" {
		return nil, fmt.Errorf("proxy requires password/uuid")
	}
	basic := outbound.BasicOption{DialerForAPI: previous}
	o := h.Options
	if o[OptSpiderX] != "" {
		return nil, fmt.Errorf("mihomo does not support reality spider_x")
	}
	fp := o[OptFingerprint]
	if fp == "none" {
		fp = ""
	}
	if fp != "" {
		if _, ok := tlsC.GetFingerprint(fp); !ok {
			return nil, fmt.Errorf("mihomo does not support requested TLS fingerprint")
		}
	}
	switch h.Protocol {
	case ProtocolHTTP, ProtocolHTTPS:
		option := outbound.HttpOption{BasicOption: basic, Name: name, Server: h.Host, Port: h.Port, UserName: h.Username, Password: h.Password, TLS: h.Protocol == ProtocolHTTPS}
		if h.Username != "" && h.Password == "" {
			option.Headers = map[string]string{"Proxy-Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(h.Username+":"))}
		}
		return outbound.NewHttp(option)
	case ProtocolSOCKS5, ProtocolSOCKS5H:
		return outbound.NewSocks5(outbound.Socks5Option{BasicOption: basic, Name: name, Server: h.Host, Port: h.Port, UserName: h.Username, Password: h.Password})
	case ProtocolSS:
		if o[OptMethod] == "" {
			return nil, fmt.Errorf("ss proxy requires method")
		}
		method := strings.ToLower(o[OptMethod])
		switch method {
		case "aead_aes_128_gcm":
			method = "aes-128-gcm"
		case "aead_aes_256_gcm":
			method = "aes-256-gcm"
		case "chacha20-poly1305", "aead_chacha20_poly1305":
			method = "chacha20-ietf-poly1305"
		case "xchacha20-poly1305", "aead_xchacha20_poly1305":
			method = "xchacha20-ietf-poly1305"
		case "plain":
			method = "none"
		}
		return outbound.NewShadowSocks(outbound.ShadowSocksOption{BasicOption: basic, Name: name, Server: h.Host, Port: h.Port, Password: h.Password, Cipher: method})
	case ProtocolAnyTLS:
		skip := false
		if v := o[OptSkipCertVerify]; v != "" {
			if v != "true" && v != "false" {
				return nil, fmt.Errorf("skip_cert_verify must be true or false")
			}
			skip = v == "true"
		}
		option := outbound.AnyTLSOption{BasicOption: basic, Name: name, Server: h.Host, Port: h.Port, Password: h.Password, SNI: o[OptSNI], ALPN: splitCSV(o[OptALPN]), ClientFingerprint: fp, SkipCertVerify: skip}
		for _, field := range []struct {
			key  string
			dest *int
		}{{OptIdleSessionCheckInterval, &option.IdleSessionCheckInterval}, {OptIdleSessionTimeout, &option.IdleSessionTimeout}, {OptMinIdleSession, &option.MinIdleSession}} {
			key, dest := field.key, field.dest
			if v := o[key]; v != "" {
				n, e := strconv.Atoi(v)
				if e != nil || n < 0 || (key != OptMinIdleSession && int64(n) > int64(^uint64(0)>>1)/1_000_000_000) {
					return nil, fmt.Errorf("%s must be a non-negative integer", key)
				}
				*dest = n
			}
		}
		return newAnyTLSAdapter(option), nil
	}

	network := strings.ToLower(optOr(o, OptNetwork, "tcp"))
	switch network {
	case "tcp", "raw":
		network = ""
	case "ws", "websocket":
		network = "ws"
	case "httpupgrade":
		network = "ws"
	case "grpc":
	case "xhttp", "splithttp":
		network = "xhttp"
	default:
		return nil, fmt.Errorf("unsupported transport network")
	}
	defaultTLS := "none"
	if h.Protocol == ProtocolTrojan {
		defaultTLS = "tls"
	}
	security := strings.ToLower(optOr(o, OptTLS, defaultTLS))
	if security != "none" && security != "tls" && security != "reality" {
		return nil, fmt.Errorf("unsupported tls mode")
	}
	sni := optOr(o, OptSNI, optOr(o, OptHost, h.Host))
	reality := outbound.RealityOptions{}
	if security == "reality" {
		if network != "" && network != "grpc" && network != "xhttp" {
			return nil, fmt.Errorf("reality only supports TCP, XHTTP and gRPC")
		}
		if o[OptPublicKey] == "" || o[OptSNI] == "" {
			return nil, fmt.Errorf("reality requires public_key and sni")
		}
		reality = outbound.RealityOptions{PublicKey: o[OptPublicKey], ShortID: o[OptShortID]}
		if fp == "" {
			fp = "chrome"
		}
	}
	ws := outbound.WSOptions{Path: optOr(o, OptPath, "/"), V2rayHttpUpgrade: strings.EqualFold(o[OptNetwork], "httpupgrade")}
	if network == "ws" && o[OptHost] != "" {
		ws.Headers = map[string]string{"Host": o[OptHost]}
	}
	grpc := outbound.GrpcOptions{GrpcServiceName: o[OptServiceName]}
	// Preserve the previous engine's bounded XHTTP transport reuse defaults.
	xh := outbound.XHTTPOptions{Path: optOr(o, OptPath, "/"), Host: o[OptHost], Mode: optOr(o, OptMode, "auto")}
	if network == "xhttp" {
		xh.ReuseSettings = &outbound.XHTTPReuseSettings{MaxConcurrency: "1", HMaxRequestTimes: "600-900", HMaxReusableSecs: "1800-3000"}
		switch xh.Mode {
		case "auto", "packet-up", "stream-up", "stream-one":
		default:
			return nil, fmt.Errorf("unsupported xhttp mode")
		}
	}
	alpn := splitCSV(o[OptALPN])
	if network == "xhttp" {
		if security == "none" {
			alpn = []string{"http/1.1"}
		}
		if security == "reality" {
			alpn = []string{"h2"}
		}
	}
	switch h.Protocol {
	case ProtocolVLESS:
		if flow := o[OptFlow]; flow != "" && flow != "xtls-rprx-vision" && flow != "xtls-rprx-vision-udp443" {
			return nil, fmt.Errorf("unsupported vless flow")
		}
		return outbound.NewVless(outbound.VlessOption{BasicOption: basic, Name: name, Server: h.Host, Port: h.Port, UUID: h.Password, Flow: o[OptFlow], Encryption: optOr(o, OptEncryption, "none"), TLS: security != "none", ALPN: alpn, ServerName: sni, ClientFingerprint: fp, RealityOpts: reality, Network: network, WSOpts: ws, GrpcOpts: grpc, XHTTPOpts: xh})
	case ProtocolVMess:
		option := outbound.VmessOption{BasicOption: basic, Name: name, Server: h.Host, Port: h.Port, UUID: h.Password, Cipher: optOr(o, OptSecurity, "auto"), TLS: security != "none", ALPN: alpn, ServerName: sni, ClientFingerprint: fp, RealityOpts: reality, Network: network, WSOpts: ws, GrpcOpts: grpc}
		if network != "xhttp" {
			return outbound.NewVmess(option)
		}
		transport, e := newStreamTransport(h, previous, network, security, sni, fp, reality)
		if e != nil {
			return nil, e
		}
		option.BasicOption.DialerForAPI = transport
		option.TLS = false
		option.RealityOpts = outbound.RealityOptions{}
		option.Network = ""
		a, e := outbound.NewVmess(option)
		if e != nil {
			_ = transport.Close()
			return nil, e
		}
		return &transportAdapter{ProxyAdapter: a, transport: transport}, nil
	case ProtocolTrojan:
		// Compose the mihomo transport and Trojan header independently so plaintext
		// Trojan and XHTTP retain their existing semantics as well as TLS/Reality.
		transport, e := newStreamTransport(h, previous, network, security, sni, fp, reality)
		if e != nil {
			return nil, e
		}
		return newTrojanAdapter(name, h, transport), nil
	}
	return nil, fmt.Errorf("unsupported proxy protocol")
}

func redactError(err error, h Hop) error {
	msg := err.Error()
	for _, secret := range []string{h.Password, h.Username, h.Options[OptPublicKey], h.Options[OptShortID], h.Options[OptEncryption]} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	return fmt.Errorf("%s", msg)
}

func optOr(o map[string]string, key, def string) string {
	if v := strings.TrimSpace(o[key]); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// closeWrite unwraps mihomo's connection decorators while preserving TLS and
// protocol-level half-close implementations when provided by the transport.
func closeWrite(c net.Conn) error {
	for {
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			return cw.CloseWrite()
		}
		up, ok := c.(interface{ Upstream() any })
		if !ok {
			return nil
		}
		next, ok := up.Upstream().(net.Conn)
		if !ok || next == c {
			return nil
		}
		c = next
	}
}

// transportAdapter owns the extra transport used by an otherwise plain adapter.
type transportAdapter struct {
	C.ProxyAdapter
	transport *streamTransport
}

func (a *transportAdapter) Close() error { _ = a.ProxyAdapter.Close(); return a.transport.Close() }

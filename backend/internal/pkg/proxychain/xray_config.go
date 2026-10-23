package proxychain

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"

	// Xray 功能注册：只引入链式代理需要的 app/proxy/transport，避免 main/distro/all 的命令行与配置加载器。
	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/policy"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/proxy/http"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	_ "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	_ "github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/proxy/trojan"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/proxy/vmess/outbound"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/headers/http"
	_ "github.com/xtls/xray-core/transport/internet/headers/noop"
	_ "github.com/xtls/xray-core/transport/internet/httpupgrade"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)

// hopTag 返回某条链第 i 跳的 outbound tag；prefix 区分同一 Xray 实例中的不同链。
func hopTag(prefix string, i int) string { return fmt.Sprintf("%s-hop%d", prefix, i) }

// buildChainOutbounds 把一条链翻译为 Xray outbound：hops[i] 经 sockopt.dialerProxy
// 从 hops[i-1] 拨出，hops[0] 直连。返回顺序与 hops 一致，落地 tag 为最后一个。
func buildChainOutbounds(prefix string, hops []Hop) ([]*core.OutboundHandlerConfig, error) {
	if len(hops) == 0 {
		return nil, fmt.Errorf("empty proxy chain")
	}
	if len(hops) > MaxHops {
		return nil, fmt.Errorf("proxy chain exceeds %d hops", MaxHops)
	}
	out := make([]*core.OutboundHandlerConfig, 0, len(hops))
	for i, h := range hops {
		ob, err := buildOutbound(h, prefix, i)
		if err != nil {
			return nil, fmt.Errorf("hop %d (%s): %w", i+1, h.Protocol, err)
		}
		out = append(out, ob)
	}
	return out, nil
}

// buildOutbound 构建单跳 outbound；index>0 时经上一跳拨出。
func buildOutbound(h Hop, prefix string, index int) (*core.OutboundHandlerConfig, error) {
	protocol := strings.ToLower(strings.TrimSpace(h.Protocol))
	settings := map[string]any{
		"address": h.Host,
		"port":    h.Port,
	}
	var xrayProtocol string
	switch protocol {
	case ProtocolHTTP, ProtocolHTTPS:
		xrayProtocol = "http"
		if h.Username != "" {
			settings["user"] = h.Username
			settings["pass"] = h.Password
		}
	case ProtocolSOCKS5, ProtocolSOCKS5H:
		xrayProtocol = "socks"
		if h.Username != "" {
			settings["user"] = h.Username
			settings["pass"] = h.Password
		}
	case ProtocolSS:
		xrayProtocol = "shadowsocks"
		settings["method"] = h.Options[OptMethod]
		settings["password"] = h.Password
	case ProtocolVMess:
		xrayProtocol = "vmess"
		settings["id"] = h.Password
		settings["security"] = optOr(h.Options, OptSecurity, "auto")
	case ProtocolVLESS:
		xrayProtocol = "vless"
		settings["id"] = h.Password
		settings["encryption"] = optOr(h.Options, OptEncryption, "none")
		if flow := h.Options[OptFlow]; flow != "" {
			settings["flow"] = flow
		}
	case ProtocolTrojan:
		xrayProtocol = "trojan"
		settings["password"] = h.Password
	default:
		return nil, fmt.Errorf("unsupported proxy protocol %q", h.Protocol)
	}

	stream := map[string]any{}
	switch protocol {
	case ProtocolHTTPS:
		stream["security"] = "tls"
		stream["tlsSettings"] = map[string]any{"serverName": h.Host}
	case ProtocolVMess, ProtocolVLESS, ProtocolTrojan:
		if err := fillTransport(stream, h, protocol); err != nil {
			return nil, err
		}
	}
	if index > 0 {
		stream["sockopt"] = map[string]any{"dialerProxy": hopTag(prefix, index-1)}
	}

	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	rawSettings := json.RawMessage(settingsJSON)
	ob := &conf.OutboundDetourConfig{
		Protocol: xrayProtocol,
		Tag:      hopTag(prefix, index),
		Settings: &rawSettings,
	}
	if len(stream) > 0 {
		streamJSON, err := json.Marshal(stream)
		if err != nil {
			return nil, err
		}
		var sc conf.StreamConfig
		if err := json.Unmarshal(streamJSON, &sc); err != nil {
			return nil, fmt.Errorf("invalid transport options: %w", err)
		}
		ob.StreamSetting = &sc
	}
	built, err := ob.Build()
	if err != nil {
		// Xray 的错误可能带出 uuid/密码原文（如 "invalid UUID: <id>"），只保留不含凭据的提示。
		return nil, sanitizeBuildError(err, h)
	}
	return built, nil
}

func sanitizeBuildError(err error, h Hop) error {
	msg := err.Error()
	for _, secret := range []string{h.Password, h.Username, h.Options[OptPublicKey], h.Options[OptShortID]} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	return fmt.Errorf("invalid %s settings: %s", h.Protocol, msg)
}

func fillTransport(stream map[string]any, h Hop, protocol string) error {
	o := h.Options
	network := strings.ToLower(optOr(o, OptNetwork, "tcp"))
	switch network {
	case "tcp", "raw":
		network = "tcp"
	case "ws", "websocket":
		network = "ws"
		stream["wsSettings"] = map[string]any{"path": optOr(o, OptPath, "/"), "host": o[OptHost]}
	case "httpupgrade":
		stream["httpupgradeSettings"] = map[string]any{"path": optOr(o, OptPath, "/"), "host": o[OptHost]}
	case "xhttp", "splithttp":
		network = "xhttp"
		stream["xhttpSettings"] = map[string]any{"path": optOr(o, OptPath, "/"), "host": o[OptHost], "mode": optOr(o, OptMode, "auto")}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": o[OptServiceName]}
	default:
		return fmt.Errorf("unsupported transport network %q", network)
	}
	stream["network"] = network

	defaultTLS := "none"
	if protocol == ProtocolTrojan {
		defaultTLS = "tls"
	}
	security := strings.ToLower(optOr(o, OptTLS, defaultTLS))
	sni := optOr(o, OptSNI, "")
	switch security {
	case "none", "":
		stream["security"] = "none"
	case "tls":
		if sni == "" {
			sni = optOr(o, OptHost, h.Host)
		}
		tlsSettings := map[string]any{"serverName": sni}
		if alpn := splitCSV(o[OptALPN]); len(alpn) > 0 {
			tlsSettings["alpn"] = alpn
		}
		if fp := o[OptFingerprint]; fp != "" {
			tlsSettings["fingerprint"] = fp
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = tlsSettings
	case "reality":
		if o[OptPublicKey] == "" {
			return fmt.Errorf("reality requires public_key")
		}
		if sni == "" {
			return fmt.Errorf("reality requires sni")
		}
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"serverName":  sni,
			"fingerprint": optOr(o, OptFingerprint, "chrome"),
			"publicKey":   o[OptPublicKey],
			"shortId":     o[OptShortID],
			"spiderX":     o[OptSpiderX],
		}
	default:
		return fmt.Errorf("unsupported tls mode %q", security)
	}
	return nil
}

func optOr(o map[string]string, key, def string) string {
	if v := strings.TrimSpace(o[key]); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

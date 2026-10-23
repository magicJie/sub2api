// Package proxychain 提供链式代理与加密代理（ss/vmess/vless/trojan）支持（fork）。
//
// 一条代理链由若干跳（Hop）组成，按拨号顺序排列：Hops[0] 最先被拨（通常是 ss/vmess
// 等加密前置代理），Hops[len-1] 是落地出口。链路由进程内嵌的 Xray-core 实例完成：
// 每跳翻译为一个 Xray outbound，后一跳通过 sockopt.dialerProxy 经前一跳拨出；
// 实例对外只暴露一个 127.0.0.1 上带随机凭据的 SOCKS5 入口，调用方拿到的仍是
// 普通 socks5h:// 代理 URL，现有 HTTP/WS/TLS 指纹等拨号路径无需改动。
package proxychain

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// MaxHops 限制一条链的最大跳数（含落地代理自身）。
const MaxHops = 5

// 支持的代理协议。
const (
	ProtocolHTTP    = "http"
	ProtocolHTTPS   = "https"
	ProtocolSOCKS5  = "socks5"
	ProtocolSOCKS5H = "socks5h"
	ProtocolSS      = "ss"
	ProtocolVMess   = "vmess"
	ProtocolVLESS   = "vless"
	ProtocolTrojan  = "trojan"
)

// Options 中使用的键。
const (
	OptMethod      = "method"       // ss 加密方式
	OptSecurity    = "security"     // vmess 加密方式
	OptFlow        = "flow"         // vless flow
	OptEncryption  = "encryption"   // vless encryption
	OptNetwork     = "network"      // 传输：tcp/ws/grpc/httpupgrade/xhttp
	OptPath        = "path"         // ws/httpupgrade/xhttp 路径
	OptHost        = "host"         // ws/httpupgrade/xhttp Host 头
	OptServiceName = "service_name" // grpc serviceName
	OptMode        = "mode"         // xhttp mode
	OptTLS         = "tls"          // 传输层安全：none/tls/reality
	OptSNI         = "sni"
	OptALPN        = "alpn" // 逗号分隔
	OptFingerprint = "fingerprint"
	OptPublicKey   = "public_key" // reality
	OptShortID     = "short_id"   // reality
	OptSpiderX     = "spider_x"   // reality
)

// Hop 是链中的一跳。
type Hop struct {
	Protocol string            `json:"protocol"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	Username string            `json:"username,omitempty"`
	Password string            `json:"password,omitempty"`
	Options  map[string]string `json:"options,omitempty"`
}

// IsSupportedProtocol 报告协议是否受支持。
func IsSupportedProtocol(protocol string) bool {
	switch strings.ToLower(protocol) {
	case ProtocolHTTP, ProtocolHTTPS, ProtocolSOCKS5, ProtocolSOCKS5H,
		ProtocolSS, ProtocolVMess, ProtocolVLESS, ProtocolTrojan:
		return true
	}
	return false
}

// IsXrayOnlyProtocol 报告协议是否只能经 Xray 承载（无法直接作为 http/socks 代理 URL 使用）。
func IsXrayOnlyProtocol(protocol string) bool {
	switch strings.ToLower(protocol) {
	case ProtocolSS, ProtocolVMess, ProtocolVLESS, ProtocolTrojan:
		return true
	}
	return false
}

func hasTransport(protocol string) bool {
	switch protocol {
	case ProtocolVMess, ProtocolVLESS, ProtocolTrojan:
		return true
	}
	return false
}

var allowedOptionKeys = map[string][]string{
	ProtocolSS:     {OptMethod},
	ProtocolVMess:  {OptSecurity},
	ProtocolVLESS:  {OptFlow, OptEncryption},
	ProtocolTrojan: {},
}

var transportOptionKeys = []string{
	OptNetwork, OptPath, OptHost, OptServiceName, OptMode,
	OptTLS, OptSNI, OptALPN, OptFingerprint, OptPublicKey, OptShortID, OptSpiderX,
}

// NormalizeOptions 只保留该协议适用的键，去掉空白值；结果为空时返回 nil。
func NormalizeOptions(protocol string, opts map[string]string) map[string]string {
	protocol = strings.ToLower(protocol)
	keys, ok := allowedOptionKeys[protocol]
	if !ok {
		return nil
	}
	if hasTransport(protocol) {
		keys = append(append([]string{}, keys...), transportOptionKeys...)
	}
	out := make(map[string]string)
	for _, k := range keys {
		if v := strings.TrimSpace(opts[k]); v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Validate 校验单跳的基础字段与协议参数（通过构建 Xray outbound 做最终校验）。
func (h Hop) Validate() error {
	if !IsSupportedProtocol(h.Protocol) {
		return fmt.Errorf("unsupported proxy protocol %q", h.Protocol)
	}
	if strings.TrimSpace(h.Host) == "" {
		return fmt.Errorf("proxy host is required")
	}
	if h.Port <= 0 || h.Port > 65535 {
		return fmt.Errorf("proxy port is invalid")
	}
	if IsXrayOnlyProtocol(h.Protocol) && strings.TrimSpace(h.Password) == "" {
		return fmt.Errorf("%s proxy requires password/uuid", h.Protocol)
	}
	if strings.EqualFold(h.Protocol, ProtocolSS) && strings.TrimSpace(h.Options[OptMethod]) == "" {
		return fmt.Errorf("ss proxy requires method")
	}
	_, err := buildOutbound(h, "validate", 0)
	return err
}

// Key 返回该跳的稳定标识（用于缓存键），包含凭据，不得输出到日志。
func (h Hop) key() string {
	var b strings.Builder
	b.WriteString(strings.ToLower(h.Protocol))
	b.WriteByte('|')
	b.WriteString(net.JoinHostPort(h.Host, fmt.Sprint(h.Port)))
	b.WriteByte('|')
	b.WriteString(h.Username)
	b.WriteByte('|')
	b.WriteString(h.Password)
	keys := make([]string, 0, len(h.Options))
	for k := range h.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(h.Options[k])
	}
	return b.String()
}

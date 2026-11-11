// Package proxychain provides reusable in-process mihomo proxy chains.
// Hops are ordered from the first dialed proxy to the final exit. Callers use
// an authenticated loopback socks5h URL, preserving existing HTTP/WS/TLS clients.
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
	ProtocolAnyTLS  = "anytls"
)

// Options 中使用的键。
const (
	OptMethod                   = "method"       // ss 加密方式
	OptSecurity                 = "security"     // vmess 加密方式
	OptFlow                     = "flow"         // vless flow
	OptEncryption               = "encryption"   // vless encryption
	OptNetwork                  = "network"      // 传输：tcp/ws/grpc/httpupgrade/xhttp
	OptPath                     = "path"         // ws/httpupgrade/xhttp 路径
	OptHost                     = "host"         // ws/httpupgrade/xhttp Host 头
	OptServiceName              = "service_name" // grpc serviceName
	OptMode                     = "mode"         // xhttp mode
	OptTLS                      = "tls"          // 传输层安全：none/tls/reality
	OptSNI                      = "sni"
	OptALPN                     = "alpn" // 逗号分隔
	OptFingerprint              = "fingerprint"
	OptPublicKey                = "public_key" // reality
	OptShortID                  = "short_id"   // reality
	OptSpiderX                  = "spider_x"   // reality
	OptSkipCertVerify           = "skip_cert_verify"
	OptIdleSessionCheckInterval = "idle_session_check_interval"
	OptIdleSessionTimeout       = "idle_session_timeout"
	OptMinIdleSession           = "min_idle_session"
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
		ProtocolSS, ProtocolVMess, ProtocolVLESS, ProtocolTrojan, ProtocolAnyTLS:
		return true
	}
	return false
}

// IsEngineProtocol reports protocols requiring an embedded outbound adapter.
func IsEngineProtocol(protocol string) bool {
	switch strings.ToLower(protocol) {
	case ProtocolSS, ProtocolVMess, ProtocolVLESS, ProtocolTrojan, ProtocolAnyTLS:
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
	ProtocolAnyTLS: {OptSNI, OptALPN, OptFingerprint, OptSkipCertVerify, OptIdleSessionCheckInterval, OptIdleSessionTimeout, OptMinIdleSession},
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

// Validate checks boundary fields and constructs then closes an outbound adapter.
func (h Hop) Validate() error {
	adapter, err := buildAdapter(h, "validate", nil)
	if err == nil {
		_ = adapter.Close()
	}
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

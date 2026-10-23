package proxychain

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParsedLink 是从分享链接解析出的代理配置。
type ParsedLink struct {
	Name     string            `json:"name"`
	Protocol string            `json:"protocol"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	Username string            `json:"username"`
	Password string            `json:"password"`
	Options  map[string]string `json:"options"`
}

// Hop 转为链中的一跳。
func (l *ParsedLink) Hop() Hop {
	return Hop{Protocol: l.Protocol, Host: l.Host, Port: l.Port, Username: l.Username, Password: l.Password, Options: l.Options}
}

// IsShareLink 报告字符串是否像受支持的分享链接。
func IsShareLink(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"ss://", "vmess://", "vless://", "trojan://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// ParseLink 解析 ss:// vmess:// vless:// trojan:// 分享链接，并校验结果。
// 错误信息不包含链接原文（可能含凭据）。
func ParseLink(raw string) (*ParsedLink, error) {
	raw = strings.TrimSpace(raw)
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, fmt.Errorf("invalid share link")
	}
	var (
		l   *ParsedLink
		err error
	)
	switch strings.ToLower(scheme) {
	case "ss":
		l, err = parseSS(raw)
	case "vmess":
		l, err = parseVMess(raw)
	case "vless", "trojan":
		l, err = parseURLStyle(strings.ToLower(scheme), raw)
	default:
		return nil, fmt.Errorf("unsupported share link scheme %q", scheme)
	}
	if err != nil {
		return nil, err
	}
	l.Options = NormalizeOptions(l.Protocol, l.Options)
	if l.Name == "" {
		l.Name = fmt.Sprintf("%s-%s", l.Protocol, l.Host)
	}
	if err := l.Hop().Validate(); err != nil {
		return nil, err
	}
	return l, nil
}

func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "=")
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

func splitFragment(raw string) (string, string) {
	body, frag, _ := strings.Cut(raw, "#")
	name, err := url.PathUnescape(frag)
	if err != nil {
		name = frag
	}
	return body, strings.TrimSpace(name)
}

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p <= 0 || p > 65535 {
		return 0, fmt.Errorf("invalid port")
	}
	return p, nil
}

// parseSS 支持 SIP002（ss://base64(method:password)@host:port 或明文 userinfo）
// 与旧格式（ss://base64(method:password@host:port)）。
func parseSS(raw string) (*ParsedLink, error) {
	body, name := splitFragment(raw)
	body = strings.TrimPrefix(body, body[:len("ss://")])
	body, _, _ = strings.Cut(body, "?") // 插件参数不支持，忽略

	var userinfo, hostport string
	if at := strings.LastIndex(body, "@"); at >= 0 {
		userinfo, hostport = body[:at], body[at+1:]
		if dec, err := decodeBase64Loose(userinfo); err == nil && strings.Contains(string(dec), ":") {
			userinfo = string(dec)
		} else if un, err := url.PathUnescape(userinfo); err == nil {
			userinfo = un
		}
	} else {
		dec, err := decodeBase64Loose(body)
		if err != nil {
			return nil, fmt.Errorf("invalid ss link")
		}
		at := strings.LastIndex(string(dec), "@")
		if at < 0 {
			return nil, fmt.Errorf("invalid ss link")
		}
		userinfo, hostport = string(dec[:at]), string(dec[at+1:])
	}
	method, password, ok := strings.Cut(userinfo, ":")
	if !ok || method == "" || password == "" {
		return nil, fmt.Errorf("invalid ss link: missing method or password")
	}
	host, portStr, err := net.SplitHostPort(strings.TrimSuffix(hostport, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid ss link: bad host")
	}
	port, err := parsePort(portStr)
	if err != nil {
		return nil, err
	}
	return &ParsedLink{
		Name: name, Protocol: ProtocolSS, Host: host, Port: port, Password: password,
		Options: map[string]string{OptMethod: strings.ToLower(method)},
	}, nil
}

// parseVMess 解析 v2rayN 格式：vmess://base64(json)。
func parseVMess(raw string) (*ParsedLink, error) {
	dec, err := decodeBase64Loose(strings.TrimSpace(raw[len("vmess://"):]))
	if err != nil {
		return nil, fmt.Errorf("invalid vmess link")
	}
	var v map[string]any
	if err := json.Unmarshal(dec, &v); err != nil {
		return nil, fmt.Errorf("invalid vmess link: bad json")
	}
	str := func(k string) string {
		switch x := v[k].(type) {
		case string:
			return strings.TrimSpace(x)
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
		return ""
	}
	port, err := parsePort(str("port"))
	if err != nil {
		return nil, err
	}
	opts := map[string]string{
		OptSecurity:    str("scy"),
		OptNetwork:     str("net"),
		OptPath:        str("path"),
		OptHost:        str("host"),
		OptSNI:         str("sni"),
		OptALPN:        str("alpn"),
		OptFingerprint: str("fp"),
	}
	if strings.EqualFold(str("tls"), "tls") {
		opts[OptTLS] = "tls"
	}
	if opts[OptNetwork] == "grpc" {
		opts[OptServiceName] = opts[OptPath]
		opts[OptPath] = ""
	}
	if typ := str("type"); typ != "" && typ != "none" && (opts[OptNetwork] == "" || opts[OptNetwork] == "tcp") {
		return nil, fmt.Errorf("vmess tcp header type %q is not supported", typ)
	}
	return &ParsedLink{
		Name: str("ps"), Protocol: ProtocolVMess, Host: str("add"), Port: port,
		Password: str("id"), Options: opts,
	}, nil
}

// parseURLStyle 解析 vless://uuid@host:port?... 与 trojan://password@host:port?...
func parseURLStyle(protocol, raw string) (*ParsedLink, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return nil, fmt.Errorf("invalid %s link", protocol)
	}
	port, err := parsePort(u.Port())
	if err != nil {
		return nil, err
	}
	q := u.Query()
	opts := map[string]string{
		OptNetwork:     q.Get("type"),
		OptPath:        q.Get("path"),
		OptHost:        q.Get("host"),
		OptServiceName: q.Get("serviceName"),
		OptMode:        q.Get("mode"),
		OptTLS:         q.Get("security"),
		OptSNI:         firstNonEmpty(q.Get("sni"), q.Get("peer")),
		OptALPN:        q.Get("alpn"),
		OptFingerprint: q.Get("fp"),
		OptPublicKey:   q.Get("pbk"),
		OptShortID:     q.Get("sid"),
		OptSpiderX:     q.Get("spx"),
	}
	if protocol == ProtocolVLESS {
		opts[OptFlow] = q.Get("flow")
		opts[OptEncryption] = q.Get("encryption")
	}
	if ht := q.Get("headerType"); ht != "" && ht != "none" {
		return nil, fmt.Errorf("%s header type %q is not supported", protocol, ht)
	}
	name, _ := url.PathUnescape(u.Fragment)
	return &ParsedLink{
		Name: strings.TrimSpace(name), Protocol: protocol, Host: u.Hostname(), Port: port,
		Password: u.User.Username(), Options: opts,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

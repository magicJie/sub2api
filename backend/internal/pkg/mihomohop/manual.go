package mihomohop

import (
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
	"net/netip"
	"strings"
)

func netipAddress(host string) (netip.Addr, error) { return netip.ParseAddr(host) }

func manualProxy(name string, hop proxychain.Hop) (map[string]any, error) {
	if hop.Host == "" || hop.Port < 1 || hop.Port > 65535 {
		return nil, errors.New("invalid manual first-hop member address")
	}
	mapping := map[string]any{"name": name, "server": hop.Host, "port": hop.Port}
	protocol := strings.ToLower(hop.Protocol)
	switch protocol {
	case proxychain.ProtocolHTTP, proxychain.ProtocolHTTPS:
		mapping["type"] = "http"
		mapping["tls"] = protocol == proxychain.ProtocolHTTPS
		mapping["username"] = hop.Username
		mapping["password"] = hop.Password
	case proxychain.ProtocolSOCKS5, proxychain.ProtocolSOCKS5H:
		mapping["type"] = "socks5"
		mapping["username"] = hop.Username
		mapping["password"] = hop.Password
	case proxychain.ProtocolSS:
		mapping["type"] = "ss"
		mapping["cipher"] = hop.Options[proxychain.OptMethod]
		mapping["password"] = hop.Password
	case proxychain.ProtocolVMess, proxychain.ProtocolVLESS, proxychain.ProtocolTrojan:
		mapping["type"] = protocol
		if protocol == proxychain.ProtocolTrojan {
			mapping["password"] = hop.Password
		} else {
			mapping["uuid"] = hop.Password
		}
		if protocol == proxychain.ProtocolVMess {
			cipher := hop.Options[proxychain.OptSecurity]
			if cipher == "" {
				cipher = "auto"
			}
			mapping["cipher"] = cipher
			mapping["alterId"] = 0
		}
		if protocol == proxychain.ProtocolVLESS {
			mapping["flow"] = hop.Options[proxychain.OptFlow]
			mapping["encryption"] = hop.Options[proxychain.OptEncryption]
		}
		security := hop.Options[proxychain.OptTLS]
		if security == "" && protocol == proxychain.ProtocolTrojan {
			security = "tls"
		}
		mapping["tls"] = security == "tls" || security == "reality"
		sni := hop.Options[proxychain.OptSNI]
		if sni == "" {
			sni = hop.Options[proxychain.OptHost]
		}
		if sni == "" {
			sni = hop.Host
		}
		mapping["servername"] = sni
		mapping["sni"] = sni
		mapping["client-fingerprint"] = hop.Options[proxychain.OptFingerprint]
		if security == "reality" {
			mapping["reality-opts"] = map[string]any{"public-key": hop.Options[proxychain.OptPublicKey], "short-id": hop.Options[proxychain.OptShortID]}
		}
		if alpn := hop.Options[proxychain.OptALPN]; alpn != "" {
			mapping["alpn"] = strings.Split(alpn, ",")
		}
		network := hop.Options[proxychain.OptNetwork]
		switch network {
		case "", "tcp", "raw":
		case "ws", "websocket", "httpupgrade":
			mapping["network"] = "ws"
			mapping["ws-opts"] = map[string]any{"path": hop.Options[proxychain.OptPath], "headers": map[string]string{"Host": hop.Options[proxychain.OptHost]}, "v2ray-http-upgrade": network == "httpupgrade"}
		case "grpc":
			mapping["network"] = "grpc"
			mapping["grpc-opts"] = map[string]string{"grpc-service-name": hop.Options[proxychain.OptServiceName]}
		case "xhttp":
			mapping["network"] = "xhttp"
			mapping["xhttp-opts"] = map[string]string{"path": hop.Options[proxychain.OptPath], "host": hop.Options[proxychain.OptHost], "mode": hop.Options[proxychain.OptMode]}
		default:
			return nil, errors.New("unsupported manual first-hop transport")
		}
	default:
		return nil, errors.New("unsupported manual first-hop protocol")
	}
	return mapping, nil
}

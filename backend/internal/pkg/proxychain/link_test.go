package proxychain

import (
	"encoding/base64"
	"strings"
	"testing"
)

const testUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

func TestParseLink(t *testing.T) {
	ssUserinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pa:ss"))
	ssLegacy := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw@1.2.3.4:8388"))
	vmessJSON := `{"v":"2","ps":"HK vmess","add":"vm.example.com","port":"443","id":"` + testUUID +
		`","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"vm.example.com"}`
	vmessLink := "vmess://" + base64.StdEncoding.EncodeToString([]byte(vmessJSON))

	cases := []struct {
		name string
		link string
		want ParsedLink
	}{
		{
			name: "ss sip002 base64 userinfo",
			link: "ss://" + ssUserinfo + "@ss.example.com:8388#HK%20SS",
			want: ParsedLink{Name: "HK SS", Protocol: "ss", Host: "ss.example.com", Port: 8388, Password: "pa:ss",
				Options: map[string]string{OptMethod: "aes-256-gcm"}},
		},
		{
			name: "ss 2022 plain userinfo",
			link: "ss://2022-blake3-aes-128-gcm:" + "AAAAAAAAAAAAAAAAAAAAAA%3D%3D" + "@[2001:db8::1]:443",
			want: ParsedLink{Name: "ss-2001:db8::1", Protocol: "ss", Host: "2001:db8::1", Port: 443, Password: "AAAAAAAAAAAAAAAAAAAAAA==",
				Options: map[string]string{OptMethod: "2022-blake3-aes-128-gcm"}},
		},
		{
			name: "ss legacy",
			link: "ss://" + ssLegacy + "#legacy",
			want: ParsedLink{Name: "legacy", Protocol: "ss", Host: "1.2.3.4", Port: 8388, Password: "pw",
				Options: map[string]string{OptMethod: "chacha20-ietf-poly1305"}},
		},
		{
			name: "vmess ws tls",
			link: vmessLink,
			want: ParsedLink{Name: "HK vmess", Protocol: "vmess", Host: "vm.example.com", Port: 443, Password: testUUID,
				Options: map[string]string{OptSecurity: "auto", OptNetwork: "ws", OptHost: "cdn.example.com", OptPath: "/ws", OptTLS: "tls", OptSNI: "vm.example.com"}},
		},
		{
			name: "vless reality vision",
			link: "vless://" + testUUID + "@vl.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw&sid=6ba85179e30d4fc2&type=tcp#VL",
			want: ParsedLink{Name: "VL", Protocol: "vless", Host: "vl.example.com", Port: 443, Password: testUUID,
				Options: map[string]string{OptEncryption: "none", OptFlow: "xtls-rprx-vision", OptTLS: "reality", OptSNI: "www.microsoft.com",
					OptFingerprint: "chrome", OptPublicKey: "Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw", OptShortID: "6ba85179e30d4fc2", OptNetwork: "tcp"}},
		},
		{
			name: "trojan grpc",
			link: "trojan://secret@tj.example.com:443?security=tls&type=grpc&serviceName=gun&sni=tj.example.com#TJ",
			want: ParsedLink{Name: "TJ", Protocol: "trojan", Host: "tj.example.com", Port: 443, Password: "secret",
				Options: map[string]string{OptTLS: "tls", OptNetwork: "grpc", OptServiceName: "gun", OptSNI: "tj.example.com"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLink(tc.link)
			if err != nil {
				t.Fatalf("ParseLink: %v", err)
			}
			if got.Name != tc.want.Name || got.Protocol != tc.want.Protocol || got.Host != tc.want.Host ||
				got.Port != tc.want.Port || got.Password != tc.want.Password {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			if len(got.Options) != len(tc.want.Options) {
				t.Fatalf("options got %v want %v", got.Options, tc.want.Options)
			}
			for k, v := range tc.want.Options {
				if got.Options[k] != v {
					t.Fatalf("option %s got %q want %q", k, got.Options[k], v)
				}
			}
		})
	}
}

func TestParseLinkErrorsDoNotLeakSecrets(t *testing.T) {
	for _, link := range []string{
		"ss://not-base64-secretvalue",
		"vless://secretvalue@host:99999",
		"trojan://secretvalue@host",
		"vmess://secretvalue",
		"http://secretvalue@host:80",
	} {
		_, err := ParseLink(link)
		if err == nil {
			t.Fatalf("expected error for %q", link)
		}
		if strings.Contains(err.Error(), "secretvalue") {
			t.Fatalf("error leaks secret: %v", err)
		}
	}
}

func TestHopValidate(t *testing.T) {
	bad := []Hop{
		{Protocol: "ss", Host: "h", Port: 1, Password: "p"}, // 缺 method
		{Protocol: "ss", Host: "h", Port: 1, Password: "p", Options: map[string]string{OptMethod: "rc4"}},
		{Protocol: "vless", Host: "h", Port: 1, Password: testUUID, Options: map[string]string{OptTLS: "reality", OptSNI: "a.com"}}, // 缺 public_key
		{Protocol: "vmess", Host: "h", Port: 1, Password: testUUID, Options: map[string]string{OptNetwork: "quic"}},
		{Protocol: "trojan", Host: "h", Port: 1},
		{Protocol: "ftp", Host: "h", Port: 1},
		{Protocol: "http", Host: "", Port: 1},
	}
	for i, h := range bad {
		if err := h.Validate(); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, h)
		}
	}
	good := []Hop{
		{Protocol: "http", Host: "h", Port: 8080},
		{Protocol: "https", Host: "h", Port: 443, Username: "u", Password: "p"},
		{Protocol: "socks5h", Host: "h", Port: 1080},
		{Protocol: "vmess", Host: "h", Port: 443, Password: testUUID, Options: map[string]string{OptNetwork: "grpc", OptServiceName: "x", OptTLS: "tls"}},
		{Protocol: "vless", Host: "h", Port: 443, Password: testUUID, Options: map[string]string{OptNetwork: "xhttp", OptTLS: "tls"}},
		{Protocol: "trojan", Host: "h", Port: 443, Password: "p", Options: map[string]string{OptNetwork: "httpupgrade"}},
	}
	for i, h := range good {
		if err := h.Validate(); err != nil {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
	}
}

func TestValidateErrorRedactsSecret(t *testing.T) {
	h := Hop{Protocol: ProtocolVLESS, Host: "h", Port: 443, Password: testUUID,
		Options: map[string]string{OptTLS: "reality", OptSNI: "a.com", OptPublicKey: "not-a-valid-key-secretpk"}}
	err := h.Validate()
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), "secretpk") || strings.Contains(err.Error(), testUUID) {
		t.Fatalf("error leaks secret: %v", err)
	}
}

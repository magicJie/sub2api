package proxychain

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestManagerIPv6LiteralFirstHop(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ipv6-resource" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "ipv6-first-hop-origin-response")
	}))
	defer target.Close()

	for _, tc := range []struct {
		name     string
		protocol string
		chain    bool
	}{
		{"http-chain", ProtocolHTTP, true},
		{"socks5-chain", ProtocolSOCKS5, true},
		{"anytls-single", ProtocolAnyTLS, false},
		{"anytls-chain", ProtocolAnyTLS, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp6", "[::1]:0")
			if err != nil {
				if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) {
					t.Skipf("IPv6 loopback bind unavailable: %v", err)
				}
				t.Fatal(err)
			}
			host, port := splitHostPort(t, ln.Addr().String())
			first := Hop{Protocol: tc.protocol, Host: host, Port: port}
			firstCount := &atomic.Int64{}
			if tc.protocol == ProtocolAnyTLS {
				first, firstCount, _ = startAnyTLSServerOn(t, "synthetic-ipv6-anytls-password", ln)
			} else {
				if tc.protocol == ProtocolSOCKS5 {
					first.Username, first.Password = "synthetic-ipv6-user", "synthetic-ipv6-password"
				}
				serveTestListener(t, ln, nil, func(c net.Conn) {
					_ = c.SetDeadline(time.Now().Add(5 * time.Second))
					br := bufio.NewReader(c)
					var destination string
					if tc.protocol == ProtocolHTTP {
						request, err := http.ReadRequest(br)
						if err != nil || request.Method != http.MethodConnect {
							return
						}
						destination = request.Host
					} else {
						_, _, ok := socksAuthenticate(br, c, func(user, password string) (string, bool) {
							return "ipv6-first-hop", user == first.Username && password == first.Password
						})
						if !ok {
							return
						}
						host, port, err := readConnectRequest(br, c)
						if err != nil {
							return
						}
						destination = net.JoinHostPort(host, strconv.Itoa(int(port)))
					}
					up, err := net.DialTimeout("tcp", destination, 5*time.Second)
					if err != nil {
						return
					}
					defer up.Close()
					firstCount.Add(1)
					if tc.protocol == ProtocolHTTP {
						_, err = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
					} else {
						err = writeReply(c, socksRepSucceeded)
					}
					if err != nil {
						return
					}
					_ = c.SetDeadline(time.Time{})
					pipe(c, up)
				})
			}
			hops := []Hop{first}
			var exitCount *atomic.Int64
			if tc.chain {
				addr, count := startConnectProxy(t, "synthetic-exit-user", "synthetic-exit-password")
				host, port := splitHostPort(t, addr)
				hops = append(hops, Hop{Protocol: ProtocolHTTP, Host: host, Port: port, Username: "synthetic-exit-user", Password: "synthetic-exit-password"})
				exitCount = count
			}
			m := &Manager{}
			defer m.Close()
			resp, err := proxyClient(t, m, hops).Get(target.URL + "/ipv6-resource")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || string(body) != "ipv6-first-hop-origin-response" {
				t.Fatalf("body=%q error=%v", body, err)
			}
			if firstCount.Load() != 1 {
				t.Fatalf("IPv6 first-hop connections=%d, want 1", firstCount.Load())
			}
			if exitCount != nil && exitCount.Load() != 1 {
				t.Fatalf("exit-hop requests=%d, want 1", exitCount.Load())
			}
		})
	}
}

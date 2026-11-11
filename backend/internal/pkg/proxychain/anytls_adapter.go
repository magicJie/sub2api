// Derived from MetaCubeX/mihomo v1.19.34 adapter/outbound/anytls.go.
// SPDX-License-Identifier: GPL-3.0-only
// See third_party/mihomo/LICENSE and third_party/mihomo/README.md.

package proxychain

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/third_party/mihomo/transport/anytls"
	"github.com/metacubex/mihomo/adapter/outbound"
	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/component/proxydialer"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/transport/vmess"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/metacubex/sing/common/uot"
)

// anyTLSAdapter binds mihomo's outbound API to the locally synchronized session
// component. TLS, wire framing, padding and idle-session reuse retain mihomo semantics.
type anyTLSAdapter struct {
	*outbound.Base
	client *anytls.Client
}

func newAnyTLSAdapter(option outbound.AnyTLSOption) *anyTLSAdapter {
	base := outbound.NewBase(outbound.BaseOption{
		Name:         option.Name,
		Addr:         net.JoinHostPort(option.Server, strconv.Itoa(option.Port)),
		Type:         C.AnyTLS,
		ProviderName: option.ProviderName,
		UDP:          option.UDP,
		TFO:          option.TFO,
		MPTCP:        option.MPTCP,
		Interface:    option.Interface,
		RoutingMark:  option.RoutingMark,
		Prefer:       option.IPVersion,
	})
	sni := option.SNI
	if sni == "" {
		sni = option.Server
	}
	client := anytls.NewClient(context.Background(), anytls.ClientConfig{
		Password:                 option.Password,
		Server:                   M.ParseSocksaddrHostPort(option.Server, uint16(option.Port)),
		Dialer:                   proxydialer.NewSingDialer(option.NewDialer(base.DialOptions())),
		ClientMetadata:           option.ClientMetadata,
		IdleSessionCheckInterval: time.Duration(option.IdleSessionCheckInterval) * time.Second,
		IdleSessionTimeout:       time.Duration(option.IdleSessionTimeout) * time.Second,
		MinIdleSession:           option.MinIdleSession,
		DisableReuse:             option.DisableReuse,
		TLSConfig: &vmess.TLSConfig{
			Host:              sni,
			SkipCertVerify:    option.SkipCertVerify,
			NextProtos:        option.ALPN,
			ClientFingerprint: option.ClientFingerprint,
		},
	})
	return &anyTLSAdapter{Base: base, client: client}
}

func (a *anyTLSAdapter) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	c, err := a.client.CreateProxy(ctx, M.ParseSocksaddrHostPort(metadata.String(), metadata.DstPort))
	if err != nil {
		return nil, err
	}
	return outbound.NewConn(c, a), nil
}

func (a *anyTLSAdapter) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	if err := a.ResolveUDP(ctx, metadata); err != nil {
		return nil, err
	}
	c, err := a.client.CreateProxy(ctx, uot.RequestDestination(2))
	if err != nil {
		return nil, err
	}
	destination := M.SocksaddrFromNet(metadata.UDPAddr())
	return outbound.NewPacketConn(N.NewThreadSafePacketConn(uot.NewLazyConn(c, uot.Request{Destination: destination})), a), nil
}

func (a *anyTLSAdapter) SupportUOT() bool { return true }
func (a *anyTLSAdapter) Close() error     { return a.client.Close() }

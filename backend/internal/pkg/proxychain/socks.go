package proxychain

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"
)

// 本地 SOCKS5 入口：仅支持用户名/密码认证（RFC 1929）与 CONNECT 命令，
// 只监听 127.0.0.1，用户名决定走哪条链。

const (
	socksVersion       = 0x05
	socksAuthUserPass  = 0x02
	socksAuthNoAccept  = 0xff
	socksCmdConnect    = 0x01
	socksAtypIPv4      = 0x01
	socksAtypDomain    = 0x03
	socksAtypIPv6      = 0x04
	socksRepSucceeded  = 0x00
	socksRepFailure    = 0x01
	socksRepNotAllowed = 0x02
	socksRepCmdUnsupp  = 0x07
	socksRepAtypUnsupp = 0x08

	handshakeTimeout = 10 * time.Second
	halfCloseGrace   = 30 * time.Second
)

func (m *Manager) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Warn("proxy_chain_accept_failed", "error", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go m.handleConn(conn)
	}
}

func (m *Manager) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	br := bufio.NewReader(conn)

	exitTag, user, ok := m.socksAuth(br, conn)
	if !ok {
		return
	}
	if !m.trackConn(user, conn) {
		return
	}
	defer m.untrackConn(user, conn)
	host, port, err := readConnectRequest(br, conn)
	if err != nil {
		return
	}

	// core.Dial 异步派发：上下文贯穿整个连接生命周期，连接结束时才取消。
	// 链路是否打通要到首次读写才知道，失败时客户端看到的是连接被关闭（EOF）。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream, err := m.dial(ctx, exitTag, host, port)
	if err != nil {
		slog.Debug("proxy_chain_dial_failed", "target", net.JoinHostPort(host, strconv.Itoa(int(port))), "error", err)
		_ = writeReply(conn, socksRepFailure)
		return
	}
	defer func() { _ = upstream.Close() }()
	if err := writeReply(conn, socksRepSucceeded); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})

	// 握手阶段 bufio 可能已预读部分载荷，先转发缓冲区。
	if n := br.Buffered(); n > 0 {
		buf, _ := br.Peek(n)
		if _, err := upstream.Write(buf); err != nil {
			return
		}
	}
	pipe(conn, upstream)
}

func (m *Manager) socksAuth(br *bufio.Reader, conn net.Conn) (string, string, bool) {
	return socksAuthenticate(br, conn, m.lookup)
}

func socksAuthenticate(br *bufio.Reader, conn net.Conn, lookup func(string, string) (string, bool)) (string, string, bool) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil || head[0] != socksVersion {
		return "", "", false
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return "", "", false
	}
	supported := false
	for _, method := range methods {
		if method == socksAuthUserPass {
			supported = true
			break
		}
	}
	if !supported {
		_, _ = conn.Write([]byte{socksVersion, socksAuthNoAccept})
		return "", "", false
	}
	if _, err := conn.Write([]byte{socksVersion, socksAuthUserPass}); err != nil {
		return "", "", false
	}
	// RFC 1929: VER ULEN UNAME PLEN PASSWD
	ver, err := br.ReadByte()
	if err != nil || ver != 0x01 {
		return "", "", false
	}
	user, err := readLenPrefixed(br)
	if err != nil {
		return "", "", false
	}
	pass, err := readLenPrefixed(br)
	if err != nil {
		return "", "", false
	}
	exitTag, ok := lookup(user, pass)
	if !ok {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return "", "", false
	}
	if _, err := conn.Write([]byte{0x01, 0x00}); err != nil {
		return "", "", false
	}
	return exitTag, user, true
}

func readConnectRequest(br *bufio.Reader, conn net.Conn) (string, uint16, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(br, head); err != nil {
		return "", 0, err
	}
	if head[0] != socksVersion {
		return "", 0, errors.New("bad socks version")
	}
	if head[1] != socksCmdConnect {
		_ = writeReply(conn, socksRepCmdUnsupp)
		return "", 0, errors.New("unsupported socks command")
	}
	var host string
	switch head[3] {
	case socksAtypIPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case socksAtypIPv6:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case socksAtypDomain:
		d, err := readLenPrefixed(br)
		if err != nil {
			return "", 0, err
		}
		host = d
	default:
		_ = writeReply(conn, socksRepAtypUnsupp)
		return "", 0, errors.New("unsupported address type")
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return "", 0, err
	}
	if host == "" {
		_ = writeReply(conn, socksRepNotAllowed)
		return "", 0, errors.New("empty target host")
	}
	return host, binary.BigEndian.Uint16(pb), nil
}

func readLenPrefixed(br *bufio.Reader) (string, error) {
	n, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(br, b); err != nil {
		return "", err
	}
	return string(b), nil
}

func writeReply(conn net.Conn, rep byte) error {
	_, err := conn.Write([]byte{socksVersion, rep, 0x00, socksAtypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

// pipe 双向转发直到两个方向都结束。一侧 EOF 时尽量半关闭对端写方向；
// 对端不支持半关闭（如 Xray 的连接）时保持连接，由另一方向结束或 Xray 的 uplinkOnly/downlinkOnly 超时收尾，
// 避免客户端 shutdown(SHUT_WR) 后响应被截断。
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	// 一个方向结束后给另一方向留出收尾时间，然后整体关闭，避免 goroutine 泄漏。
	timer := time.NewTimer(halfCloseGrace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	_ = a.Close()
	_ = b.Close()
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

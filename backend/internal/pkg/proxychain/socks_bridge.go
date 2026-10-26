package proxychain

// 链式代理（fork）：复用 SOCKS5 协议处理，为 mihomo 首跳组暴露随机认证回环入口。

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"time"
)

type SOCKSBridge struct {
	listener    net.Listener
	endpoint    Hop
	dial        func(context.Context, string, uint16) (net.Conn, error)
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]struct{}
	wg          sync.WaitGroup
}

func NewSOCKSBridge(dial func(context.Context, string, uint16) (net.Conn, error)) (*SOCKSBridge, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	bridge := &SOCKSBridge{listener: listener, dial: dial, ctx: ctx, cancel: cancel, connections: make(map[net.Conn]struct{})}
	bridge.endpoint = Hop{Protocol: ProtocolSOCKS5, Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: hex.EncodeToString(secret[:16]), Password: hex.EncodeToString(secret[16:])}
	bridge.wg.Add(1)
	go bridge.serve()
	return bridge, nil
}

func (s *SOCKSBridge) Endpoint() Hop { return s.endpoint }

func (s *SOCKSBridge) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	err := s.listener.Close()
	for conn := range s.connections {
		_ = conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return err
}

func (s *SOCKSBridge) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.connections[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *SOCKSBridge) handle(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close(); s.mu.Lock(); delete(s.connections, conn); s.mu.Unlock() }()
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	reader := bufio.NewReader(conn)
	_, _, ok := socksAuthenticate(reader, conn, func(user, pass string) (string, bool) {
		return "", constantTimeEqual(user, s.endpoint.Username) && constantTimeEqual(pass, s.endpoint.Password)
	})
	if !ok {
		return
	}
	host, port, err := readConnectRequest(reader, conn)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, handshakeTimeout)
	defer cancel()
	upstream, err := s.dial(ctx, host, port)
	if err != nil {
		_ = writeReply(conn, socksRepFailure)
		return
	}
	defer upstream.Close()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.connections[upstream] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.connections, upstream); s.mu.Unlock() }()
	if err := writeReply(conn, socksRepSucceeded); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if n := reader.Buffered(); n > 0 {
		data, _ := reader.Peek(n)
		if _, err := upstream.Write(data); err != nil {
			return
		}
	}
	pipe(conn, upstream)
}

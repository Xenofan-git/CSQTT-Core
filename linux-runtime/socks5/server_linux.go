//go:build linux

// Package socks5 implements the local CSQTT egress adapter used by sing-box.
// It is an independent implementation; it does not depend on LaLune.
package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	version5       = byte(5)
	methodNoAuth   = byte(0)
	methodNoAccept = byte(0xff)
	cmdConnect     = byte(1)
	cmdUDPAssociate = byte(3)
	atypIPv4       = byte(1)
	atypDomain     = byte(3)
	atypIPv6       = byte(4)
	replySucceeded = byte(0)
	replyGeneral   = byte(1)
	replyNotAllowed = byte(2)
	replyNetwork   = byte(3)
	replyHost      = byte(4)
	replyRefused   = byte(5)
	replyCommand   = byte(7)
	replyAddress   = byte(8)
	soMarkOption   = 36 // Linux SOL_SOCKET/SO_MARK
)

type ResolveFunc func(context.Context, string) ([]net.IPAddr, error)

type Config struct {
	ListenAddr       string
	Mark             int
	DialTimeout      time.Duration
	ResolveTimeout   time.Duration
	StopTimeout      time.Duration
	MaxSessions      int
	MaxUDPTargets    int
	Resolve          ResolveFunc
	Logger           func(format string, args ...any)
}

type Server struct {
	cfg Config

	mu       sync.Mutex
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	running  bool
	sessions map[io.Closer]struct{}
	handlers sync.WaitGroup
}

func New(cfg Config) (*Server, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:12500"
	}
	host, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid SOCKS5 listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("CSQTT SOCKS5 must bind to an explicit loopback IP; LAN and wildcard binds are forbidden")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("invalid SOCKS5 listen port: %w", err)
	}
	if cfg.Mark < 0 {
		return nil, errors.New("SO_MARK must not be negative")
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 15 * time.Second
	}
	if cfg.ResolveTimeout <= 0 {
		cfg.ResolveTimeout = 8 * time.Second
	}
	if cfg.StopTimeout <= 0 {
		cfg.StopTimeout = 5 * time.Second
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 256
	}
	if cfg.MaxUDPTargets <= 0 {
		cfg.MaxUDPTargets = 64
	}
	return &Server{cfg: cfg, sessions: make(map[io.Closer]struct{})}, nil
}

func (s *Server) Start(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	if s.done != nil {
		done := s.done
		s.mu.Unlock()
		select {
		case <-done:
		case <-time.After(s.cfg.StopTimeout):
			return errors.New("previous SOCKS5 listener is still stopping")
		}
		s.mu.Lock()
	}
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("listen on local CSQTT SOCKS5 endpoint: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.listener, s.ctx, s.cancel, s.done, s.running = ln, ctx, cancel, done, true
	s.sessions = make(map[io.Closer]struct{})
	s.mu.Unlock()

	go s.acceptLoop(ctx, ln, done)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		s.closeSessions()
	}()
	return nil
}

func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) Stop() error {
	s.mu.Lock()
	done, cancel, ln := s.done, s.cancel, s.listener
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	s.closeSessions()
	select {
	case <-done:
		s.mu.Lock()
		if s.done == done {
			s.listener, s.ctx, s.cancel, s.done, s.running = nil, nil, nil, nil, false
		}
		s.mu.Unlock()
		return nil
	case <-time.After(s.cfg.StopTimeout):
		return errors.New("CSQTT SOCKS5 sessions did not stop before timeout")
	}
}

func (s *Server) acceptLoop(ctx context.Context, ln net.Listener, done chan struct{}) {
	defer func() {
		s.closeSessions()
		s.handlers.Wait()
		s.mu.Lock()
		if s.done == done {
			s.listener = nil
			s.running = false
		}
		s.mu.Unlock()
		close(done)
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log("SOCKS5 accept failed: %v", err)
			return
		}
		s.mu.Lock()
		if !s.running || ctx.Err() != nil || len(s.sessions) >= s.cfg.MaxSessions {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.sessions[conn] = struct{}{}
		s.handlers.Add(1)
		s.mu.Unlock()
		go func(c net.Conn) {
			defer s.handlers.Done()
			defer s.untrack(c)
			s.handleClient(ctx, c)
		}(conn)
	}
}

func (s *Server) register(c io.Closer) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.ctx == nil || s.ctx.Err() != nil {
		_ = c.Close()
		return false
	}
	s.sessions[c] = struct{}{}
	return true
}

func (s *Server) untrack(c io.Closer) {
	s.mu.Lock()
	delete(s.sessions, c)
	s.mu.Unlock()
}

func (s *Server) closeSessions() {
	s.mu.Lock()
	items := make([]io.Closer, 0, len(s.sessions))
	for c := range s.sessions {
		items = append(items, c)
	}
	s.mu.Unlock()
	for _, c := range items {
		_ = c.Close()
	}
}

func (s *Server) handleClient(serverCtx context.Context, client net.Conn) {
	_ = client.SetDeadline(time.Now().Add(20 * time.Second))
	var greeting [2]byte
	if _, err := io.ReadFull(client, greeting[:]); err != nil || greeting[0] != version5 || greeting[1] == 0 {
		return
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(client, methods); err != nil {
		return
	}
	noAuth := false
	for _, method := range methods {
		if method == methodNoAuth {
			noAuth = true
			break
		}
	}
	if !noAuth {
		_, _ = client.Write([]byte{version5, methodNoAccept})
		return
	}
	if _, err := client.Write([]byte{version5, methodNoAuth}); err != nil {
		return
	}
	var request [3]byte
	if _, err := io.ReadFull(client, request[:]); err != nil || request[0] != version5 || request[2] != 0 {
		return
	}
	host, port, err := readAddress(client)
	if err != nil {
		_ = writeReply(client, replyAddress, nil)
		return
	}
	_ = client.SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(serverCtx)
	defer cancel()

	switch request[1] {
	case cmdConnect:
		s.handleConnect(ctx, client, host, port)
	case cmdUDPAssociate:
		s.handleUDPAssociate(ctx, client)
	default:
		_ = writeReply(client, replyCommand, nil)
	}
}

func (s *Server) handleConnect(ctx context.Context, client net.Conn, host string, port uint16) {
	dialCtx, cancel := context.WithTimeout(ctx, s.cfg.DialTimeout)
	defer cancel()
	target, err := s.resolveTarget(dialCtx, host, port)
	if err != nil {
		s.log("SOCKS5 CONNECT resolve %q failed: %v", net.JoinHostPort(host, strconv.Itoa(int(port))), err)
		_ = writeReply(client, replyHost, nil)
		return
	}
	remote, err := s.markedDialer().DialContext(dialCtx, "tcp", target)
	if err != nil {
		s.log("SOCKS5 CONNECT dial %q failed: %v", target, err)
		_ = writeReply(client, mapDialError(err), nil)
		return
	}
	if !s.register(remote) {
		return
	}
	defer func() {
		s.untrack(remote)
		_ = remote.Close()
	}()
	if err := writeReply(client, replySucceeded, remote.LocalAddr()); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(remote, client)
		if tcp, ok := remote.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, remote)
		if tcp, ok := client.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (s *Server) handleUDPAssociate(ctx context.Context, control net.Conn) {
	peer, ok := control.RemoteAddr().(*net.TCPAddr)
	if !ok || peer.IP == nil || !peer.IP.IsLoopback() {
		_ = writeReply(control, replyNotAllowed, nil)
		return
	}
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: peer.IP, Port: 0})
	if err != nil {
		_ = writeReply(control, replyGeneral, nil)
		return
	}
	if !s.register(relay) {
		return
	}
	defer func() {
		s.untrack(relay)
		_ = relay.Close()
	}()
	if err := writeReply(control, replySucceeded, relay.LocalAddr()); err != nil {
		return
	}

	assocCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, control)
		cancel()
	}()

	association := &udpAssociation{
		server: s,
		ctx: assocCtx,
		relay: relay,
		peerIP: peer.IP,
		targets: make(map[string]*udpTarget),
	}
	defer association.close()

	buf := make([]byte, 65535)
	for {
		_ = relay.SetReadDeadline(time.Now().Add(time.Second))
		n, source, readErr := relay.ReadFromUDP(buf)
		if readErr != nil {
			if assocCtx.Err() != nil {
				return
			}
			if timeout, ok := readErr.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return
		}
		if !source.IP.Equal(peer.IP) {
			continue
		}
		if err := association.acceptClient(source); err != nil {
			continue
		}
		host, port, payload, err := parseUDPDatagram(buf[:n])
		if err != nil {
			s.log("SOCKS5 UDP datagram rejected: %v", err)
			continue
		}
		target, err := association.target(host, port)
		if err != nil {
			s.log("SOCKS5 UDP target %q:%d failed: %v", host, port, err)
			continue
		}
		if _, err := target.conn.WriteToUDP(payload, target.addr); err != nil {
			s.log("SOCKS5 UDP send to %s failed: %v", target.addr, err)
		}
	}
}

type udpTarget struct {
	conn *net.UDPConn
	addr *net.UDPAddr
}

type udpAssociation struct {
	server *Server
	ctx context.Context
	relay *net.UDPConn
	peerIP net.IP

	mu sync.Mutex
	client *net.UDPAddr
	targets map[string]*udpTarget
	wg sync.WaitGroup
}

func (a *udpAssociation) acceptClient(source *net.UDPAddr) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		a.client = cloneUDPAddr(source)
		return nil
	}
	if !a.client.IP.Equal(source.IP) || a.client.Port != source.Port {
		return errors.New("UDP client address changed during association")
	}
	return nil
}

func (a *udpAssociation) target(host string, port uint16) (*udpTarget, error) {
	ctx, cancel := context.WithTimeout(a.ctx, a.server.cfg.ResolveTimeout)
	defer cancel()
	target, err := a.server.resolveUDPAddr(ctx, host, port)
	if err != nil {
		return nil, err
	}
	key := target.String()
	a.mu.Lock()
	if existing := a.targets[key]; existing != nil {
		a.mu.Unlock()
		return existing, nil
	}
	if len(a.targets) >= a.server.cfg.MaxUDPTargets {
		a.mu.Unlock()
		return nil, errors.New("UDP association destination limit reached")
	}
	network, local := "udp4", "0.0.0.0:0"
	if target.IP.To4() == nil {
		network, local = "udp6", "[::]:0"
	}
	pc, err := (net.ListenConfig{Control: a.server.socketControl}).ListenPacket(a.ctx, network, local)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()
		a.mu.Unlock()
		return nil, errors.New("marked UDP socket has unexpected type")
	}
	if !a.server.register(conn) {
		a.mu.Unlock()
		return nil, context.Canceled
	}
	entry := &udpTarget{conn: conn, addr: target}
	a.targets[key] = entry
	client := cloneUDPAddr(a.client)
	a.wg.Add(1)
	a.mu.Unlock()
	go a.readReplies(entry, client)
	return entry, nil
}

func (a *udpAssociation) readReplies(target *udpTarget, client *net.UDPAddr) {
	defer a.wg.Done()
	buf := make([]byte, 65535)
	for {
		_ = target.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, source, err := target.conn.ReadFromUDP(buf)
		if err != nil {
			if a.ctx.Err() != nil {
				return
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return
		}
		packet, err := buildUDPDatagram(source, buf[:n])
		if err != nil {
			continue
		}
		if _, err := a.relay.WriteToUDP(packet, client); err != nil && a.ctx.Err() == nil {
			a.server.log("SOCKS5 UDP reply to %s failed: %v", client, err)
		}
	}
}

func (a *udpAssociation) close() {
	a.mu.Lock()
	targets := make([]*udpTarget, 0, len(a.targets))
	for _, target := range a.targets {
		targets = append(targets, target)
	}
	a.mu.Unlock()
	for _, target := range targets {
		a.server.untrack(target.conn)
		_ = target.conn.Close()
	}
	a.wg.Wait()
}

func (s *Server) resolveTarget(ctx context.Context, host string, port uint16) (string, error) {
	ip := net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	if ip != nil {
		return net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), nil
	}
	ips, err := s.lookupIP(ctx, host)
	if err != nil {
		return "", err
	}
	for _, candidate := range ips {
		if candidate.IP == nil {
			continue
		}
		return net.JoinHostPort(candidate.IP.String(), strconv.Itoa(int(port))), nil
	}
	return "", errors.New("hostname resolved to no usable addresses")
}

func (s *Server) resolveUDPAddr(ctx context.Context, host string, port uint16) (*net.UDPAddr, error) {
	ip := net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	if ip != nil {
		return &net.UDPAddr{IP: ip, Port: int(port)}, nil
	}
	ips, err := s.lookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, candidate := range ips {
		if candidate.IP != nil {
			return &net.UDPAddr{IP: candidate.IP, Port: int(port)}, nil
		}
	}
	return nil, errors.New("hostname resolved to no usable addresses")
}

func (s *Server) lookupIP(ctx context.Context, host string) ([]net.IPAddr, error) {
	if s.cfg.Resolve != nil {
		return s.cfg.Resolve(ctx, host)
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return s.markedDialer().DialContext(ctx, network, address)
		},
	}
	return resolver.LookupIPAddr(ctx, host)
}

func (s *Server) markedDialer() *net.Dialer {
	return &net.Dialer{Timeout: s.cfg.DialTimeout, Control: s.socketControl}
}

func (s *Server) socketControl(_ string, _ string, raw syscall.RawConn) error {
	if s.cfg.Mark == 0 {
		return nil
	}
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		socketErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soMarkOption, s.cfg.Mark)
	}); err != nil {
		return err
	}
	return socketErr
}

func (s *Server) log(format string, args ...any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger(format, args...)
	}
}

func mapDialError(err error) byte {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return replyRefused
		}
		if errors.Is(opErr.Err, syscall.ENETUNREACH) {
			return replyNetwork
		}
		if errors.Is(opErr.Err, syscall.EHOSTUNREACH) {
			return replyHost
		}
	}
	return replyGeneral
}

func readAddress(r io.Reader) (string, uint16, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return "", 0, err
	}
	host := ""
	switch atyp[0] {
	case atypIPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case atypIPv6:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case atypDomain:
		var size [1]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return "", 0, err
		}
		if size[0] == 0 {
			return "", 0, errors.New("empty SOCKS5 domain")
		}
		b := make([]byte, int(size[0]))
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, errors.New("unsupported SOCKS5 address type")
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(r, portBytes[:]); err != nil {
		return "", 0, err
	}
	port := binary.BigEndian.Uint16(portBytes[:])
	if port == 0 {
		return "", 0, errors.New("SOCKS5 destination port must be non-zero")
	}
	return host, port, nil
}

func parseUDPDatagram(packet []byte) (string, uint16, []byte, error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 {
		return "", 0, nil, errors.New("invalid UDP RSV header")
	}
	if packet[2] != 0 {
		return "", 0, nil, errors.New("fragmented SOCKS5 UDP datagrams are not supported")
	}
	reader := strings.NewReader(string(packet[3:]))
	host, port, err := readAddress(reader)
	if err != nil {
		return "", 0, nil, err
	}
	offset := len(packet) - reader.Len()
	if offset > len(packet) {
		return "", 0, nil, errors.New("invalid UDP address length")
	}
	return host, port, packet[offset:], nil
}

func buildUDPDatagram(source *net.UDPAddr, payload []byte) ([]byte, error) {
	if source == nil || source.IP == nil {
		return nil, errors.New("UDP reply has no source IP")
	}
	address, err := encodeAddress(source.IP.String(), uint16(source.Port))
	if err != nil {
		return nil, err
	}
	packet := make([]byte, 3, 3+len(address)+len(payload))
	packet[0], packet[1], packet[2] = 0, 0, 0
	packet = append(packet, address...)
	packet = append(packet, payload...)
	return packet, nil
}

func encodeAddress(host string, port uint16) ([]byte, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		if len(host) == 0 || len(host) > 255 {
			return nil, errors.New("invalid SOCKS5 domain length")
		}
		out := []byte{atypDomain, byte(len(host))}
		out = append(out, host...)
		var p [2]byte
		binary.BigEndian.PutUint16(p[:], port)
		return append(out, p[:]...), nil
	}
	var out []byte
	if v4 := ip.To4(); v4 != nil {
		out = append([]byte{atypIPv4}, v4...)
	} else {
		out = append([]byte{atypIPv6}, ip.To16()...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], port)
	return append(out, p[:]...), nil
}

func writeReply(w io.Writer, code byte, bind net.Addr) error {
	host, port := "0.0.0.0", uint16(0)
	if bind != nil {
		switch addr := bind.(type) {
		case *net.TCPAddr:
			if addr.IP != nil {
				host = addr.IP.String()
			}
			port = uint16(addr.Port)
		case *net.UDPAddr:
			if addr.IP != nil {
				host = addr.IP.String()
			}
			port = uint16(addr.Port)
		}
	}
	address, err := encodeAddress(host, port)
	if err != nil {
		return err
	}
	reply := []byte{version5, code, 0}
	reply = append(reply, address...)
	_, err = w.Write(reply)
	return err
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}
	return &net.UDPAddr{IP: append(net.IP(nil), addr.IP...), Port: addr.Port, Zone: addr.Zone}
}

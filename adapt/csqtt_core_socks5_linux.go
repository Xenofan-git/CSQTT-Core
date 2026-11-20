//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"
)


const laluneSocksMark = 0x4d53

var errSocksAddressUnavailable = errors.New("SOCKS5 downstream address unavailable")

// The unauthenticated SOCKS5 service is reachable only on the downstream
// LAN interface. Resolve its current IPv4 address at startup; never bind :12500
// or 0.0.0.0 because this service intentionally has no password.
func laluneSocks5ListenAddr() (string, error) {
	iface, err := net.InterfaceByName("eth1")
	if err != nil { return "", fmt.Errorf("%w: downstream LAN interface eth1 is unavailable", errSocksAddressUnavailable) }
	addrs, err := iface.Addrs()
	if err != nil { return "", err }
	return socks5ListenAddrFromAddrs(addrs)
}

func socks5ListenAddrFromAddrs(addrs []net.Addr) (string, error) {
	var selected net.IP
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil { continue }
		ip = ip.To4()
		if ip == nil || !ip.IsPrivate() || ip.IsUnspecified() || ip.IsLoopback() { continue }
		if selected != nil && !selected.Equal(ip) {
			return "", errors.New("eth1 has multiple private IPv4 addresses; refusing ambiguous unauthenticated SOCKS5 bind")
		}
		selected = append(net.IP(nil), ip...)
	}
	if selected == nil {
		return "", fmt.Errorf("%w: eth1 has no private IPv4 address; refusing to expose unauthenticated SOCKS5", errSocksAddressUnavailable)
	}
	return net.JoinHostPort(selected.String(), "12500"), nil
}

type Socks5Server struct {
	mu sync.Mutex
	addr string
	ln net.Listener
	stop chan struct{}
	done chan struct{}
	resolveAddr func() (string, error)
	pollInterval time.Duration
	stopped bool
	active map[net.Conn]struct{}
	handlers sync.WaitGroup

	dialTCPOverride func(string) (net.Conn, error)
	resolveTCPOverride func(context.Context, string) (string, error)
	dialUDPOverride func() (*net.UDPConn, error)
	tracef func(string, ...any)
}

func NewSocks5Server(addr string) *Socks5Server {
	return &Socks5Server{addr: addr, active: make(map[net.Conn]struct{})}
}

// Socks5Status is a read-only snapshot of the actual listener, not desired configuration.
type Socks5Status struct {
	Interface string `json:"interface"`
	Protocol string `json:"protocol"`
	Auth string `json:"auth"`
	Monitoring bool `json:"monitoring"`
	Listening bool `json:"listening"`
	Address string `json:"address"`
}

func (s *Socks5Server) Status() Socks5Status {
	status := Socks5Status{Interface: "eth1", Protocol: "SOCKS5", Auth: "no-auth"}
	if s == nil { return status }
	s.mu.Lock()
	defer s.mu.Unlock()
	status.Monitoring = s.stop != nil && !s.stopped
	if s.ln != nil {
		status.Listening = true
		status.Address = s.ln.Addr().String()
	}
	return status
}

func (s *Socks5Server) listenAddress() (string, error) {
	if s.resolveAddr != nil { return s.resolveAddr() }
	if s.addr == "eth1:12500" { return laluneSocks5ListenAddr() }
	return s.addr, nil
}

// publishListener serializes listener publication with Stop. A listener created
// while shutdown is in progress is closed instead of becoming reachable.
func (s *Socks5Server) publishListener(ln net.Listener, stop <-chan struct{}) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.stop == nil || s.stop != stop {
		_ = ln.Close()
		return false
	}
	old := s.ln
	s.ln = ln
	if old != nil { _ = old.Close() }
	s.handlers.Add(1)
	go s.acceptLoop(ln, stop)
	return true
}

func (s *Socks5Server) acceptLoop(ln net.Listener, stop <-chan struct{}) {
	defer s.handlers.Done()
	for {
		conn, err := ln.Accept()
		if err != nil { return }
		s.mu.Lock()
		if s.stopped || s.stop != stop {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.active[conn] = struct{}{}
		s.handlers.Add(1)
		s.mu.Unlock()
		go func(c net.Conn) {
			defer s.handlers.Done()
			defer func() {
				s.mu.Lock()
				delete(s.active, c)
				s.mu.Unlock()
			}()
			s.handle(c)
		}(conn)
	}
}

func (s *Socks5Server) closeActiveSessions() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.active))
	for conn := range s.active { conns = append(conns, conn) }
	s.mu.Unlock()
	for _, conn := range conns { _ = conn.Close() }
}

func (s *Socks5Server) trackConn(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped { _ = conn.Close(); return false }
	s.active[conn] = struct{}{}
	return true
}

func (s *Socks5Server) untrackConn(conn net.Conn) {
	s.mu.Lock()
	delete(s.active, conn)
	s.mu.Unlock()
}

func (s *Socks5Server) bindCurrent(stop <-chan struct{}) error {
	addr, err := s.listenAddress()
	if err != nil { return err }
	ln, err := net.Listen("tcp", addr)
	if err != nil { return err }
	if !s.publishListener(ln, stop) { return errors.New("SOCKS5 server stopped during bind") }
	return nil
}

func (s *Socks5Server) Start() error {
	s.mu.Lock()
	if !s.stopped && s.stop != nil { s.mu.Unlock(); return nil }
	stop, done := make(chan struct{}), make(chan struct{})
	s.stop, s.done, s.stopped = stop, done, false
	s.mu.Unlock()

	// A missing downstream address is expected during boot and DHCP renewal.
	// A resolved address that cannot be bound is not: surface that error so
	// systemd does not report a healthy SOCKS service with no listening socket.
	initialAddr, resolveErr := s.listenAddress()
	if resolveErr != nil && !errors.Is(resolveErr, errSocksAddressUnavailable) {
		s.mu.Lock(); s.stopped = true; s.stop, s.done = nil, nil; s.mu.Unlock()
		close(stop); close(done)
		return fmt.Errorf("resolve SOCKS5 bind address: %w", resolveErr)
	}
	if resolveErr == nil {
		ln, listenErr := net.Listen("tcp", initialAddr)
		if listenErr != nil {
			s.mu.Lock()
			s.stopped = true
			s.stop, s.done = nil, nil
			s.mu.Unlock()
			close(stop)
			close(done)
			return fmt.Errorf("bind SOCKS5 listener %s: %w", initialAddr, listenErr)
		}
		if !s.publishListener(ln, stop) {
			close(stop)
			close(done)
			return errors.New("SOCKS5 server stopped during initial bind")
		}
	}
	go func() {
		defer close(done)
		interval := s.pollInterval
		if interval <= 0 { interval = 5 * time.Second }
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop: return
			case <-ticker.C:
				addr, err := s.listenAddress()
				s.mu.Lock()
				if s.stopped || s.stop != stop { s.mu.Unlock(); return }
				current := ""
				if s.ln != nil { current = s.ln.Addr().String() }
				if err == nil && current == addr { s.mu.Unlock(); continue }
				old := s.ln
				s.ln = nil
				s.mu.Unlock()
				if old != nil { _ = old.Close() }
				// An address transition invalidates sessions accepted on the old
				// downstream address. Close both client and upstream sockets.
				s.closeActiveSessions()
				if err == nil { _ = s.bindCurrent(stop) }
			}
		}
	}()
	return nil
}

func (s *Socks5Server) Stop() {
	s.mu.Lock()
	if s.stop == nil {
		s.stopped = true
		s.mu.Unlock()
		return
	}
	stop, done, ln := s.stop, s.done, s.ln
	s.stopped = true
	s.stop, s.done, s.ln = nil, nil, nil
	conns := make([]net.Conn, 0, len(s.active))
	for conn := range s.active { conns = append(conns, conn) }
	s.mu.Unlock()

	close(stop)
	if ln != nil { _ = ln.Close() }
	for _, conn := range conns { _ = conn.Close() }
	if done != nil { <-done }
	// No further handler/accept-loop Add calls are possible after stopped=true
	// and the listener is closed; wait for all accepted sessions to unwind.
	s.handlers.Wait()
}

func laluneDialer(network string) net.Dialer {
	return net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(_, _ string, rc syscall.RawConn) error {
			var setErr error
			if err := rc.Control(func(fd uintptr) {
				setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, laluneSocksMark)
			}); err != nil {
				return err
			}
			return setErr
		},
	}
}

func (s *Socks5Server) trace(format string, args ...any) { if s.tracef != nil { s.tracef(format, args...) } }

func (s *Socks5Server) dialTCP(addr string) (net.Conn, error) {
	if s.dialTCPOverride != nil { return s.dialTCPOverride(addr) }
	if _, err := net.InterfaceByName("csqtt0"); err != nil {
		return nil, errors.New("LaLune TUN is not active")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resolved := addr
	var err error
	if s.resolveTCPOverride != nil {
		resolved, err = s.resolveTCPOverride(ctx, addr)
	} else {
		resolved, err = resolveTCPAddrMarked(ctx, addr)
	}
	if err != nil { s.trace("TCP resolve target=%q failed: %v", addr, err); return nil, err }
	s.trace("TCP resolved target=%q to=%q", addr, resolved)
	tcpDialer := laluneDialer("tcp")
	conn, dialErr := tcpDialer.DialContext(ctx, "tcp", resolved)
	if dialErr != nil { s.trace("TCP marked dial target=%q failed: %v", resolved, dialErr) } else { s.trace("TCP marked dial target=%q connected local=%s remote=%s", resolved, conn.LocalAddr(), conn.RemoteAddr()) }
	return conn, dialErr
}

// resolveTCPAddrMarked performs hostname resolution through sockets carrying
// the same SO_MARK as the subsequent TCP connection. Resolving with the
// process default resolver here would leak DNS outside table 202.
func resolveTCPAddrMarked(ctx context.Context, target string) (string, error) {
	return resolveTCPAddrMarkedWithResolver(ctx, target, nil)
}

func resolveTCPAddrMarkedWithResolver(ctx context.Context, target string, resolver *net.Resolver) (string, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil { return "", err }
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 { return "", errors.New("invalid TCP target port") }
	if ip := net.ParseIP(host); ip != nil { return net.JoinHostPort(ip.String(), portText), nil }
	if resolver == nil {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				markedDialer := laluneDialer(network)
				return markedDialer.DialContext(ctx, network, address)
			},
		}
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil { return "", err }
	for _, candidate := range ips {
		if ip4 := candidate.IP.To4(); ip4 != nil { return net.JoinHostPort(ip4.String(), portText), nil }
	}
	for _, candidate := range ips {
		if ip6 := candidate.IP.To16(); ip6 != nil { return net.JoinHostPort(ip6.String(), portText), nil }
	}
	return "", errors.New("TCP target hostname resolved to no usable addresses")
}

func (s *Socks5Server) dialUDP() (*net.UDPConn, error) {
	return s.dialUDPFor(net.IPv4zero)
}

func udpNetworkForIP(destination net.IP) (network, local string) {
	if destination.To4() == nil { return "udp6", "[::]:0" }
	return "udp4", "0.0.0.0:0"
}

func (s *Socks5Server) dialUDPFor(destination net.IP) (*net.UDPConn, error) {
	if s.dialUDPOverride != nil { return s.dialUDPOverride() }
	if _, err := net.InterfaceByName("csqtt0"); err != nil {
		return nil, errors.New("LaLune TUN is not active")
	}
	network, local := udpNetworkForIP(destination)
	lc := net.ListenConfig{Control: laluneDialer(network).Control}
	pc, err := lc.ListenPacket(context.Background(), network, local)
	if err != nil { return nil, err }
	return pc.(*net.UDPConn), nil
}

func readSocksAddr(r io.Reader) (string, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil { return "", err }
	var host string
	switch t[0] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil { return "", err }
		host = net.IP(b).String()
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil { return "", err }
		b := make([]byte, int(n[0]))
		if _, err := io.ReadFull(r, b); err != nil { return "", err }
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil { return "", err }
		host = net.IP(b).String()
	default:
		return "", errors.New("unsupported SOCKS address type")
	}
	var p [2]byte
	if _, err := io.ReadFull(r, p[:]); err != nil { return "", err }
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(p[:])))), nil
}

func writeSocksReply(c net.Conn, code byte, bind net.Addr) error {
	ip := net.IPv4zero
	port := 0
	switch a := bind.(type) {
	case *net.TCPAddr:
		ip, port = a.IP, a.Port
	case *net.UDPAddr:
		ip, port = a.IP, a.Port
	}
	if ip == nil || ip.IsUnspecified() {
		if local, ok := c.LocalAddr().(*net.TCPAddr); ok { ip = local.IP }
	}
	if v4 := ip.To4(); v4 != nil {
		b := []byte{5, code, 0, 1, v4[0], v4[1], v4[2], v4[3], 0, 0}
		binary.BigEndian.PutUint16(b[8:], uint16(port))
		_, err := c.Write(b)
		return err
	}
	if v6 := ip.To16(); v6 != nil {
		b := []byte{5, code, 0, 4}
		b = append(b, v6...)
		b = append(b, 0, 0)
		binary.BigEndian.PutUint16(b[len(b)-2:], uint16(port))
		_, err := c.Write(b)
		return err
	}
	return errors.New("cannot determine SOCKS5 reply address")
}

func (s *Socks5Server) handle(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(20 * time.Second))

	var h [2]byte
	if _, err := io.ReadFull(c, h[:]); err != nil || h[0] != 5 { return }
	methods := make([]byte, int(h[1]))
	if _, err := io.ReadFull(c, methods); err != nil { return }
	noAuth := false
	for _, method := range methods {
		if method == 0 { noAuth = true; break }
	}
	if !noAuth {
		_, _ = c.Write([]byte{5, 0xff})
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil { return }

	var req [3]byte
	if _, err := io.ReadFull(c, req[:]); err != nil || req[0] != 5 { return }

	switch req[1] {
	case 1:
		s.handleConnect(c)
	case 3:
		s.handleUDPAssociate(c)
	default:
		_ = writeSocksReply(c, 7, &net.TCPAddr{})
	}
}

func (s *Socks5Server) handleConnect(c net.Conn) {
	addr, err := readSocksAddr(c)
	if err != nil { _ = writeSocksReply(c, 8, &net.TCPAddr{}); return }
	out, err := s.dialTCP(addr)
	if err != nil { s.trace("SOCKS CONNECT target=%q failed: %v", addr, err); _ = writeSocksReply(c, 1, &net.TCPAddr{}); return }
	if !s.trackConn(out) { return }
	defer func() { s.untrackConn(out); _ = out.Close() }()

	c.SetDeadline(time.Time{})
	if err := writeSocksReply(c, 0, out.LocalAddr()); err != nil { return }

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(out, c)
		if tcp, ok := out.(*net.TCPConn); ok { _ = tcp.CloseWrite() }
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(c, out)
		if tcp, ok := c.(*net.TCPConn); ok { _ = tcp.CloseWrite() }
		done <- struct{}{}
	}()
	// Wait for both directions so a half-close does not leak the reverse copier.
	<-done
	if tcp, ok := out.(*net.TCPConn); ok { _ = tcp.CloseRead() }
	if tcp, ok := c.(*net.TCPConn); ok { _ = tcp.CloseRead() }
	<-done
}

func (s *Socks5Server) handleUDPAssociate(c net.Conn) {
	bindIP := net.IPv4zero
	if local, ok := c.LocalAddr().(*net.TCPAddr); ok && local.IP != nil && !local.IP.IsUnspecified() {
		bindIP = local.IP
	}
	u, err := net.ListenUDP("udp", &net.UDPAddr{IP: bindIP, Port: 0})
	if err != nil { _ = writeSocksReply(c, 1, &net.TCPAddr{}); return }
	if !s.trackConn(u) { return }
	defer func() { s.untrackConn(u); _ = u.Close() }()
	if err := writeSocksReply(c, 0, u.LocalAddr()); err != nil { return }
	c.SetDeadline(time.Time{})

	// RFC 1928 ties the relay lifetime to its TCP control connection.
	go func() { _, _ = io.Copy(io.Discard, c); _ = u.Close() }()

	// A single association may carry independent UDP requests. Process them
	// concurrently, but cap in-flight upstream sockets to bound memory/fds.
	const maxInflight = 64
	sem := make(chan struct{}, maxInflight)
	var workers sync.WaitGroup
	defer workers.Wait()
	buf := make([]byte, 65535)
	var client *net.UDPAddr
	for {
		_ = u.SetReadDeadline(time.Now().Add(12 * time.Hour))
		n, src, readErr := u.ReadFromUDP(buf)
		if readErr != nil { return }
		if client == nil { client = cloneUDPAddr(src) }
		if !src.IP.Equal(client.IP) || src.Port != client.Port { continue }
		if n < 7 || buf[0] != 0 || buf[1] != 0 || buf[2] != 0 { s.trace("UDP relay dropped malformed datagram bytes=%d", n); continue }
		target, off, parseErr := readUDPAddr(buf[:n], 3)
		if parseErr != nil { s.trace("UDP relay address parse failed: %v", parseErr); continue }
		s.trace("UDP relay received target=%q bytes=%d from=%s", target, n-off, src)
		payload := append([]byte(nil), buf[off:n]...)
		select {
		case sem <- struct{}{}:
		default:
			// Drop rather than blocking the association reader when saturated.
			continue
		}
		workers.Add(1)
		go func(target string, payload []byte, client *net.UDPAddr) {
			defer workers.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dst, resolveErr := resolveUDPAddrMarkedContext(ctx, target)
			if resolveErr != nil { s.trace("UDP resolve target=%q failed: %v", target, resolveErr); return }
			s.trace("UDP resolved target=%q to=%s", target, dst)
			out, dialErr := s.dialUDPFor(dst.IP)
			if dialErr != nil { s.trace("UDP marked socket create target=%s failed: %v", dst, dialErr); return }
			s.trace("UDP marked socket created local=%s target=%s", out.LocalAddr(), dst)
			if !s.trackConn(out) { return }
			defer func() { s.untrackConn(out); _ = out.Close() }()
			_ = out.SetDeadline(time.Now().Add(5 * time.Second))
			if _, writeErr := out.WriteToUDP(payload, dst); writeErr != nil { s.trace("UDP marked send target=%s failed: %v", dst, writeErr); return }
			s.trace("UDP marked packet sent target=%s bytes=%d", dst, len(payload))
			replyBuf := make([]byte, 65535)
			rn, ra, replyErr := out.ReadFromUDP(replyBuf)
			if replyErr != nil { s.trace("UDP marked receive target=%s failed: %v", dst, replyErr); return }
			s.trace("UDP marked reply from=%s bytes=%d", ra, rn)
			packet, buildErr := buildUDPReply(ra, replyBuf[:rn])
			if buildErr != nil { s.trace("UDP reply build from=%s failed: %v", ra, buildErr); return }
			if _, relayErr := u.WriteToUDP(packet, client); relayErr != nil { s.trace("UDP relay reply to=%s failed: %v", client, relayErr) } else { s.trace("UDP relay reply delivered to=%s bytes=%d", client, len(packet)) }
		}(target, payload, cloneUDPAddr(client))
	}
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil { return nil }
	return &net.UDPAddr{IP: append(net.IP(nil), addr.IP...), Port: addr.Port, Zone: addr.Zone}
}

func readUDPAddr(b []byte, off int) (string, int, error) {
	if len(b) <= off { return "", 0, io.ErrUnexpectedEOF }
	t := b[off]; off++
	var host string
	switch t {
	case 1:
		if len(b) < off+4 { return "", 0, io.ErrUnexpectedEOF }
		host = net.IP(b[off:off+4]).String(); off += 4
	case 3:
		if len(b) < off+1 { return "", 0, io.ErrUnexpectedEOF }
		n := int(b[off]); off++
		if len(b) < off+n { return "", 0, io.ErrUnexpectedEOF }
		host = string(b[off:off+n]); off += n
	case 4:
		if len(b) < off+16 { return "", 0, io.ErrUnexpectedEOF }
		host = net.IP(b[off:off+16]).String(); off += 16
	default:
		return "", 0, errors.New("unsupported UDP address type")
	}
	if len(b) < off+2 { return "", 0, io.ErrUnexpectedEOF }
	port := int(binary.BigEndian.Uint16(b[off : off+2]))
	return net.JoinHostPort(host, strconv.Itoa(port)), off + 2, nil
}

func buildUDPReply(addr *net.UDPAddr, payload []byte) ([]byte, error) {
	if addr == nil || addr.Port < 0 || addr.Port > 65535 {
		return nil, errors.New("invalid UDP source address")
	}
	var header []byte
	if ip4 := addr.IP.To4(); ip4 != nil {
		header = []byte{0, 0, 0, 1, ip4[0], ip4[1], ip4[2], ip4[3], 0, 0}
	} else if ip6 := addr.IP.To16(); ip6 != nil {
		header = []byte{0, 0, 0, 4}
		header = append(header, ip6...)
		header = append(header, 0, 0)
	} else {
		return nil, errors.New("invalid UDP source IP")
	}
	binary.BigEndian.PutUint16(header[len(header)-2:], uint16(addr.Port))
	return append(header, payload...), nil
}

func resolveUDPAddrMarked(target string) (*net.UDPAddr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return resolveUDPAddrMarkedContext(ctx, target)
}

func resolveUDPAddrMarkedContext(ctx context.Context, target string) (*net.UDPAddr, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil { return nil, err }
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 { return nil, errors.New("invalid UDP target port") }
	if ip := net.ParseIP(host); ip != nil { return &net.UDPAddr{IP: ip, Port: port}, nil }
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			markedDialer := laluneDialer(network)
			return markedDialer.DialContext(ctx, network, address)
		},
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil { return nil, err }
	for _, candidate := range ips {
		if ip4 := candidate.IP.To4(); ip4 != nil { return &net.UDPAddr{IP: ip4, Port: port}, nil }
	}
	for _, candidate := range ips {
		if ip6 := candidate.IP.To16(); ip6 != nil { return &net.UDPAddr{IP: ip6, Port: port}, nil }
	}
	return nil, errors.New("UDP target hostname resolved to no usable addresses")
}

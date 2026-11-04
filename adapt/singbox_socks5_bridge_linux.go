//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// Socks5Bridge exposes the existing eth1-bound, unauthenticated CSQTT SOCKS5
// listener on loopback for sing-box. It forwards the SOCKS byte stream without
// parsing or rewriting it, preserving domain names and UDP ASSOCIATE replies.
type Socks5Bridge struct {
	listenAddr string
	resolveUpstream func() (string, error)
	dialTimeout time.Duration

	mu sync.Mutex
	ln net.Listener
	closed bool
	active map[net.Conn]struct{}
	wg sync.WaitGroup
}

func NewSocks5Bridge(listenAddr string) *Socks5Bridge {
	return &Socks5Bridge{
		listenAddr: listenAddr,
		resolveUpstream: laluneSocks5ListenAddr,
		dialTimeout: 5 * time.Second,
		active: make(map[net.Conn]struct{}),
	}
}

func (b *Socks5Bridge) Start() error {
	host, portText, err := net.SplitHostPort(b.listenAddr)
	if err != nil { return fmt.Errorf("invalid SOCKS5 bridge listen address: %w", err) }
	ip := net.ParseIP(host)
	port, portErr := strconv.Atoi(portText)
	if ip == nil || !ip.IsLoopback() || portErr != nil || port < 1 || port > 65535 {
		return errors.New("SOCKS5 bridge must bind to a specific loopback IP and nonzero port")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed { return errors.New("SOCKS5 bridge is stopped") }
	if b.ln != nil { return nil }
	ln, err := net.Listen("tcp", b.listenAddr)
	if err != nil { return fmt.Errorf("bind SOCKS5 bridge %s: %w", b.listenAddr, err) }
	b.ln = ln
	b.wg.Add(1)
	go b.acceptLoop(ln)
	return nil
}

func (b *Socks5Bridge) acceptLoop(ln net.Listener) {
	defer b.wg.Done()
	for {
		client, err := ln.Accept()
		if err != nil { return }
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			_ = client.Close()
			return
		}
		b.active[client] = struct{}{}
		b.wg.Add(1)
		b.mu.Unlock()
		go b.handle(client)
	}
}

func (b *Socks5Bridge) handle(client net.Conn) {
	defer b.wg.Done()
	defer func() {
		b.mu.Lock()
		delete(b.active, client)
		b.mu.Unlock()
		_ = client.Close()
	}()
	resolve := b.resolveUpstream
	if resolve == nil { resolve = laluneSocks5ListenAddr }
	upstreamAddr, err := resolve()
	if err != nil { return }
	timeout := b.dialTimeout
	if timeout <= 0 { timeout = 5 * time.Second }
	upstream, err := net.DialTimeout("tcp", upstreamAddr, timeout)
	if err != nil { return }
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = upstream.Close()
		return
	}
	b.active[upstream] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.active, upstream)
		b.mu.Unlock()
		_ = upstream.Close()
	}()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		if c, ok := upstream.(*net.TCPConn); ok { _ = c.CloseWrite() }
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		if c, ok := client.(*net.TCPConn); ok { _ = c.CloseWrite() }
		done <- struct{}{}
	}()
	<-done
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	_ = upstream.SetReadDeadline(time.Now().Add(time.Second))
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

func (b *Socks5Bridge) Stop() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		b.wg.Wait()
		return
	}
	b.closed = true
	ln := b.ln
	b.ln = nil
	conns := make([]net.Conn, 0, len(b.active))
	for conn := range b.active { conns = append(conns, conn) }
	b.mu.Unlock()
	if ln != nil { _ = ln.Close() }
	for _, conn := range conns { _ = conn.Close() }
	b.wg.Wait()
}

func (b *Socks5Bridge) Addr() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ln == nil { return "" }
	return b.ln.Addr().String()
}

// Package testproxy supplies an echo-only loopback SOCKS server for offline
// tests. It never dials the requested destination or changes network settings.
package testproxy

import (
	"accelerator/engine/internal/socks"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	listener        net.Listener
	mu              sync.Mutex
	conns           map[net.Conn]bool
	closed          bool
	wg              sync.WaitGroup
	udpAssociations atomic.Uint64
	tcpResponder    func(net.Conn)
}

func Start() (*Server, error) {
	return StartTCPResponder(nil)
}

// StartTCPResponder substitutes only the echo payload handler. Like Start,
// this server never dials the SOCKS destination or opens a physical interface.
func StartTCPResponder(respond func(net.Conn)) (*Server, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{listener: l, conns: make(map[net.Conn]bool), tcpResponder: respond}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				c.Close()
				return
			}
			s.conns[c] = true
			s.wg.Add(1)
			s.mu.Unlock()
			go func() {
				defer s.wg.Done()
				defer func() {
					c.Close()
					s.mu.Lock()
					delete(s.conns, c)
					s.mu.Unlock()
				}()
				s.serve(c)
			}()
		}
	}()
	return s, nil
}

func (s *Server) Address() string { return s.listener.Addr().String() }

func (s *Server) UDPAssociations() uint64 { return s.udpAssociations.Load() }

func (s *Server) serve(c net.Conn) {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var greeting [3]byte
	if _, err := io.ReadFull(c, greeting[:]); err != nil || greeting != [3]byte{5, 1, 0} {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}
	var request [3]byte
	if _, err := io.ReadFull(c, request[:]); err != nil {
		return
	}
	if _, err := socks.ReadAddress(c); err != nil {
		return
	}
	if request[1] == 1 {
		c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		c.SetDeadline(time.Time{})
		if s.tcpResponder != nil {
			s.tcpResponder(c)
		} else {
			io.Copy(c, c)
		}
		return
	}
	if request[1] != 3 {
		return
	}
	s.udpAssociations.Add(1)
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return
	}
	defer udp.Close()
	port := udp.LocalAddr().(*net.UDPAddr).Port
	reply := []byte{5, 0, 0, 1, 127, 0, 0, 1}
	reply = binary.BigEndian.AppendUint16(reply, uint16(port))
	c.Write(reply)
	c.SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65535)
		for {
			n, from, err := udp.ReadFromUDP(buf)
			if err != nil || ctx.Err() != nil {
				return
			}
			if _, _, err := socks.DecodeDatagram(buf[:n]); err == nil {
				udp.WriteToUDP(buf[:n], from)
			}
		}
	}()
	io.Copy(io.Discard, c)
	cancel()
	udp.Close()
	<-done
}

func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.listener.Close()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

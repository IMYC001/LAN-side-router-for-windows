package relay

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"time"

	"accelerator/engine/internal/socks"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
)

type udpSource struct {
	Address netip.AddrPort
	DNS     bool
}

type udpSession struct {
	key     udpSource
	ready   chan struct{}
	err     error
	remote  *socks.UDPAssociation
	users   int
	stopped bool
	flows   map[netip.AddrPort]adapter.UDPConn
	writeMu sync.Mutex
}

func (s *udpSession) write(data []byte, destination netip.AddrPort) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.remote.SetDeadline(time.Now().Add(90 * time.Second)); err != nil {
		return err
	}
	return s.remote.WriteTo(data, destination.String())
}

// Reserve a user before waiting for setup so a fast sibling cannot close the
// shared socket while another destination for the same source is attaching.
func (e *Engine) acquireUDP(key udpSource) (*udpSession, error) {
	e.udpMu.Lock()
	session := e.udpSessions[key]
	creator := session == nil
	if creator {
		session = &udpSession{key: key, ready: make(chan struct{}), flows: make(map[netip.AddrPort]adapter.UDPConn)}
		e.udpSessions[key] = session
	}
	session.users++
	e.udpMu.Unlock()
	if creator {
		ctx, cancel := context.WithTimeout(e.ctx, 8*time.Second)
		remote, err := e.proxy.AssociateUDP(ctx)
		cancel()
		e.udpMu.Lock()
		session.remote = remote
		e.udpMu.Unlock()
		if err == nil {
			if !e.start(remote, func() { e.readUDP(session) }) {
				err = errors.New("UDP 会话已停止或资源不足")
			} else {
				e.udpAssociations.Add(1)
			}
		}
		e.udpMu.Lock()
		session.err = err
		if err != nil && e.udpSessions[key] == session {
			delete(e.udpSessions, key)
		}
		close(session.ready)
		e.udpMu.Unlock()
	}
	select {
	case <-e.ctx.Done():
		e.releaseUDP(session, netip.AddrPort{}, nil)
		return nil, e.ctx.Err()
	case <-session.ready:
		if session.err != nil {
			e.releaseUDP(session, netip.AddrPort{}, nil)
			return nil, session.err
		}
		return session, nil
	}
}

func (e *Engine) attachUDP(session *udpSession, destination netip.AddrPort, origin adapter.UDPConn) error {
	e.udpMu.Lock()
	defer e.udpMu.Unlock()
	if session.stopped || e.ctx.Err() != nil {
		return errors.New("UDP 会话已停止")
	}
	if _, exists := session.flows[destination]; exists {
		return errors.New("重复的 UDP 目标映射")
	}
	session.flows[destination] = origin
	return nil
}

func (e *Engine) releaseUDP(session *udpSession, destination netip.AddrPort, origin adapter.UDPConn) {
	e.udpMu.Lock()
	if origin != nil && session.flows[destination] == origin {
		delete(session.flows, destination)
	}
	session.users--
	closeRemote := session.users == 0
	if closeRemote {
		session.stopped = true
		if e.udpSessions[session.key] == session {
			delete(e.udpSessions, session.key)
		}
	}
	remote := session.remote
	e.udpMu.Unlock()
	if closeRemote && remote != nil {
		remote.Close()
	}
}

func (e *Engine) readUDP(session *udpSession) {
	defer func() {
		e.udpMu.Lock()
		session.stopped = true
		if e.udpSessions[session.key] == session {
			delete(e.udpSessions, session.key)
		}
		origins := make([]adapter.UDPConn, 0, len(session.flows))
		for _, origin := range session.flows {
			origins = append(origins, origin)
		}
		e.udpMu.Unlock()
		for _, origin := range origins {
			origin.Close()
		}
	}()
	buf := make([]byte, 65535)
	session.remote.SetDeadline(time.Now().Add(90 * time.Second))
	for e.ctx.Err() == nil {
		payload, source, err := session.remote.ReadFrom(buf)
		if err != nil {
			return
		}
		from, err := netip.ParseAddrPort(source)
		if err != nil {
			continue
		}
		e.udpMu.Lock()
		origin := session.flows[from]
		e.udpMu.Unlock()
		// Accept replies only from contacted destinations. Shared mapping
		// does not promise full-cone filtering or unsolicited inbound peers.
		if origin == nil {
			continue
		}
		origin.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := origin.Write(payload); err != nil {
			origin.Close()
			continue
		}
		e.downloaded.Add(uint64(len(payload)))
	}
}

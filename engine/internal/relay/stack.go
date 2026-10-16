package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"accelerator/engine/internal/socks"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/option"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type Stats struct {
	TCPStarted                 uint64 `json:"tcpStarted"`
	UDPStarted                 uint64 `json:"udpStarted"`
	UDPAssociations            uint64 `json:"udpAssociations"`
	Failed                     uint64 `json:"failed"`
	Uploaded                   uint64 `json:"uploaded"`
	Downloaded                 uint64 `json:"downloaded"`
	ProxyRead                  uint64 `json:"proxyRead"`
	DeviceRead                 uint64 `json:"deviceRead"`
	DownloadWriteMillis        uint64 `json:"downloadWriteMillis"`
	LongestDownloadWriteMillis uint64 `json:"longestDownloadWriteMillis"`
	TCPRetransmits             uint64 `json:"tcpRetransmits"`
	TCPTimeouts                uint64 `json:"tcpTimeouts"`
	TCPSendErrors              uint64 `json:"tcpSendErrors"`
	TCPChecksumErrors          uint64 `json:"tcpChecksumErrors"`
	TCPResetsReceived          uint64 `json:"tcpResetsReceived"`
	PendingPackets             int    `json:"pendingPackets"`
}

type Engine struct {
	stack                     *stack.Stack
	link                      *channel.Endpoint
	proxy                     socks.Client
	ctx                       context.Context
	cancel                    context.CancelFunc
	mu                        sync.Mutex
	conns                     map[io.Closer]struct{}
	closed                    bool
	wg                        sync.WaitGroup
	closeOnce                 sync.Once
	tcpStarted                atomic.Uint64
	udpStarted                atomic.Uint64
	udpAssociations           atomic.Uint64
	udpMu                     sync.Mutex
	udpSessions               map[udpSource]*udpSession
	failed                    atomic.Uint64
	uploaded                  atomic.Uint64
	downloaded                atomic.Uint64
	proxyRead                 atomic.Uint64
	deviceRead                atomic.Uint64
	downloadWriteNanos        atomic.Uint64
	longestDownloadWriteNanos atomic.Uint64
	limits                    chan struct{}
	dnsGateway                netip.Addr
	dnsRemote                 string
}

func New(proxy socks.Client) (*Engine, error) {
	return NewWithDNS(proxy, netip.Addr{}, "")
}

func NewWithDNS(proxy socks.Client, gateway netip.Addr, resolver string) (*Engine, error) {
	if err := proxy.Validate(); err != nil {
		return nil, err
	}
	if gateway.IsValid() {
		target, err := netip.ParseAddrPort(resolver)
		if !gateway.Is4() || err != nil || !target.Addr().Is4() || target.Addr().IsPrivate() ||
			!target.Addr().IsGlobalUnicast() || target.Port() != 53 {
			return nil, errors.New("DNS 上游需为公共 IPv4 的 53 端口")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		// Leave room for short Wi-Fi bursts before the relay goroutines run.
		link:  channel.New(4096, 1500, ""),
		proxy: proxy, ctx: ctx, cancel: cancel,
		conns: make(map[io.Closer]struct{}), limits: make(chan struct{}, 1024),
		udpSessions: make(map[udpSource]*udpSession),
		dnsGateway:  gateway, dnsRemote: resolver,
	}
	// RACK in the pinned stack spuriously retransmits on this queued Ethernet
	// link, even in lossless local downloads. Use SACK/duplicate-ACK recovery
	// plus the standard retransmission timeout; keep congestion control,
	// retransmission and selective ACK enabled.
	s, err := core.CreateStack(&core.Config{LinkEndpoint: e.link, TransportHandler: e,
		Options: []option.Option{option.WithTCPRecovery(0)},
	})
	if err != nil {
		cancel()
		return nil, err
	}
	e.stack = s
	return e, nil
}

func (e *Engine) Inject(ip []byte) error {
	if len(ip) < 20 || ip[0]>>4 != 4 {
		return errors.New("仅支持 IPv4 数据包")
	}
	if e.ctx.Err() != nil {
		return errors.New("转发引擎已停止")
	}
	packet := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(ip)})
	defer packet.DecRef()
	e.link.InjectInbound(header.IPv4ProtocolNumber, packet)
	return nil
}

func (e *Engine) ReadPacket(ctx context.Context) ([]byte, error) {
	// Shutdown closes the channel endpoint; callers also cancel their own pump.
	packet := e.link.ReadContext(ctx)
	if packet == nil {
		return nil, context.Canceled
	}
	defer packet.DecRef()
	b := packet.ToBuffer()
	defer b.Release()
	return append([]byte(nil), b.Flatten()...), nil
}

func (e *Engine) register(c io.Closer) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		c.Close()
		return false
	}
	e.conns[c] = struct{}{}
	return true
}

func (e *Engine) unregister(c io.Closer) {
	e.mu.Lock()
	delete(e.conns, c)
	e.mu.Unlock()
	c.Close()
}

func (e *Engine) start(c io.Closer, action func()) bool {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		c.Close()
		return false
	}
	select {
	case e.limits <- struct{}{}:
	default:
		e.mu.Unlock()
		e.failed.Add(1)
		c.Close()
		return false
	}
	e.conns[c] = struct{}{}
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		defer func() { <-e.limits }()
		defer e.unregister(c)
		action()
	}()
	return true
}

func (e *Engine) HandleTCP(origin adapter.TCPConn) {
	e.start(origin, func() {
		e.tcpStarted.Add(1)
		id := origin.ID()
		destination := net.JoinHostPort(id.LocalAddress.String(), portString(id.LocalPort))
		destination = e.destination(destination)
		ctx, cancel := context.WithTimeout(e.ctx, 8*time.Second)
		remote, err := e.proxy.DialTCP(ctx, destination)
		cancel()
		if err != nil {
			e.failed.Add(1)
			return
		}
		if !e.register(remote) {
			return
		}
		defer e.unregister(remote)
		var pump sync.WaitGroup
		pump.Add(2)
		copyStream := func(dst, src net.Conn, count, read *atomic.Uint64, download bool) {
			defer pump.Done()
			writer := &countWriter{writer: dst, count: count}
			if download {
				writer.wait = &e.downloadWriteNanos
				writer.longest = &e.longestDownloadWriteNanos
			}
			io.Copy(writer, &countReader{reader: src, count: read})
			if cw, ok := dst.(interface{ CloseWrite() error }); ok {
				cw.CloseWrite()
			}
			dst.SetReadDeadline(time.Now().Add(15 * time.Second))
		}
		go copyStream(remote, origin, &e.uploaded, &e.deviceRead, false)
		go copyStream(origin, remote, &e.downloaded, &e.proxyRead, true)
		pump.Wait()
	})
}

func (e *Engine) HandleUDP(origin adapter.UDPConn) {
	e.start(origin, func() {
		e.udpStarted.Add(1)
		id := origin.ID()
		original := net.JoinHostPort(id.LocalAddress.String(), portString(id.LocalPort))
		destination, parseErr := netip.ParseAddrPort(e.destination(original))
		source, sourceErr := netip.ParseAddrPort(net.JoinHostPort(id.RemoteAddress.String(), portString(id.RemotePort)))
		if parseErr != nil || sourceErr != nil {
			e.failed.Add(1)
			return
		}
		// DNS rewriting needs a separate association to preserve each reply's
		// original source when gateway DNS and direct DNS use the same port.
		key := udpSource{Address: source, DNS: original != destination.String()}
		session, err := e.acquireUDP(key)
		if err != nil {
			if e.ctx.Err() == nil {
				e.failed.Add(1)
			}
			return
		}
		defer e.releaseUDP(session, destination, origin)
		if err := e.attachUDP(session, destination, origin); err != nil {
			if e.ctx.Err() == nil {
				e.failed.Add(1)
			}
			return
		}
		buf := make([]byte, 65535)
		for {
			origin.SetReadDeadline(time.Now().Add(90 * time.Second))
			n, err := origin.Read(buf)
			if err != nil {
				break
			}
			if err := session.write(buf[:n], destination); err != nil {
				if e.ctx.Err() == nil {
					e.failed.Add(1)
				}
				break
			}
			e.uploaded.Add(uint64(n))
		}
	})
}

func (e *Engine) destination(address string) string {
	destination, err := netip.ParseAddrPort(address)
	if err == nil && e.dnsGateway.IsValid() && destination.Addr() == e.dnsGateway && destination.Port() == 53 {
		return e.dnsRemote
	}
	return address
}

func (e *Engine) Snapshot() Stats {
	tcp := e.stack.Stats().TCP
	return Stats{
		TCPStarted: e.tcpStarted.Load(), UDPStarted: e.udpStarted.Load(),
		UDPAssociations: e.udpAssociations.Load(),
		Failed:          e.failed.Load(), Uploaded: e.uploaded.Load(), Downloaded: e.downloaded.Load(),
		ProxyRead: e.proxyRead.Load(), DeviceRead: e.deviceRead.Load(),
		DownloadWriteMillis:        e.downloadWriteNanos.Load() / uint64(time.Millisecond),
		LongestDownloadWriteMillis: e.longestDownloadWriteNanos.Load() / uint64(time.Millisecond),
		TCPRetransmits:             tcp.Retransmits.Value(), TCPTimeouts: tcp.Timeouts.Value(),
		TCPSendErrors: tcp.SegmentSendErrors.Value(), TCPChecksumErrors: tcp.ChecksumErrors.Value(),
		TCPResetsReceived: tcp.ResetsReceived.Value(), PendingPackets: e.link.NumQueued(),
	}
}

func (e *Engine) ProxyAddress() string { return e.proxy.Address }

func (e *Engine) Close() {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.cancel()
		for c := range e.conns {
			c.Close()
		}
		e.mu.Unlock()
		e.link.Close()
		e.stack.Close()
		e.wg.Wait()
		e.stack.Wait()
	})
}

func portString(port uint16) string {
	return strconv.Itoa(int(port))
}

type countWriter struct {
	writer  io.Writer
	count   *atomic.Uint64
	wait    *atomic.Uint64
	longest *atomic.Uint64
}

func (w *countWriter) Write(p []byte) (int, error) {
	var start time.Time
	if w.wait != nil {
		start = time.Now()
	}
	n, err := w.writer.Write(p)
	w.count.Add(uint64(n))
	if w.wait != nil {
		elapsed := uint64(time.Since(start))
		w.wait.Add(elapsed)
		for previous := w.longest.Load(); elapsed > previous; previous = w.longest.Load() {
			if w.longest.CompareAndSwap(previous, elapsed) {
				break
			}
		}
	}
	return n, err
}

type countReader struct {
	reader io.Reader
	count  *atomic.Uint64
}

func (r *countReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.count.Add(uint64(n))
	return n, err
}

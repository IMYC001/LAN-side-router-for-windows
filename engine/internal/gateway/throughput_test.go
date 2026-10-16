package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"accelerator/engine/internal/network"
	"accelerator/engine/internal/packet"
	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/socks"
	"accelerator/engine/internal/testproxy"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// The mock driver exchanges Ethernet frames with a real TCP peer stack.
// Only the driver is substituted; runGateway, its lease, packet validation,
// batching and the SOCKS relay are the production implementations.
type bulkCapture struct {
	ctx                               context.Context
	input                             chan []byte
	peer                              *channel.Endpoint
	gateway                           packet.ManualGateway
	delay                             time.Duration
	batches                           atomic.Uint64
	frames                            atomic.Uint64
	faults                            bulkFaults
	downData, upData                  int
	droppedDown, droppedUp, reordered bool
	held                              []byte
}

func (c *bulkCapture) Read(ctx context.Context) ([]byte, error) {
	for {
		select {
		case frame := <-c.input:
			if tcpPayload(frame) {
				c.upData++
				if c.faults.dropUpload && c.upData == 7 {
					c.droppedUp = true
					continue
				}
			}
			return frame, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type bulkFaults struct {
	dropDownload                int
	dropUpload, reorderDownload bool
}

func tcpPayload(frame []byte) bool {
	if len(frame) < 54 || frame[23] != 6 {
		return false
	}
	ihl := int(frame[14]&15) * 4
	if ihl < 20 || len(frame) < 14+ihl+20 {
		return false
	}
	thl := int(frame[14+ihl+12]>>4) * 4
	return int(binary.BigEndian.Uint16(frame[16:18])) > ihl+thl && thl >= 20
}

func (c *bulkCapture) Send(frame []byte) error { return c.SendBatch([][]byte{frame}) }

func (c *bulkCapture) SendBatch(frames [][]byte) error {
	if c.delay != 0 {
		timer := time.NewTimer(c.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-c.ctx.Done():
			return c.ctx.Err()
		}
	}
	for _, frame := range frames {
		if len(frame) < 34 || !bytes.Equal(frame[:6], c.gateway.Target.DeviceMAC) ||
			!bytes.Equal(frame[6:12], c.gateway.Target.LocalMAC) ||
			binary.BigEndian.Uint16(frame[12:14]) != 0x0800 {
			return fmt.Errorf("invalid gateway Ethernet output")
		}
		length := int(binary.BigEndian.Uint16(frame[16:18]))
		if length < 20 || length > len(frame)-14 {
			return fmt.Errorf("invalid gateway IP length")
		}
		if tcpPayload(frame) {
			c.downData++
			if c.faults.dropDownload != 0 && c.downData == c.faults.dropDownload {
				c.droppedDown = true
				continue
			}
			if c.faults.reorderDownload && c.downData == 11 {
				c.held = append([]byte(nil), frame...)
				c.reordered = true
				continue
			}
		}
		c.deliver(frame)
		if c.held != nil {
			c.deliver(c.held)
			c.held = nil
		}
	}
	c.batches.Add(1)
	c.frames.Add(uint64(len(frames)))
	return nil
}

func (c *bulkCapture) deliver(frame []byte) {
	length := int(binary.BigEndian.Uint16(frame[16:18]))
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(append([]byte(nil), frame[14:14+length]...)),
	})
	c.peer.InjectInbound(ipv4.ProtocolNumber, pkt)
	pkt.DecRef()
}

func gatewayBulk(t testing.TB, delay time.Duration, size int, downloadOnly bool, faults bulkFaults) (time.Duration, uint64, uint64, relay.Stats) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	directory := t.TempDir()
	lease := filepath.Join(directory, "lease")
	if err := os.WriteFile(lease, nil, 0600); err != nil {
		t.Fatal(err)
	}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				os.Chtimes(lease, now, now)
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	chunk := bytes.Repeat([]byte{0x31, 0x9a, 0x00, 0xff}, 16384)
	var responder func(net.Conn)
	if downloadOnly {
		responder = func(conn net.Conn) {
			for remaining := size; remaining > 0; {
				n, err := conn.Write(chunk[:min(remaining, len(chunk))])
				if err != nil {
					return
				}
				remaining -= n
			}
		}
	}
	server, err := testproxy.StartTCPResponder(responder)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	engine, err := relay.New(socks.Client{Address: server.Address()})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	link := channel.New(4096, 1500, "")
	peer := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})
	defer func() { link.Close(); peer.Close(); peer.Wait() }()
	if err := peer.CreateNIC(1, link); err != nil {
		t.Fatal(err)
	}
	address := netip.MustParseAddr("10.254.83.2")
	if err := peer.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address: tcpip.AddrFrom4(address.As4()), PrefixLen: 30,
		},
	}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	peer.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	local, _ := net.ParseMAC("02:11:22:33:44:55")
	device, _ := net.ParseMAC("02:11:22:33:44:66")
	gateway := packet.ManualGateway{Address: netip.MustParseAddr("10.254.83.1"),
		Target: packet.Target{LocalMAC: local, DeviceMAC: device, DeviceIP: address}}
	capture := &bulkCapture{ctx: ctx, input: make(chan []byte, 4096), peer: link, gateway: gateway, delay: delay, faults: faults}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		for {
			pkt := link.ReadContext(ctx)
			if pkt == nil {
				return
			}
			b := pkt.ToBuffer()
			frame := make([]byte, max(60, 14+b.Size()))
			copy(frame[:6], local)
			copy(frame[6:12], device)
			binary.BigEndian.PutUint16(frame[12:14], 0x0800)
			copy(frame[14:], b.Flatten())
			b.Release()
			pkt.DecRef()
			select {
			case capture.input <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- runGateway(ctx, cancel, directory, capture, engine, gateway, network.Adapter{}, &Status{})
	}()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			<-inputDone
			if err := <-done; err != nil {
				t.Errorf("gateway failed: %v", err)
			}
		})
	}
	defer stop()
	start := time.Now()
	conn, err := gonet.DialContextTCP(ctx, peer, tcpip.FullAddress{
		NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443,
	}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	written := make(chan error, 1)
	go func() {
		if downloadOnly {
			written <- nil
			return
		}
		for remaining := size; remaining > 0; {
			n := min(remaining, len(chunk))
			count, err := conn.Write(chunk[:n])
			if err != nil {
				written <- err
				return
			}
			remaining -= count
		}
		written <- nil
	}()
	reply := make([]byte, len(chunk))
	for remaining := size; remaining > 0; {
		n := min(remaining, len(reply))
		if _, err := io.ReadFull(conn, reply[:n]); err != nil {
			conn.Close()
			<-written
			t.Fatal(err)
		}
		if !bytes.Equal(chunk[:n], reply[:n]) {
			t.Fatal("gateway TCP payload mismatch")
		}
		remaining -= n
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	stats := engine.Snapshot()
	if stats.Failed != 0 || stats.Downloaded < uint64(size) || (!downloadOnly && stats.Uploaded < uint64(size)) {
		t.Fatalf("incomplete relay: %+v", stats)
	}
	// Join both pumps before checking driver-owned fault state.
	stop()
	if (faults.dropDownload != 0) != capture.droppedDown || faults.dropUpload != capture.droppedUp ||
		faults.reorderDownload != capture.reordered {
		t.Fatal("requested loss/reordering was not exercised")
	}
	if faults.dropDownload == 0 && !faults.dropUpload && !faults.reorderDownload &&
		(stats.TCPRetransmits != 0 || stats.TCPTimeouts != 0) {
		t.Fatalf("spurious recovery on lossless FIFO link: %+v", stats)
	}
	return elapsed, capture.batches.Load(), capture.frames.Load(), stats
}

func TestGatewayBulkTransfer(t *testing.T) {
	for _, delay := range []time.Duration{0, 2 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			const size = 8 << 20
			elapsed, batches, frames, stats := gatewayBulk(t, delay, size, false, bulkFaults{})
			t.Logf("8 MiB echo via production gateway: %.1f Mbit/s, %.3fs, batches=%d frames=%d retransmits=%d timeouts=%d; mock driver delay=%s, no physical traffic",
				float64(size)*8/elapsed.Seconds()/1e6, elapsed.Seconds(), batches, frames, stats.TCPRetransmits, stats.TCPTimeouts, delay)
		})
	}
}

func TestGatewayDownload(t *testing.T) {
	for _, delay := range []time.Duration{0, 2 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			const size = 32 << 20
			elapsed, batches, frames, stats := gatewayBulk(t, delay, size, true, bulkFaults{})
			t.Logf("32 MiB validated download: %.1f Mbit/s, %.3fs, batches=%d frames=%d retransmits=%d timeouts=%d; mock driver delay=%s",
				float64(size)*8/elapsed.Seconds()/1e6, elapsed.Seconds(), batches, frames, stats.TCPRetransmits, stats.TCPTimeouts, delay)
		})
	}
}

func TestGatewayTCPRecoversFromLossAndReordering(t *testing.T) {
	for _, tc := range []struct {
		name   string
		faults bulkFaults
	}{
		{"download-loss", bulkFaults{dropDownload: 7}},
		{"upload-loss", bulkFaults{dropUpload: true}},
		{"download-reordering", bulkFaults{reorderDownload: true}},
		{"bidirectional-loss-and-reordering", bulkFaults{dropDownload: 7, dropUpload: true, reorderDownload: true}},
		{"single-segment-loss", bulkFaults{dropDownload: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			size, downloadOnly := 2<<20, false
			if tc.name == "single-segment-loss" {
				size, downloadOnly = 512, true
			}
			_, _, _, stats := gatewayBulk(t, 2*time.Millisecond, size, downloadOnly, tc.faults)
			if tc.faults.dropDownload != 0 && stats.TCPRetransmits == 0 {
				t.Fatal("injected download loss did not trigger retransmission")
			}
			if downloadOnly && stats.TCPTimeouts == 0 {
				t.Fatal("lost single segment did not exercise retransmission timeout")
			}
		})
	}
}

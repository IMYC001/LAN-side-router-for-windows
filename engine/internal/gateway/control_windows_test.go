package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	"accelerator/engine/internal/probe"
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

func fixtureRuntime(t *testing.T, id string, slot int) *multiGatewayRuntime {
	t.Helper()
	engine, err := relay.New(socks.Client{Address: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &multiGatewayRuntime{id: id, engine: engine, ctx: ctx, cancel: cancel,
		request: DeviceRequest{ID: id, Plan: network.PlanRequest{Slot: slot}},
		plan:    network.ManualPlan{DeviceIP: fmt.Sprintf("10.250.%d.2", slot)},
		bound:   true, mac: "02:11:22:33:44:77", sampled: time.Now(), previous: relay.Stats{Downloaded: 77}}
	t.Cleanup(r.close)
	return r
}

func TestDeviceControlRetainsRuntimeAndRollsBackOnlyAdditions(t *testing.T) {
	retained := fixtureRuntime(t, "second", 1)
	retained.received.Store(50)
	registry := &runtimeRegistry{}
	registry.current.Store(&runtimeSet{devices: []*multiGatewayRuntime{retained}})
	added := fixtureRuntime(t, "first", 0)
	control := deviceControl{Revision: 1, Devices: []DeviceRequest{retained.request, added.request}}
	build := func(context.Context, DeviceRequest) (*multiGatewayRuntime, error) { return added, nil }
	prepared := prepareControl(context.Background(), control, registry.load(), build)
	if err := registry.commit(prepared, func([]string) error { return errors.New("fixture capture failure") }); err == nil {
		t.Fatal("capture failure accepted")
	}
	if registry.load()[0] != retained || retained.closed.Load() || !added.closed.Load() ||
		retained.received.Load() != 50 || !retained.bound || retained.previous.Downloaded != 77 {
		t.Fatal("failed addition disturbed retained runtime or leaked added runtime")
	}
	added = fixtureRuntime(t, "first", 0)
	prepared = prepareControl(context.Background(), control, registry.load(), build)
	if err := registry.commit(prepared, nil); err != nil {
		t.Fatal(err)
	}
	prepared = prepareControl(context.Background(), deviceControl{Revision: 2,
		Devices: []DeviceRequest{retained.request}}, registry.load(), build)
	if err := registry.commit(prepared, nil); err != nil || registry.load()[0] != retained ||
		retained.ctx.Err() != nil || !added.closed.Load() || added.ctx.Err() == nil ||
		retained.received.Load() != 50 || retained.previous.Downloaded != 77 {
		t.Fatal("removal changed retained runtime or failed to cancel removed runtime")
	}
}

func TestDeviceControlValidationAndCancellation(t *testing.T) {
	retained := fixtureRuntime(t, "first", 0)
	for _, devices := range [][]DeviceRequest{
		nil,
		{retained.request, retained.request},
		{retained.request, {ID: "second", Plan: network.PlanRequest{Slot: 0}}},
		{{ID: "first", Plan: network.PlanRequest{Slot: 1}}},
		{{ID: "second", Plan: network.PlanRequest{Slot: 8}}},
	} {
		if validateControl(deviceControl{Revision: 1, Devices: devices}, []*multiGatewayRuntime{retained}) == nil {
			t.Fatalf("unsafe change accepted: %+v", devices)
		}
	}
	added := fixtureRuntime(t, "second", 1)
	ctx, cancel := context.WithCancel(context.Background())
	result := prepareControl(ctx, deviceControl{Revision: 1, Devices: []DeviceRequest{retained.request, added.request}},
		[]*multiGatewayRuntime{retained}, func(context.Context, DeviceRequest) (*multiGatewayRuntime, error) {
			cancel()
			return added, nil
		})
	if !errors.Is(result.err, context.Canceled) || !added.closed.Load() || retained.closed.Load() {
		t.Fatal("canceled preparation leaked or disturbed retained device")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "control.json")
	for _, data := range []string{`{"revision":1,"devices":[]}`, `{`, string(bytes.Repeat([]byte(" "), 65537))} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, accepted := readDeviceControl(dir, 1); accepted {
			t.Fatal("stale, malformed or oversized control accepted")
		}
	}
}

// Multi-device tests run the complete production loop with real TCP stacks and
// loopback SOCKS nodes. No physical adapter, public endpoint or driver is used.
type memoryMultiCapture struct {
	input   chan []byte
	peers   map[netip.Addr]*channel.Endpoint
	delay   time.Duration
	mu      sync.Mutex
	targets []string
	closed  atomic.Bool
	batches atomic.Uint64
}

func (c *memoryMultiCapture) Read(ctx context.Context) ([]byte, error) {
	select {
	case frame := <-c.input:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *memoryMultiCapture) Send(frame []byte) error { return c.SendBatch([][]byte{frame}) }
func (c *memoryMultiCapture) SendBatch(frames [][]byte) error {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	for _, frame := range frames {
		if len(frame) < 34 || binary.BigEndian.Uint16(frame[12:14]) != 0x0800 {
			return errors.New("invalid fixture output")
		}
		address := netip.AddrFrom4([4]byte(frame[30:34]))
		peer := c.peers[address]
		if peer == nil {
			return errors.New("packet routed to wrong device")
		}
		n := int(binary.BigEndian.Uint16(frame[16:18]))
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(append([]byte(nil), frame[14:14+n]...)),
		})
		peer.InjectInbound(ipv4.ProtocolNumber, pkt)
		pkt.DecRef()
	}
	c.batches.Add(1)
	return nil
}
func (c *memoryMultiCapture) Close() { c.closed.Store(true) }
func (c *memoryMultiCapture) SetTargets(addresses []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.targets = append([]string(nil), addresses...)
	return nil
}

func multiPeer(t *testing.T, ctx context.Context, capture *memoryMultiCapture, plan network.ManualPlan, suffix byte) *stack.Stack {
	t.Helper()
	link := channel.New(4096, 1500, "")
	peer := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	if err := peer.CreateNIC(1, link); err != nil {
		t.Fatal(err)
	}
	address := netip.MustParseAddr(plan.DeviceIP)
	if err := peer.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4(address.As4()), PrefixLen: 30}},
		stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	peer.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	capture.peers[address] = link
	local, _ := net.ParseMAC(plan.Adapter.MAC)
	device := net.HardwareAddr{2, 0x11, 0x22, 0x33, 0x44, suffix}
	done := make(chan struct{})
	go func() {
		defer close(done)
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
	t.Cleanup(func() { link.Close(); <-done; peer.Close(); peer.Wait() })
	return peer
}

func TestMultiGatewayChangeKeepsOtherLiveTCPAndStatistics(t *testing.T) {
	for _, sameNode := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-node=%v", sameNode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			// Cleanup cancels before peer cleanup joins the in-memory packet pumps.
			t.Cleanup(cancel)
			server, err := testproxy.Start()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			other, err := testproxy.Start()
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			dir := t.TempDir()
			lease := filepath.Join(dir, "lease")
			if err := os.WriteFile(lease, nil, 0600); err != nil {
				t.Fatal(err)
			}
			go func() {
				tick := time.NewTicker(time.Second)
				defer tick.Stop()
				for {
					select {
					case now := <-tick.C:
						os.Chtimes(lease, now, now)
					case <-ctx.Done():
						return
					}
				}
			}()
			adapter := network.Adapter{Name: "Offline", GUID: "offline-adapter", Index: 1,
				Physical: true, Status: "Up", MAC: "02:11:22:33:44:55",
				IPv4: "192.168.1.20", Prefix: 24, Gateway: "192.168.1.1"}
			snapshot := network.Snapshot{Captured: time.Now().Format(time.RFC3339), WinpkFilter: true,
				Adapters: []network.Adapter{adapter}}
			makeRequest := func(slot int, proxy string) (DeviceRequest, network.ManualPlan) {
				p := network.PlanRequest{Slot: slot, AdapterIndex: 1, AutoDiscover: true, IPv6Absent: true, Proxy: proxy}
				plan := network.BuildManualPlan(snapshot, p)
				if !plan.Prepared {
					t.Fatalf("fixture plan blocked: %+v", plan)
				}
				return DeviceRequest{ID: fmt.Sprintf("device-%d", slot), Plan: p, Expected: Expected{
					AdapterGUID: adapter.GUID, AdapterIP: adapter.IPv4, AdapterMAC: adapter.MAC,
					AdapterPrefix: adapter.Prefix, Router: adapter.Gateway, DeviceIP: plan.DeviceIP, Gateway: plan.Gateway,
				}}, plan
			}
			firstProxy := server.Address()
			if !sameNode {
				firstProxy = other.Address()
			}
			first, firstPlan := makeRequest(0, firstProxy)
			second, secondPlan := makeRequest(1, server.Address())
			capture := &memoryMultiCapture{input: make(chan []byte, 4096),
				peers: map[netip.Addr]*channel.Endpoint{}, delay: 2 * time.Millisecond}
			firstPeer := multiPeer(t, ctx, capture, firstPlan, 0x66)
			secondPeer := multiPeer(t, ctx, capture, secondPlan, 0x77)
			var opens atomic.Int32
			workerDone := make(chan error, 1)
			go func() {
				workerDone <- runMultiGatewayWith(ctx, cancel, dir, snapshot,
					Request{Mode: "gateway", Devices: []DeviceRequest{second}, Continuous: true}, &Status{},
					multiGatewayDependencies{
						inspect: func(context.Context) (network.Snapshot, error) { return snapshot, nil },
						probe: func(_ context.Context, address string) probe.Result {
							return probe.Result{TCP: address != "127.0.0.1:1", UDP: address != "127.0.0.1:1"}
						},
						open: func(_ network.Adapter, _ string, addresses []string) (captureHandle, error) {
							opens.Add(1)
							capture.SetTargets(addresses)
							return capture, nil
						},
					})
			}()
			defer func() {
				cancel()
				if err := <-workerDone; err != nil {
					t.Errorf("gateway failed: %v", err)
				}
			}()
			dial := func(peer *stack.Stack) net.Conn {
				conn, err := gonet.DialContextTCP(ctx, peer, tcpip.FullAddress{
					NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}, ipv4.ProtocolNumber)
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				return conn
			}
			kept := dial(secondPeer)
			defer kept.Close()
			payload := bytes.Repeat([]byte("continuous second device "), 1024)
			echo := func(conn net.Conn) error {
				if _, err := conn.Write(payload); err != nil {
					return err
				}
				reply := make([]byte, len(payload))
				if _, err := io.ReadFull(conn, reply); err != nil {
					return err
				}
				if !bytes.Equal(payload, reply) {
					return errors.New("echo mismatch")
				}
				return nil
			}
			echoErrors := make(chan error, 1)
			echoStop := make(chan struct{})
			var echoed atomic.Uint64
			go func() {
				for {
					if err := echo(kept); err != nil {
						echoErrors <- err
						return
					}
					echoed.Add(uint64(len(payload)))
					select {
					case <-echoStop:
						echoErrors <- nil
						return
					case <-time.After(3 * time.Millisecond):
					}
				}
			}()
			defer func() { close(echoStop); kept.Close(); <-echoErrors }()
			waitState := func(revision int64, predicate func(Status) bool) Status {
				for {
					data, _ := os.ReadFile(filepath.Join(dir, "status.json"))
					var status Status
					if json.Unmarshal(data, &status) == nil && status.ControlRevision >= revision && predicate(status) {
						return status
					}
					select {
					case <-ctx.Done():
						t.Fatal("status or live transfer timed out")
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			findSecond := func(status Status) DeviceStatus {
				for _, device := range status.Devices {
					if device.ID == second.ID {
						return device
					}
				}
				return DeviceStatus{}
			}
			baseline := findSecond(waitState(0, func(s Status) bool { return findSecond(s).Stats.Downloaded > 0 }))
			change := func(revision int64, devices []DeviceRequest) Status {
				data, _ := json.Marshal(deviceControl{Revision: revision, Devices: devices})
				if err := os.WriteFile(filepath.Join(dir, "control.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				state := waitState(revision, func(s Status) bool { return s.ControlRevision == revision })
				keptState := findSecond(state)
				if opens.Load() != 1 || capture.closed.Load() || keptState.DeviceMAC != baseline.DeviceMAC ||
					keptState.Phase != "connected" || keptState.Stats.TCPStarted != 1 ||
					keptState.Stats.Downloaded < baseline.Stats.Downloaded || echoed.Load() <= baseline.Stats.Downloaded {
					t.Fatalf("other device lost connection, identity, traffic or NIC: %+v", state)
				}
				baseline = keptState
				return state
			}
			added := change(1, []DeviceRequest{first, second})
			if added.ControlError != "" || len(added.Devices) != 2 {
				t.Fatalf("add failed: %+v", added)
			}
			firstConnection := dial(firstPeer)
			if err := echo(firstConnection); err != nil {
				t.Fatal(err)
			}
			firstConnection.Close()
			removed := change(2, []DeviceRequest{second})
			if removed.ControlError != "" || len(removed.Devices) != 1 {
				t.Fatalf("remove failed: %+v", removed)
			}
			bad := first
			bad.Plan.Proxy = "127.0.0.1:1"
			failed := change(3, []DeviceRequest{second, bad})
			if failed.ControlError == "" || len(failed.Devices) != 1 {
				t.Fatalf("bad new node did not roll back: %+v", failed)
			}
			restarted := change(4, []DeviceRequest{second, first})
			if restarted.ControlError != "" || len(restarted.Devices) != 2 {
				t.Fatalf("restart failed: %+v", restarted)
			}
			firstConnection = dial(firstPeer)
			if err := echo(firstConnection); err != nil {
				t.Fatal(err)
			}
			bulk := bytes.Repeat([]byte{0x31, 0x9a, 0, 0xff}, 2<<20)
			written := make(chan error, 1)
			started := time.Now()
			go func() {
				_, err := io.Copy(firstConnection, bytes.NewReader(bulk))
				written <- err
			}()
			reply := make([]byte, len(bulk))
			if _, err := io.ReadFull(firstConnection, reply); err != nil {
				firstConnection.Close()
				<-written
				t.Fatal(err)
			}
			if err := <-written; err != nil || !bytes.Equal(reply, bulk) {
				t.Fatal("bulk transfer failed or data changed", err)
			}
			elapsed := time.Since(started)
			firstConnection.Close()
			t.Logf("Second started alone, first added/removed/failed/restarted, sameNode=%v, retained TCP=%d, bytes=%d, capture opens=%d, batches=%d",
				sameNode, baseline.Stats.TCPStarted, baseline.Stats.Downloaded, opens.Load(), capture.batches.Load())
			t.Logf("8 MiB verified multi-device echo with another live transfer and 2ms mock-driver send delay: %.1f Mbit/s; no physical traffic",
				float64(len(bulk))*8/elapsed.Seconds()/1e6)
		})
	}
}

package gateway

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"accelerator/engine/internal/network"
	"accelerator/engine/internal/packet"
	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/socks"
)

func TestLeaseStopsOnMissingStaleAndExplicitStop(t *testing.T) {
	dir := t.TempDir()
	if leaseValid(dir, time.Now()) {
		t.Fatal("missing lease accepted")
	}
	path := filepath.Join(dir, "lease")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !leaseValid(dir, time.Now()) {
		t.Fatal("fresh lease rejected")
	}
	past := time.Now().Add(-9 * time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if leaseValid(dir, time.Now()) {
		t.Fatal("stale lease accepted")
	}
	now := time.Now()
	os.Chtimes(path, now, now)
	os.WriteFile(filepath.Join(dir, "stop"), nil, 0600)
	if leaseValid(dir, time.Now()) {
		t.Fatal("stop ignored")
	}
}

func TestPreviewCannotSilentlyChange(t *testing.T) {
	plan := network.ManualPlan{Adapter: network.Adapter{
		GUID: "abcd", IPv4: "192.168.50.10", MAC: "02:00:00:00:00:10", Prefix: 24, Gateway: "192.168.50.1",
	}, DeviceIP: "10.254.83.2", Gateway: "10.254.83.1"}
	r := Request{Mode: "gateway", Expected: Expected{
		AdapterGUID: "abcd", AdapterIP: plan.Adapter.IPv4, AdapterMAC: plan.Adapter.MAC,
		AdapterPrefix: 24, Router: plan.Adapter.Gateway, DeviceIP: plan.DeviceIP, Gateway: plan.Gateway,
	}}
	if !matchesExpected(r, plan) {
		t.Fatal("valid preview rejected")
	}
	for _, mutate := range []func(*network.ManualPlan){
		func(p *network.ManualPlan) { p.Adapter.GUID = "different" },
		func(p *network.ManualPlan) { p.Adapter.Prefix = 16 },
		func(p *network.ManualPlan) { p.Adapter.Gateway = "192.168.50.254" },
		func(p *network.ManualPlan) { p.DeviceIP = "10.253.84.2" },
		func(p *network.ManualPlan) { p.Gateway = "10.253.84.1" },
	} {
		changed := plan
		mutate(&changed)
		if matchesExpected(r, changed) {
			t.Fatal("changed network/parameters accepted")
		}
	}
}

type simulatedCapture struct {
	frames chan []byte
	err    error
	sent   int
}

func (h *simulatedCapture) Read(ctx context.Context) ([]byte, error) {
	if h.err != nil {
		return nil, h.err
	}
	select {
	case frame := <-h.frames:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (h *simulatedCapture) Send([]byte) error { h.sent++; return nil }

func TestPassiveObservationNeverSendsAndRejectsOtherMAC(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lease"), nil, 0600)
	h := &simulatedCapture{frames: make(chan []byte, 2)}
	frame := make([]byte, 60)
	mac, _ := net.ParseMAC("02:00:00:00:00:20")
	copy(frame[6:12], mac)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	frame[14] = 0x45
	copy(frame[26:30], netip.MustParseAddr("192.168.50.20").AsSlice())
	other := append([]byte(nil), frame...)
	other[6] ^= 2
	h.frames <- other
	h.frames <- frame
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	status := &Status{}
	if err := runPassive(ctx, dir, h, network.ManualPlan{DeviceMAC: mac.String()}, status); err != nil {
		t.Fatal(err)
	}
	if h.sent != 0 || status.Received != 1 || !status.PassivePassed || status.DeviceIP != "192.168.50.20" {
		t.Fatalf("incorrect observation %+v, sends %d", status, h.sent)
	}
}

func TestCaptureFailureAndDeadlineJoinPumps(t *testing.T) {
	for _, captureError := range []error{nil, errors.New("simulated capture failed")} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "lease"), nil, 0600)
		engine, err := relay.New(socks.Client{Address: "127.0.0.1:1"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		h := &simulatedCapture{frames: make(chan []byte), err: captureError}
		err = runGateway(ctx, cancel, dir, h, engine, packet.ManualGateway{}, network.Adapter{}, &Status{})
		cancel()
		engine.Close()
		if !errors.Is(err, captureError) {
			t.Fatalf("got %v, want %v", err, captureError)
		}
		if h.sent != 0 {
			t.Fatal("unrequested packet emitted")
		}
	}
}

func TestIdlePassiveDoesNotReportUnassignedGatewayIP(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	h := &simulatedCapture{frames: make(chan []byte)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	status := &Status{DeviceIP: "10.254.83.2"}
	if err := runPassive(ctx, dir, h, network.ManualPlan{
		OriginalIP: "192.168.50.20", DeviceMAC: "02:00:00:00:00:20",
	}, status); err != nil {
		t.Fatal(err)
	}
	if status.DeviceIP != "192.168.50.20" || status.PassivePassed || status.Received != 0 || h.sent != 0 {
		t.Fatalf("idle observation misreported device identity: %+v", status)
	}
}

func TestQueuedARPIsNotSentAfterStop(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stop"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	local, _ := net.ParseMAC("02:00:00:00:00:10")
	device, _ := net.ParseMAC("02:00:00:00:00:20")
	g := packet.ManualGateway{Address: netip.MustParseAddr("10.254.83.1"), Target: packet.Target{
		LocalMAC: local, DeviceMAC: device, DeviceIP: netip.MustParseAddr("10.254.83.2"),
	}}
	frame := make([]byte, 60)
	copy(frame[:6], []byte{255, 255, 255, 255, 255, 255})
	copy(frame[6:12], device)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	arp := frame[14:]
	binary.BigEndian.PutUint16(arp[:2], 1)
	binary.BigEndian.PutUint16(arp[2:4], 0x0800)
	arp[4], arp[5] = 6, 4
	binary.BigEndian.PutUint16(arp[6:8], 1)
	copy(arp[8:14], device)
	copy(arp[14:18], g.Target.DeviceIP.AsSlice())
	copy(arp[24:28], g.Address.AsSlice())
	if _, ok := g.ReplyARP(frame); !ok {
		t.Fatal("fixture must otherwise trigger a gateway reply")
	}
	h := &simulatedCapture{frames: make(chan []byte, 1)}
	h.frames <- frame
	engine, err := relay.New(socks.Client{Address: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status := &Status{}
	if err := runGateway(ctx, cancel, dir, h, engine, g, network.Adapter{}, status); err != nil {
		t.Fatal(err)
	}
	if h.sent != 0 || status.Forwarded != 0 || status.ARP != 0 {
		t.Fatalf("queued traffic processed after stop: %+v, sends %d", status, h.sent)
	}
}

func TestWorkerRequiresExplicitBoundedRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "request.json")
	for _, data := range []string{
		`{"mode":"gateway","seconds":180}`,
		`{"mode":"gateway","seconds":181,"confirmed":true}`,
		`{"mode":"passive","seconds":180,"confirmed":true}`,
		`{"mode":"capture-all","seconds":10,"confirmed":true}`,
		`{"mode":"passive","seconds":0,"confirmed":true,"continuous":true}`,
		`{"mode":"gateway","seconds":180,"confirmed":true,"continuous":true}`,
		`{"mode":"gateway","seconds":0,"continuous":true}`,
	} {
		os.WriteFile(path, []byte(data), 0600)
		if _, err := loadRequest(path); err == nil {
			t.Fatalf("unsafe request accepted: %s", data)
		}
	}
	os.WriteFile(path, []byte(`{"mode":"passive","seconds":15,"confirmed":true}`), 0600)
	if _, err := loadRequest(path); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(`{"mode":"gateway","seconds":0,"confirmed":true,"continuous":true}`), 0600)
	if _, err := loadRequest(path); err != nil {
		t.Fatal(err)
	}
}

func TestContinuousSessionStopsOnLeaseWithoutDeadline(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lease"), nil, 0600)
	engine, err := relay.New(socks.Client{Address: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &simulatedCapture{frames: make(chan []byte)}
	done := make(chan error, 1)
	go func() {
		done <- runGateway(ctx, cancel, dir, h, engine, packet.ManualGateway{}, network.Adapter{}, &Status{Continuous: true})
	}()
	os.WriteFile(filepath.Join(dir, "stop"), nil, 0600)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("continuous session ignored explicit stop")
	}
	if h.sent != 0 {
		t.Fatal("idle continuous session emitted unrequested packets")
	}
}

func BenchmarkPerPacketLeaseValidation(b *testing.B) {
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !leaseValid(dir, time.Now()) {
			b.Fatal("lease expired")
		}
	}
}

func TestDiscoveryIdleCancelAndStaleLeaseNeverSend(t *testing.T) {
	for _, validLease := range []bool{true, false} {
		dir := t.TempDir()
		if validLease {
			os.WriteFile(filepath.Join(dir, "lease"), nil, 0600)
		}
		h := &simulatedCapture{frames: make(chan []byte, 1)}
		h.frames <- make([]byte, 60)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		plan := network.ManualPlan{Adapter: network.Adapter{MAC: "02:11:22:33:44:55"},
			DeviceIP: "10.254.83.2", Gateway: "10.254.83.1"}
		status := &Status{}
		mac, err := waitForClient(ctx, dir, h, plan, status)
		cancel()
		if err != nil || mac != nil || h.sent != 0 || status.Forwarded != 0 {
			t.Fatalf("idle discovery emitted or latched: %v %v %+v", mac, err, status)
		}
	}
}

func TestDiscoveryBindsConfiguredClientAndRepliesOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	local, _ := net.ParseMAC("02:11:22:33:44:55")
	device, _ := net.ParseMAC("02:11:22:33:44:66")
	plan := network.ManualPlan{Adapter: network.Adapter{MAC: local.String()},
		DeviceIP: "10.254.83.2", Gateway: "10.254.83.1"}
	frame := make([]byte, 60)
	copy(frame[:6], []byte{255, 255, 255, 255, 255, 255})
	copy(frame[6:12], device)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	binary.BigEndian.PutUint16(frame[14:16], 1)
	binary.BigEndian.PutUint16(frame[16:18], 0x0800)
	frame[18], frame[19] = 6, 4
	binary.BigEndian.PutUint16(frame[20:22], 1)
	copy(frame[22:28], device)
	copy(frame[28:32], netip.MustParseAddr(plan.DeviceIP).AsSlice())
	copy(frame[38:42], netip.MustParseAddr(plan.Gateway).AsSlice())
	other := append([]byte(nil), frame...)
	other[28] = 192
	h := &simulatedCapture{frames: make(chan []byte, 2)}
	h.frames <- other
	h.frames <- frame
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status := &Status{}
	mac, err := waitForClient(ctx, dir, h, plan, status)
	if err != nil || mac.String() != device.String() || h.sent != 1 ||
		status.ARP != 1 || status.Sent != 1 || status.DeviceMAC != device.String() ||
		status.IdentityMethod != "configured-gateway-client" || status.Phase != "identified" {
		t.Fatalf("configured client was not bound and answered: %v %v %+v sends=%d", mac, err, status, h.sent)
	}
}

func TestDiscoveryAnswersFirstGatewayEchoWithCachedARP(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	local, _ := net.ParseMAC("02:11:22:33:44:55")
	device, _ := net.ParseMAC("02:11:22:33:44:66")
	plan := network.ManualPlan{Adapter: network.Adapter{MAC: local.String()},
		DeviceIP: "10.254.83.2", Gateway: "10.254.83.1"}
	frame := make([]byte, 42)
	copy(frame[:6], local)
	copy(frame[6:12], device)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	ip := frame[14:]
	ip[0], ip[8], ip[9] = 0x45, 64, 1
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
	copy(ip[12:16], netip.MustParseAddr(plan.DeviceIP).AsSlice())
	copy(ip[16:20], netip.MustParseAddr(plan.Gateway).AsSlice())
	ip[20], ip[24], ip[26] = 8, 1, 2
	sum := func(data []byte) uint16 {
		var result uint32
		for len(data) >= 2 {
			result += uint32(binary.BigEndian.Uint16(data[:2]))
			data = data[2:]
		}
		for result>>16 != 0 {
			result = result&0xffff + result>>16
		}
		return ^uint16(result)
	}
	binary.BigEndian.PutUint16(ip[22:24], sum(ip[20:]))
	binary.BigEndian.PutUint16(ip[10:12], sum(ip[:20]))
	h := &simulatedCapture{frames: make(chan []byte, 1)}
	h.frames <- frame
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status := &Status{}
	mac, err := waitForClient(ctx, dir, h, plan, status)
	if err != nil || mac.String() != device.String() || h.sent != 1 ||
		status.Sent != 1 || status.ARP != 0 || status.Forwarded != 0 {
		t.Fatalf("first gateway echo was lost: mac=%v error=%v status=%+v sends=%d", mac, err, status, h.sent)
	}
}

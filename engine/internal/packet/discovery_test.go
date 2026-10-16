package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func discoveryARP(g ManualGateway) []byte {
	frame := make([]byte, 60)
	copy(frame[:6], []byte{255, 255, 255, 255, 255, 255})
	copy(frame[6:12], g.Target.DeviceMAC)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	binary.BigEndian.PutUint16(frame[14:16], 1)
	binary.BigEndian.PutUint16(frame[16:18], 0x0800)
	frame[18], frame[19] = 6, 4
	binary.BigEndian.PutUint16(frame[20:22], 1)
	copy(frame[22:28], g.Target.DeviceMAC)
	copy(frame[28:32], g.Target.DeviceIP.AsSlice())
	copy(frame[38:42], g.Address.AsSlice())
	return frame
}

func TestDiscoveryRequiresConfiguredClientAndConsistentIdentity(t *testing.T) {
	g := manualFixture()
	frame := discoveryARP(g)
	mac, ok := DiscoverGatewayClient(frame, g.Target.LocalMAC, g.Target.DeviceIP, g.Address)
	if !ok || !bytes.Equal(mac, g.Target.DeviceMAC) {
		t.Fatal("configured client not discovered")
	}
	for _, modify := range []func([]byte){
		func(f []byte) { f[6] ^= 2 },
		func(f []byte) { f[22] ^= 2 },
		func(f []byte) { f[28] = 192 },
		func(f []byte) { f[38] = 192 },
		func(f []byte) { f[20], f[21] = 0, 2 },
		func(f []byte) { f[18] = 8 },
		func(f []byte) { copy(f[6:12], g.Target.LocalMAC); copy(f[22:28], g.Target.LocalMAC) },
		func(f []byte) { f[6], f[22] = 255, 255 },
		func(f []byte) { f[0] = 4 },
	} {
		bad := append([]byte(nil), frame...)
		modify(bad)
		if _, ok := DiscoverGatewayClient(bad, g.Target.LocalMAC, g.Target.DeviceIP, g.Address); ok {
			t.Fatal("unrelated or malformed discovery accepted")
		}
	}
	for length := 0; length < 42; length++ {
		if _, ok := DiscoverGatewayClient(frame[:length], g.Target.LocalMAC, g.Target.DeviceIP, g.Address); ok {
			t.Fatal("truncated ARP accepted")
		}
	}
	// Mutating the receive buffer must never mutate the latched identity.
	frame[6] ^= 2
	if !bytes.Equal(mac, g.Target.DeviceMAC) {
		t.Fatal("identity aliased capture buffer")
	}
}

func TestDiscoveryAllowsCachedGatewayAndBindingRejectsSecondDevice(t *testing.T) {
	g := manualFixture()
	data := make([]byte, 8)
	binary.BigEndian.PutUint16(data[2:4], 53)
	frame := manualIP(g, g.Address.String(), 17, data)
	mac, ok := DiscoverGatewayClient(frame, g.Target.LocalMAC, g.Target.DeviceIP, g.Address)
	if !ok {
		t.Fatal("reconnect with cached ARP not discovered")
	}
	g.Target.DeviceMAC = mac
	frame[6] ^= 2
	if _, ok := g.Input(frame); ok {
		t.Fatal("latched session accepted a second device")
	}
	frame[6] ^= 2
	frame[24] ^= 1
	if _, ok := DiscoverGatewayClient(frame, g.Target.LocalMAC, g.Target.DeviceIP, g.Address); ok {
		t.Fatal("bad IPv4 checksum accepted")
	}
}

func TestDiscoveryAcceptsOnlyValidGatewayEchoAfterCachedARP(t *testing.T) {
	g := manualFixture()
	echo := []byte{8, 0, 0, 0, 1, 2, 3, 4}
	binary.BigEndian.PutUint16(echo[2:4], checksum(echo))
	frame := manualIP(g, g.Address.String(), 1, echo)
	mac, ok := DiscoverGatewayClient(frame, g.Target.LocalMAC, g.Target.DeviceIP, g.Address)
	if !ok || !bytes.Equal(mac, g.Target.DeviceMAC) {
		t.Fatal("gateway ping with cached ARP did not discover client")
	}
	for _, modify := range []func([]byte){
		func(f []byte) { f[0] ^= 2 },
		func(f []byte) { f[26] ^= 2 },
		func(f []byte) { f[30] ^= 2 },
		func(f []byte) { f[34] = 0 },
		func(f []byte) { f[35] = 1 },
		func(f []byte) { f[36] ^= 2 },
		func(f []byte) { f[20] = 0x20 },
	} {
		bad := append([]byte(nil), frame...)
		modify(bad)
		if _, ok := DiscoverGatewayClient(bad, g.Target.LocalMAC, g.Target.DeviceIP, g.Address); ok {
			t.Fatal("invalid or unrelated ICMP discovered client")
		}
	}
	if _, ok := DiscoverGatewayClient(manualIP(g, "1.1.1.1", 1, echo),
		g.Target.LocalMAC, g.Target.DeviceIP, g.Address); ok {
		t.Fatal("public ICMP discovered client without a supported forwarding path")
	}
}

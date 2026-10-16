package packet

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func manualFixture() ManualGateway {
	t := fixture()
	t.DeviceIP = netip.MustParseAddr("10.254.83.2")
	return ManualGateway{Target: t, Address: netip.MustParseAddr("10.254.83.1")}
}

func manualIP(g ManualGateway, destination string, protocol byte, payload []byte) []byte {
	frame := make([]byte, 34+len(payload))
	copy(frame[:6], g.Target.LocalMAC)
	copy(frame[6:12], g.Target.DeviceMAC)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	ip := frame[14:]
	ip[0], ip[8], ip[9] = 0x45, 64, protocol
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
	copy(ip[12:16], g.Target.DeviceIP.AsSlice())
	copy(ip[16:20], netip.MustParseAddr(destination).AsSlice())
	copy(ip[20:], payload)
	binary.BigEndian.PutUint16(ip[10:12], checksum(ip[:20]))
	return frame
}

func TestManualGatewayDoesNotClaimRouterOrOtherDevices(t *testing.T) {
	g := manualFixture()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	withoutRouter := g
	withoutRouter.Target.GatewayMAC = nil
	if err := withoutRouter.Validate(); err != nil {
		t.Fatal("manual gateway unnecessarily depends on router MAC")
	}
	frame := make([]byte, 60)
	copy(frame[:6], []byte{255, 255, 255, 255, 255, 255})
	copy(frame[6:12], g.Target.DeviceMAC)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	arp := frame[14:]
	binary.BigEndian.PutUint16(arp[:2], 1)
	binary.BigEndian.PutUint16(arp[2:4], 0x0800)
	arp[4], arp[5] = 6, 4
	binary.BigEndian.PutUint16(arp[6:8], 1)
	copy(arp[8:14], g.Target.DeviceMAC)
	copy(arp[14:18], g.Target.DeviceIP.AsSlice())
	copy(arp[24:28], g.Address.AsSlice())
	reply, ok := g.ReplyARP(frame)
	if !ok || string(reply[:6]) != string(g.Target.DeviceMAC) ||
		netip.AddrFrom4([4]byte(reply[28:32])) != g.Address {
		t.Fatal("gateway discovery failed")
	}
	copy(arp[24:28], g.Target.GatewayIP.AsSlice())
	if _, ok := g.ReplyARP(frame); ok {
		t.Fatal("real router request intercepted")
	}
	copy(arp[24:28], g.Address.AsSlice())
	copy(frame[:6], g.Target.GatewayMAC)
	if _, ok := g.ReplyARP(frame); ok {
		t.Fatal("ARP addressed to another MAC intercepted")
	}
	copy(frame[:6], g.Target.LocalMAC)
	if _, ok := g.ReplyARP(frame); !ok {
		t.Fatal("targeted ARP request rejected")
	}
	frame[6] ^= 2
	if _, ok := g.ReplyARP(frame); ok {
		t.Fatal("another device's request intercepted")
	}
}

func TestManualIPIsolationAndDNS(t *testing.T) {
	g := manualFixture()
	dns := make([]byte, 8)
	binary.BigEndian.PutUint16(dns[2:4], 53)
	if _, ok := g.Input(manualIP(g, g.Address.String(), 17, dns)); !ok {
		t.Fatal("gateway DNS rejected")
	}
	for _, ip := range []string{"127.0.0.1", "192.168.50.10", "192.168.50.1", "169.254.1.1", "100.64.0.1", "198.18.0.1"} {
		if _, ok := g.Input(manualIP(g, ip, 17, dns)); ok {
			t.Fatalf("internal destination %s exposed", ip)
		}
	}
	if _, ok := g.Input(manualIP(g, "1.1.1.1", 17, dns)); !ok {
		t.Fatal("public destination rejected")
	}
	frame := manualIP(g, g.Address.String(), 6, []byte{0, 1, 0, 80})
	if _, ok := g.Input(frame); ok {
		t.Fatal("non-DNS PC gateway service exposed")
	}
}

func TestGatewayEchoIsNotInternetPing(t *testing.T) {
	g := manualFixture()
	echo := []byte{8, 0, 0, 0, 1, 2, 3, 4, 5, 6}
	binary.BigEndian.PutUint16(echo[2:4], checksum(echo))
	frame := manualIP(g, g.Address.String(), 1, echo)
	reply, ok := g.ReplyEcho(frame)
	if !ok || reply[34] != 0 || checksum(reply[34:44]) != 0 {
		t.Fatal("gateway echo invalid")
	}
	if _, ok := g.ReplyEcho(manualIP(g, "1.1.1.1", 1, echo)); ok {
		t.Fatal("internet echo fabricated")
	}
}

func FuzzManualFrames(f *testing.F) {
	f.Add(make([]byte, 60))
	f.Fuzz(func(t *testing.T, b []byte) {
		g := manualFixture()
		g.Input(b)
		g.ReplyARP(b)
		g.ReplyEcho(b)
	})
}

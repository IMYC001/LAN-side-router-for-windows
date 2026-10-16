package packet

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
)

func fixture() Target {
	local, _ := net.ParseMAC("02:00:00:00:00:01")
	device, _ := net.ParseMAC("02:00:00:00:00:02")
	gateway, _ := net.ParseMAC("02:00:00:00:00:03")
	return Target{LocalMAC: local, DeviceMAC: device, GatewayMAC: gateway, DeviceIP: netip.MustParseAddr("192.168.1.35"), GatewayIP: netip.MustParseAddr("192.168.1.1")}
}

func TestOnlySelectedDeviceAccepted(t *testing.T) {
	target := fixture()
	frame := make([]byte, 34)
	copy(frame[:6], target.LocalMAC)
	copy(frame[6:12], target.DeviceMAC)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	frame[14] = 0x45
	binary.BigEndian.PutUint16(frame[16:18], 20)
	copy(frame[26:30], target.DeviceIP.AsSlice())
	copy(frame[30:34], netip.MustParseAddr("203.0.113.1").AsSlice())
	if _, ok := target.Input(frame); !ok {
		t.Fatal("selected device was rejected")
	}
	for _, offset := range []int{0, 6, 26} {
		modified := append([]byte(nil), frame...)
		modified[offset] ^= 1
		if _, ok := target.Input(modified); ok {
			t.Fatalf("accepted wrong address at offset %d", offset)
		}
	}
	frame[12], frame[13] = 0x86, 0xDD
	if _, ok := target.Input(frame); ok {
		t.Fatal("accepted IPv6")
	}
}

func TestARPIsUnicastAndRecoverable(t *testing.T) {
	target := fixture()
	for _, restore := range []bool{false, true} {
		frame, err := target.ARPReply(restore)
		if err != nil {
			t.Fatal(err)
		}
		if string(frame[:6]) != string(target.DeviceMAC) || binary.BigEndian.Uint16(frame[12:14]) != 0x0806 {
			t.Fatal("ARP was not targeted unicast")
		}
		expected := target.LocalMAC
		if restore {
			expected = target.GatewayMAC
		}
		if string(frame[22:28]) != string(expected) {
			t.Fatal("wrong advertised MAC")
		}
	}
	target.DeviceMAC = target.LocalMAC
	if _, err := target.ARPReply(false); err == nil {
		t.Fatal("accepted self takeover")
	}
}

func FuzzPacketInput(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 60))
	f.Fuzz(func(t *testing.T, data []byte) {
		fixture().Input(data)
	})
}

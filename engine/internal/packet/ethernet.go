package packet

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
)

type Target struct {
	LocalMAC   net.HardwareAddr
	DeviceMAC  net.HardwareAddr
	GatewayMAC net.HardwareAddr
	DeviceIP   netip.Addr
	GatewayIP  netip.Addr
}

func (t Target) Validate() error {
	for _, mac := range []net.HardwareAddr{t.LocalMAC, t.DeviceMAC, t.GatewayMAC} {
		if len(mac) != 6 || mac[0]&1 != 0 || string(mac) == string(make([]byte, 6)) {
			return errors.New("需要有效的单播以太网 MAC")
		}
	}
	if t.LocalMAC.String() == t.DeviceMAC.String() || t.LocalMAC.String() == t.GatewayMAC.String() || t.DeviceMAC.String() == t.GatewayMAC.String() {
		return errors.New("电脑、目标设备、网关的 MAC 不能相同")
	}
	if !t.DeviceIP.Is4() || !t.GatewayIP.Is4() || t.DeviceIP == t.GatewayIP || !t.DeviceIP.IsPrivate() || !t.GatewayIP.IsPrivate() {
		return errors.New("设备和网关必须是不同的 IPv4 私有地址")
	}
	return nil
}

// Input accepts only the selected device's untagged IPv4 unicast frames
// addressed to this PC. Other devices, ARP, IPv6 and VLAN frames stay excluded.
func (t Target) Input(frame []byte) ([]byte, bool) {
	if len(frame) < 34 || string(frame[:6]) != string(t.LocalMAC) || string(frame[6:12]) != string(t.DeviceMAC) || binary.BigEndian.Uint16(frame[12:14]) != 0x0800 {
		return nil, false
	}
	ip := frame[14:]
	ihl := int(ip[0]&15) * 4
	if ip[0]>>4 != 4 || ihl < 20 || len(ip) < ihl {
		return nil, false
	}
	length := int(binary.BigEndian.Uint16(ip[2:4]))
	if length < ihl || length > len(ip) || netip.AddrFrom4([4]byte(ip[12:16])) != t.DeviceIP {
		return nil, false
	}
	dst := netip.AddrFrom4([4]byte(ip[16:20]))
	if dst.IsMulticast() || dst == netip.MustParseAddr("255.255.255.255") || dst.IsUnspecified() {
		return nil, false
	}
	return append([]byte(nil), ip[:length]...), true
}

func (t Target) Output(ip []byte) ([]byte, error) {
	if len(ip) < 20 || ip[0]>>4 != 4 || netip.AddrFrom4([4]byte(ip[16:20])) != t.DeviceIP {
		return nil, errors.New("返回数据包不是发给选定设备")
	}
	frame := make([]byte, 14+len(ip))
	copy(frame[:6], t.DeviceMAC)
	copy(frame[6:12], t.LocalMAC)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	copy(frame[14:], ip)
	if len(frame) < 60 {
		frame = append(frame, make([]byte, 60-len(frame))...)
	}
	return frame, nil
}

// ARPReply constructs a unicast reply exclusively for the selected device.
// restore=true reinstates the real gateway mapping; no packet is transmitted.
func (t Target) ARPReply(restore bool) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	advertised := t.LocalMAC
	if restore {
		advertised = t.GatewayMAC
	}
	frame := make([]byte, 60)
	copy(frame[:6], t.DeviceMAC)
	copy(frame[6:12], advertised)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	arp := frame[14:]
	binary.BigEndian.PutUint16(arp[:2], 1)
	binary.BigEndian.PutUint16(arp[2:4], 0x0800)
	arp[4], arp[5] = 6, 4
	binary.BigEndian.PutUint16(arp[6:8], 2)
	copy(arp[8:14], advertised)
	copy(arp[14:18], t.GatewayIP.AsSlice())
	copy(arp[18:24], t.DeviceMAC)
	copy(arp[24:28], t.DeviceIP.AsSlice())
	return frame, nil
}

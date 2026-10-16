package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
)

type ManualGateway struct {
	Target  Target
	Address netip.Addr
}

func (g ManualGateway) Validate() error {
	// This gateway never impersonates the router, so its MAC is unnecessary.
	for _, mac := range []net.HardwareAddr{g.Target.LocalMAC, g.Target.DeviceMAC} {
		if len(mac) != 6 || mac[0]&1 != 0 || bytes.Equal(mac, make([]byte, 6)) {
			return errors.New("需要有效的电脑和主机单播 MAC")
		}
	}
	if bytes.Equal(g.Target.LocalMAC, g.Target.DeviceMAC) ||
		!g.Target.DeviceIP.Is4() || !g.Target.DeviceIP.IsPrivate() {
		return errors.New("电脑与主机身份不能相同，主机须使用私有 IPv4")
	}
	if !g.Address.Is4() || !g.Address.IsPrivate() || g.Address == g.Target.DeviceIP ||
		g.Address == g.Target.GatewayIP {
		return errors.New("无效独立网关地址")
	}
	subnet := netip.PrefixFrom(g.Address, 30).Masked()
	if subnet.Addr().Next() != g.Address || subnet.Addr().Next().Next() != g.Target.DeviceIP {
		return errors.New("独立网关仅使用 /30 的两个主机地址")
	}
	return nil
}

// ReplyARP answers only a request by the confirmed PS5 for our virtual
// gateway. It does not advertise the router IP or alter anyone else's cache.
func (g ManualGateway) ReplyARP(frame []byte) ([]byte, bool) {
	if len(frame) < 42 || !bytes.Equal(frame[6:12], g.Target.DeviceMAC) ||
		(!bytes.Equal(frame[:6], g.Target.LocalMAC) && !bytes.Equal(frame[:6], []byte{255, 255, 255, 255, 255, 255})) ||
		binary.BigEndian.Uint16(frame[12:14]) != 0x0806 {
		return nil, false
	}
	arp := frame[14:42]
	if binary.BigEndian.Uint16(arp[:2]) != 1 || binary.BigEndian.Uint16(arp[2:4]) != 0x0800 ||
		arp[4] != 6 || arp[5] != 4 || binary.BigEndian.Uint16(arp[6:8]) != 1 ||
		!bytes.Equal(arp[8:14], g.Target.DeviceMAC) ||
		netip.AddrFrom4([4]byte(arp[14:18])) != g.Target.DeviceIP ||
		netip.AddrFrom4([4]byte(arp[24:28])) != g.Address {
		return nil, false
	}
	reply := make([]byte, 60)
	copy(reply[:6], g.Target.DeviceMAC)
	copy(reply[6:12], g.Target.LocalMAC)
	binary.BigEndian.PutUint16(reply[12:14], 0x0806)
	copy(reply[14:22], arp[:8])
	binary.BigEndian.PutUint16(reply[20:22], 2)
	copy(reply[22:28], g.Target.LocalMAC)
	copy(reply[28:32], g.Address.AsSlice())
	copy(reply[32:38], g.Target.DeviceMAC)
	copy(reply[38:42], g.Target.DeviceIP.AsSlice())
	return reply, true
}

// Input accepts only public IPv4 TCP/UDP destinations or DNS to this gateway.
// Host/LAN addresses are not exposed through the proxy. Fragments retain the
// same destination checks and are reassembled by the independent stack.
func (g ManualGateway) Input(frame []byte) ([]byte, bool) {
	ip, ok := g.Target.Input(frame)
	if !ok || checksum(ip[:int(ip[0]&15)*4]) != 0 {
		return nil, false
	}
	destination := netip.AddrFrom4([4]byte(ip[16:20]))
	if ip[9] != 6 && ip[9] != 17 {
		return nil, false
	}
	if destination == g.Address {
		// Fragmented gateway DNS is not admitted; ordinary DNS fits the MTU.
		if binary.BigEndian.Uint16(ip[6:8])&0x3fff != 0 {
			return nil, false
		}
		ihl := int(ip[0]&15) * 4
		if len(ip) < ihl+4 || binary.BigEndian.Uint16(ip[ihl+2:ihl+4]) != 53 {
			return nil, false
		}
	} else if !publicDestination(destination) {
		return nil, false
	}
	return ip, true
}

func publicDestination(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "198.18.0.0/15", "240.0.0.0/4"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}

// ReplyEcho answers only a gateway reachability check; internet RTT is never
// replaced with a synthetic ping reply.
func (g ManualGateway) ReplyEcho(frame []byte) ([]byte, bool) {
	ip, ok := g.Target.Input(frame)
	if !ok || ip[9] != 1 || netip.AddrFrom4([4]byte(ip[16:20])) != g.Address ||
		binary.BigEndian.Uint16(ip[6:8])&0x3fff != 0 {
		return nil, false
	}
	ihl := int(ip[0]&15) * 4
	if len(ip) < ihl+8 || ip[ihl] != 8 || ip[ihl+1] != 0 || checksum(ip[:ihl]) != 0 || checksum(ip[ihl:]) != 0 {
		return nil, false
	}
	reply := append([]byte(nil), ip...)
	copy(reply[12:16], g.Address.AsSlice())
	copy(reply[16:20], g.Target.DeviceIP.AsSlice())
	reply[8] = 64
	reply[ihl], reply[ihl+2], reply[ihl+3] = 0, 0, 0
	binary.BigEndian.PutUint16(reply[ihl+2:ihl+4], checksum(reply[ihl:]))
	reply[10], reply[11] = 0, 0
	binary.BigEndian.PutUint16(reply[10:12], checksum(reply[:ihl]))
	out, err := g.Target.Output(reply)
	return out, err == nil
}

func checksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data[:2]))
		data = data[2:]
	}
	if len(data) > 0 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

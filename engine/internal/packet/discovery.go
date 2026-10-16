package packet

import (
	"bytes"
	"net"
	"net/netip"
)

// DiscoverGatewayClient accepts the generated client IP requesting the exact
// gateway, or valid traffic already addressed to us after an ARP-cache hit.
func DiscoverGatewayClient(frame []byte, local net.HardwareAddr, deviceIP, gateway netip.Addr) (net.HardwareAddr, bool) {
	if len(frame) < 34 {
		return nil, false
	}
	mac := append(net.HardwareAddr(nil), frame[6:12]...)
	g := ManualGateway{Address: gateway, Target: Target{LocalMAC: local, DeviceMAC: mac, DeviceIP: deviceIP}}
	if g.Validate() != nil || bytes.Equal(mac, local) {
		return nil, false
	}
	_, valid := g.ReplyARP(frame)
	if !valid {
		_, valid = g.Input(frame)
	}
	if !valid {
		_, valid = g.ReplyEcho(frame)
	}
	return mac, valid
}

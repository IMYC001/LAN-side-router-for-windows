//go:build windows

package network

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"unsafe"
)

var sendARP = syscall.NewLazyDLL("iphlpapi.dll").NewProc("SendARP")

// ResolveMAC sends one Windows ARP request using the selected local IPv4
// address and returns the MAC reported for the target IPv4 address.
func ResolveMAC(targetIP, sourceIP string) (net.HardwareAddr, error) {
	target, err := netip.ParseAddr(targetIP)
	if err != nil || !target.Is4() {
		return nil, errors.New("ARP 目标必须是 IPv4 地址")
	}
	source, err := netip.ParseAddr(sourceIP)
	if err != nil || !source.Is4() {
		return nil, errors.New("ARP 源地址必须是 IPv4 地址")
	}

	targetBytes := target.As4()
	sourceBytes := source.As4()
	mac := make([]byte, 6)
	length := uint32(len(mac))
	ret, _, callErr := sendARP.Call(
		uintptr(binary.LittleEndian.Uint32(targetBytes[:])),
		uintptr(binary.LittleEndian.Uint32(sourceBytes[:])),
		uintptr(unsafe.Pointer(&mac[0])),
		uintptr(unsafe.Pointer(&length)),
	)
	if ret != 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return nil, fmt.Errorf("Windows ARP 查询失败: %w", callErr)
		}
		return nil, fmt.Errorf("Windows ARP 查询失败: 错误码 %d", ret)
	}
	if length != 6 {
		return nil, fmt.Errorf("Windows ARP 返回了无效 MAC 长度 %d", length)
	}
	return net.HardwareAddr(append([]byte(nil), mac...)), nil
}

//go:build windows

package nodelatency

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func boundDialer(request Request, network string) *net.Dialer {
	dialer := &net.Dialer{Timeout: 2 * time.Second, Control: func(_, _ string, raw syscall.RawConn) error {
		if request.Index <= 0 {
			return errors.New("无效网卡索引")
		}
		var optionErr error
		err := raw.Control(func(fd uintptr) {
			// IP_UNICAST_IF expects the interface index in network byte order.
			index := uint32(request.Index)
			var encoded [4]byte
			binary.BigEndian.PutUint32(encoded[:], index)
			optionErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, 31,
				int(binary.LittleEndian.Uint32(encoded[:])))
		})
		if err != nil {
			return err
		}
		return optionErr
	}}
	if network == "udp" || network == "udp4" {
		dialer.LocalAddr = &net.UDPAddr{IP: net.ParseIP(request.Source)}
	} else {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(request.Source)}
	}
	return dialer
}

var (
	ipHelper   = windows.NewLazySystemDLL("iphlpapi.dll")
	icmpCreate = ipHelper.NewProc("IcmpCreateFile")
	icmpClose  = ipHelper.NewProc("IcmpCloseHandle")
	icmpSend   = ipHelper.NewProc("IcmpSendEcho2Ex")
)

func probe(ctx context.Context, request Request, ip netip.Addr, port int, udp bool) (time.Duration, error) {
	if !udp {
		start := time.Now()
		conn, err := boundDialer(request, "tcp4").DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		elapsed := time.Since(start)
		if err != nil {
			return 0, err
		}
		conn.Close()
		return elapsed, nil
	}
	// UDP protocols have no TCP handshake. ICMP may be blocked; do not invent a TCP RTT.
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	handle, _, _ := icmpCreate.Call()
	if handle == ^uintptr(0) {
		return 0, errors.New("无法创建 ICMP 检测")
	}
	defer icmpClose.Call(handle)
	source := netip.MustParseAddr(request.Source).As4()
	dest := ip.As4()
	payload := [16]byte{0x41, 0x43, 0x43}
	reply := [512]byte{}
	timeout := 1000
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, max(1, int(time.Until(deadline).Milliseconds())))
	}
	count, _, _ := icmpSend.Call(handle, 0, 0, 0,
		uintptr(binary.LittleEndian.Uint32(source[:])), uintptr(binary.LittleEndian.Uint32(dest[:])),
		uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)), 0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(timeout))
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if count == 0 || binary.LittleEndian.Uint32(reply[4:8]) != 0 ||
		binary.LittleEndian.Uint32(reply[0:4]) != binary.LittleEndian.Uint32(dest[:]) {
		return 0, errors.New("节点未响应 ICMP")
	}
	return time.Duration(binary.LittleEndian.Uint32(reply[8:12])) * time.Millisecond, nil
}

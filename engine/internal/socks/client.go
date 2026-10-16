package socks

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// Client deliberately accepts only a loopback SOCKS server. Its upstream policy
// remains the responsibility of Clash; this adapter never changes host routes.
type Client struct {
	Address string
}

func (c Client) Validate() error {
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil {
		return errors.New("代理入口必须是 127.0.0.1:端口或 [::1]:端口")
	}
	ip, err := netip.ParseAddr(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || !ip.IsLoopback() || portErr != nil || n < 1 || n > 65535 {
		return errors.New("只允许本机回环 SOCKS5 入口，端口范围为 1–65535")
	}
	return nil
}

func (c Client) command(ctx context.Context, command byte, dst string) (net.Conn, string, error) {
	if err := c.Validate(); err != nil {
		return nil, "", err
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return nil, "", fmt.Errorf("SOCKS5 入口连接失败: %w", err)
	}
	// This is process-local buffering only; it does not change Windows
	// adapter settings. It prevents the loopback leg from becoming the
	// bottleneck while the remote node has a large bandwidth-delay product.
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(4 << 20)
		_ = tcp.SetWriteBuffer(4 << 20)
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		return nil, "", err
	}
	var greeting [2]byte
	if _, err = io.ReadFull(conn, greeting[:]); err != nil {
		return nil, "", err
	}
	if greeting != [2]byte{5, 0} {
		return nil, "", errors.New("入口不是 SOCKS5，或要求认证；当前版本支持无认证的本机入口")
	}
	addr, err := EncodeAddress(dst)
	if err != nil {
		return nil, "", err
	}
	if _, err = conn.Write(append([]byte{5, command, 0}, addr...)); err != nil {
		return nil, "", err
	}
	var reply [3]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil {
		return nil, "", err
	}
	if reply[0] != 5 || reply[1] != 0 || reply[2] != 0 {
		return nil, "", fmt.Errorf("SOCKS5 请求被拒绝，状态码 %d", reply[1])
	}
	bound, err := ReadAddress(conn)
	if err != nil {
		return nil, "", err
	}
	conn.SetDeadline(time.Time{})
	success = true
	return conn, bound, nil
}

func (c Client) DialTCP(ctx context.Context, dst string) (net.Conn, error) {
	conn, _, err := c.command(ctx, 1, dst)
	return conn, err
}

type UDPAssociation struct {
	control net.Conn
	socket  *net.UDPConn
}

func (c Client) AssociateUDP(ctx context.Context) (*UDPAssociation, error) {
	control, bound, err := c.command(ctx, 3, "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	endpoint, err := netip.ParseAddrPort(bound)
	if err != nil || endpoint.Port() == 0 {
		control.Close()
		return nil, errors.New("SOCKS5 返回了无效 UDP 入口")
	}
	if endpoint.Addr().IsUnspecified() {
		host, _, _ := net.SplitHostPort(c.Address)
		ip, _ := netip.ParseAddr(host)
		endpoint = netip.AddrPortFrom(ip, endpoint.Port())
	}
	if !endpoint.Addr().IsLoopback() {
		control.Close()
		return nil, errors.New("SOCKS5 UDP 入口不是回环地址，已拒绝以避免被本机 TUN 再次接管")
	}
	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(endpoint))
	if err != nil {
		control.Close()
		return nil, err
	}
	_ = conn.SetReadBuffer(4 << 20)
	_ = conn.SetWriteBuffer(4 << 20)
	a := &UDPAssociation{control: control, socket: conn}
	go func() {
		// RFC 1928: the UDP association ends when its TCP control closes.
		io.Copy(io.Discard, control)
		conn.Close()
	}()
	return a, nil
}

func (a *UDPAssociation) WriteTo(payload []byte, destination string) error {
	addr, err := EncodeAddress(destination)
	if err != nil {
		return err
	}
	if len(payload)+len(addr)+3 > 65507 {
		return errors.New("UDP 数据报超过允许长度")
	}
	frame := make([]byte, 3+len(addr)+len(payload))
	copy(frame[3:], addr)
	copy(frame[3+len(addr):], payload)
	_, err = a.socket.Write(frame)
	return err
}

func (a *UDPAssociation) ReadFrom(buffer []byte) ([]byte, string, error) {
	n, err := a.socket.Read(buffer)
	if err != nil {
		return nil, "", err
	}
	return DecodeDatagram(buffer[:n])
}

func (a *UDPAssociation) SetDeadline(deadline time.Time) error {
	return a.socket.SetDeadline(deadline)
}

func (a *UDPAssociation) Close() error {
	a.socket.Close()
	return a.control.Close()
}

func EncodeAddress(address string) ([]byte, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return nil, errors.New("无效目标端口")
	}
	var result []byte
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.Is4() {
			result = append([]byte{1}, ip.AsSlice()...)
		} else {
			result = append([]byte{4}, ip.AsSlice()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, errors.New("无效域名")
		}
		result = append([]byte{3, byte(len(host))}, []byte(host)...)
	}
	return binary.BigEndian.AppendUint16(result, uint16(n)), nil
}

func ReadAddress(r io.Reader) (string, error) {
	var kind [1]byte
	if _, err := io.ReadFull(r, kind[:]); err != nil {
		return "", err
	}
	size := 0
	switch kind[0] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return "", err
		}
		size = int(length[0])
		if size == 0 {
			return "", errors.New("空域名")
		}
	default:
		return "", errors.New("未知 SOCKS5 地址类型")
	}
	bytes := make([]byte, size+2)
	if _, err := io.ReadFull(r, bytes); err != nil {
		return "", err
	}
	host := string(bytes[:size])
	if kind[0] != 3 {
		ip, ok := netip.AddrFromSlice(bytes[:size])
		if !ok {
			return "", errors.New("无效 IP 地址")
		}
		host = ip.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(bytes[size:])))), nil
}

func DecodeDatagram(frame []byte) ([]byte, string, error) {
	if len(frame) < 4 || frame[0] != 0 || frame[1] != 0 {
		return nil, "", errors.New("无效 SOCKS5 UDP 数据报")
	}
	if frame[2] != 0 {
		return nil, "", errors.New("不支持 SOCKS5 UDP 分片")
	}
	reader := &countingReader{bytes: frame[3:]}
	address, err := ReadAddress(reader)
	if err != nil {
		return nil, "", err
	}
	return frame[3+reader.offset:], address, nil
}

type countingReader struct {
	bytes  []byte
	offset int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.bytes) {
		return 0, io.EOF
	}
	n := copy(p, r.bytes[r.offset:])
	r.offset += n
	return n, nil
}

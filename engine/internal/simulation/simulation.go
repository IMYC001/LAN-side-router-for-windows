package simulation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/socks"
	"accelerator/engine/internal/testproxy"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

type Result struct {
	TCPBytes      int         `json:"tcpBytes"`
	UDPSizes      []int       `json:"udpSizes"`
	Stats         relay.Stats `json:"stats"`
	NetworkWrites int         `json:"networkWrites"`
	Scope         string      `json:"scope"`
}

// Run never opens a physical interface. All target IPs are documentation-only
// addresses inside two in-memory IP stacks; the SOCKS echo binds loopback.
func Run(parent context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	server, err := testproxy.Start()
	if err != nil {
		return Result{}, err
	}
	defer server.Close()
	engine, err := relay.New(socks.Client{Address: server.Address()})
	if err != nil {
		return Result{}, err
	}
	defer engine.Close()
	client, cleanup, err := makeDevice(ctx, engine)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	stopClose := context.AfterFunc(ctx, engine.Close)
	defer stopClose()
	result := Result{Scope: "内存协议栈 + 本机回环模拟 SOCKS5。没有捕获网卡、发送 ARP、访问公网或测试真实 PS5。", UDPSizes: []int{}}
	tcpConn, err := gonet.DialContextTCP(ctx, client, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}, ipv4.ProtocolNumber)
	if err != nil {
		return result, err
	}
	defer tcpConn.Close()
	tcpConn.SetDeadline(time.Now().Add(5 * time.Second))
	payload := bytes.Repeat([]byte("PS5 TCP echo "), 2000)
	if _, err := tcpConn.Write(payload); err != nil {
		return result, err
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(tcpConn, reply); err != nil {
		return result, err
	}
	if !bytes.Equal(reply, payload) {
		return result, fmt.Errorf("TCP 往返内容不一致")
	}
	result.TCPBytes = len(payload)
	tcpConn.Close()
	remote := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 53}), Port: 3074}
	udpConn, err := gonet.DialUDP(client, nil, &remote, ipv4.ProtocolNumber)
	if err != nil {
		return result, err
	}
	defer udpConn.Close()
	udpConn.SetDeadline(time.Now().Add(5 * time.Second))
	for _, size := range []int{1, 64, 1200, 4096} {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		payload := bytes.Repeat([]byte{0x3A}, size)
		if _, err := udpConn.Write(payload); err != nil {
			return result, err
		}
		reply := make([]byte, 65535)
		n, err := udpConn.Read(reply)
		if err != nil {
			return result, fmt.Errorf("UDP %d 字节: %w", size, err)
		}
		if !bytes.Equal(payload, reply[:n]) {
			return result, fmt.Errorf("UDP %d 字节往返内容不一致", size)
		}
		result.UDPSizes = append(result.UDPSizes, size)
	}
	result.Stats = engine.Snapshot()
	if result.Stats.Failed != 0 {
		return result, fmt.Errorf("模拟转发存在失败会话")
	}
	return result, nil
}

func makeDevice(ctx context.Context, engine *relay.Engine) (*stack.Stack, func(), error) {
	link := channel.New(1024, 1500, "")
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(1, link); err != nil {
		s.Close()
		link.Close()
		return nil, nil, fmt.Errorf("模拟网卡: %s", err)
	}
	address := tcpip.AddrFrom4([4]byte{192, 168, 1, 35})
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddressWithPrefix{Address: address, PrefixLen: 24}}, stack.AddressProperties{}); err != nil {
		s.Close()
		link.Close()
		return nil, nil, fmt.Errorf("模拟地址: %s", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	pumpCtx, cancel := context.WithCancel(ctx)
	doneA, doneB := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(doneA)
		for {
			packet := link.ReadContext(pumpCtx)
			if packet == nil {
				return
			}
			b := packet.ToBuffer()
			data := append([]byte(nil), b.Flatten()...)
			b.Release()
			packet.DecRef()
			engine.Inject(data)
		}
	}()
	go func() {
		defer close(doneB)
		for {
			data, err := engine.ReadPacket(pumpCtx)
			if err != nil {
				return
			}
			packet := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(data)})
			link.InjectInbound(ipv4.ProtocolNumber, packet)
			packet.DecRef()
		}
	}()
	cleanup := func() {
		cancel()
		link.Close()
		engine.Close()
		s.Close()
		<-doneA
		<-doneB
		s.Wait()
	}
	return s, cleanup, nil
}

package simulation

import (
	"bytes"
	"context"
	"io"
	"net/netip"
	"testing"
	"time"

	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/socks"
	"accelerator/engine/internal/testproxy"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

func TestCancellationClosesSimulation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx); err == nil {
		t.Fatal("canceled simulation succeeded")
	}
}

func TestGatewayDNSRoundtripUDPAndTCP(t *testing.T) {
	server, err := testproxy.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	gateway := netip.MustParseAddr("10.254.83.1")
	engine, err := relay.NewWithDNS(socks.Client{Address: server.Address()}, gateway, "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	device, cleanup, err := makeDevice(ctx, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	remote := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(gateway.As4()), Port: 53}
	payload := []byte{0x01, 0x23, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	udp, err := gonet.DialUDP(device, nil, &remote, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := udp.Write(payload); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 512)
	n, from, err := udp.ReadFrom(buffer)
	if err != nil || !bytes.Equal(buffer[:n], payload) || from.String() != gateway.String()+":53" {
		t.Fatalf("DNS UDP return changed source or payload: %v %v", from, err)
	}
	conn, err := gonet.DialContextTCP(ctx, device, remote, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	message := append([]byte{0, byte(len(payload))}, payload...)
	if _, err := conn.Write(message); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(message))
	if _, err := io.ReadFull(conn, reply); err != nil || !bytes.Equal(reply, message) {
		t.Fatalf("DNS TCP return failed: %v", err)
	}
}

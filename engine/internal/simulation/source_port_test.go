package simulation

import (
	"bytes"
	"context"
	"net"
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

func TestUDPSourcePortReusesAssociationAcrossDestinations(t *testing.T) {
	for _, dns := range []bool{false, true} {
		t.Run(map[bool]string{false: "game", true: "gateway-dns"}[dns], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			proxy, err := testproxy.Start()
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			var engine *relay.Engine
			if dns {
				engine, err = relay.NewWithDNS(socks.Client{Address: proxy.Address()},
					netip.MustParseAddr("10.254.83.1"), "1.1.1.1:53")
			} else {
				engine, err = relay.New(socks.Client{Address: proxy.Address()})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			device, cleanup, err := makeDevice(ctx, engine)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			stop := context.AfterFunc(ctx, engine.Close)
			defer stop()
			source := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{192, 168, 1, 35}), Port: 3074}
			conn, err := gonet.DialUDP(device, &source, nil, ipv4.ProtocolNumber)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			destinations := []*net.UDPAddr{
				{IP: net.ParseIP("203.0.113.9"), Port: 3478},
				{IP: net.ParseIP("203.0.113.10"), Port: 3479},
			}
			wantAssociations := uint64(1)
			if dns {
				destinations = []*net.UDPAddr{
					{IP: net.ParseIP("10.254.83.1"), Port: 53},
					{IP: net.ParseIP("1.1.1.1"), Port: 53},
				}
				wantAssociations = 2
			}
			payloads := [][]byte{[]byte("first-target"), []byte("second-target")}
			// Send both before reading to exercise concurrent association setup.
			for i, destination := range destinations {
				if _, err := conn.WriteTo(payloads[i], destination); err != nil {
					t.Fatal(err)
				}
			}
			seen := map[string]bool{}
			for range destinations {
				buf := make([]byte, 65535)
				n, from, err := conn.ReadFrom(buf)
				if err != nil {
					t.Fatal(err)
				}
				matched := false
				for i, destination := range destinations {
					if from.String() == destination.String() && bytes.Equal(buf[:n], payloads[i]) {
						seen[from.String()] = true
						matched = true
					}
				}
				if !matched {
					t.Fatalf("wrong destination or payload: %v %s", from, buf[:n])
				}
			}
			if len(seen) != 2 || proxy.UDPAssociations() != wantAssociations ||
				engine.Snapshot().UDPAssociations != wantAssociations {
				t.Fatalf("mapping mismatch: %v proxy=%d stats=%+v", seen, proxy.UDPAssociations(), engine.Snapshot())
			}
			// Another PS5 source port must not share the first port's socket.
			source.Port++
			second, err := gonet.DialUDP(device, &source, nil, ipv4.ProtocolNumber)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			second.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := second.WriteTo([]byte("other-source"), destinations[0]); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 64)
			n, from, err := second.ReadFrom(buf)
			if err != nil || string(buf[:n]) != "other-source" || from.String() != destinations[0].String() {
				t.Fatalf("separate source reply: %v %v", from, err)
			}
			if proxy.UDPAssociations() != wantAssociations+1 || engine.Snapshot().Failed != 0 {
				t.Fatalf("sources mixed: proxy=%d stats=%+v", proxy.UDPAssociations(), engine.Snapshot())
			}
			engine.Close()
			if err := ctx.Err(); err != nil {
				t.Fatalf("shutdown exceeded deadline: %v", err)
			}
		})
	}
}

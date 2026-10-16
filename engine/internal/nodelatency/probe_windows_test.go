package nodelatency

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestBoundWindowsTCPProbeOnLoopback(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			index = iface.Index
			break
		}
	}
	if index == 0 {
		t.Fatal("missing loopback interface")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			conn.Close()
		}
		done <- e
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	elapsed, err := probe(ctx, Request{Index: index, Source: "127.0.0.1"}, netip.MustParseAddr("127.0.0.1"),
		listener.Addr().(*net.TCPAddr).Port, false)
	if err != nil || elapsed < 0 {
		t.Fatalf("bound TCP probe failed: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := probe(ctx, Request{Index: -1, Source: "127.0.0.1"}, netip.MustParseAddr("127.0.0.1"),
		listener.Addr().(*net.TCPAddr).Port, false); err == nil {
		t.Fatal("invalid interface did not fail closed")
	}
}

func TestSourceBoundICMPLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := probe(ctx, Request{Source: "127.0.0.1"}, netip.MustParseAddr("127.0.0.1"), 0, true); err != nil {
		t.Fatalf("native ICMP reply parsing failed: %v", err)
	}
}

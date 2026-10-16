package relay

import (
	"net/netip"
	"testing"

	"accelerator/engine/internal/socks"
	"accelerator/engine/internal/testproxy"
)

func TestSharedUDPReferenceLifetime(t *testing.T) {
	proxy, err := testproxy.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	engine, err := New(socks.Client{Address: proxy.Address()})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	key := udpSource{Address: netip.MustParseAddrPort("192.168.1.35:3074")}
	first, err := engine.acquireUDP(key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.acquireUDP(key)
	if err != nil || first != second || proxy.UDPAssociations() != 1 {
		t.Fatalf("source association not shared: %v %d", err, proxy.UDPAssociations())
	}
	engine.releaseUDP(first, netip.AddrPort{}, nil)
	engine.udpMu.Lock()
	retained := engine.udpSessions[key] == second && !second.stopped && second.users == 1
	engine.udpMu.Unlock()
	if !retained {
		t.Fatal("one destination closed the sibling's mapping")
	}
	engine.releaseUDP(second, netip.AddrPort{}, nil)
	engine.udpMu.Lock()
	removed := engine.udpSessions[key] == nil && second.stopped
	engine.udpMu.Unlock()
	if !removed {
		t.Fatal("last destination did not remove the mapping")
	}
	third, err := engine.acquireUDP(key)
	if err != nil || third == first || proxy.UDPAssociations() != 2 {
		t.Fatalf("expired mapping reused: %v %d", err, proxy.UDPAssociations())
	}
	engine.releaseUDP(third, netip.AddrPort{}, nil)
	engine.Close()
	if _, err := engine.acquireUDP(key); err == nil {
		t.Fatal("closed engine created a UDP mapping")
	}
}

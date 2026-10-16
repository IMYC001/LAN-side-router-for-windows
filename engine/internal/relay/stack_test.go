package relay_test

import (
	"context"
	"testing"

	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/simulation"
	"accelerator/engine/internal/socks"
)

func TestSimulatedDeviceTCPAndUDP(t *testing.T) {
	result, err := simulation.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TCPBytes != 26000 || len(result.UDPSizes) != 4 || result.NetworkWrites != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	stats := result.Stats
	if stats.TCPStarted != 1 || stats.UDPStarted != 1 || stats.Failed != 0 || stats.Downloaded < uint64(26000+1+64+1200+4096) {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestCloseRejectsFurtherPackets(t *testing.T) {
	engine, err := relay.New(socks.Client{Address: "127.0.0.1:12345"})
	if err != nil {
		t.Fatal(err)
	}
	engine.Close()
	engine.Close()
	ip := make([]byte, 20)
	ip[0] = 0x45
	if engine.Inject(ip) == nil {
		t.Fatal("closed engine accepted a packet")
	}
}

package relay

import (
	"net/netip"
	"testing"

	"accelerator/engine/internal/socks"
)

func TestOnlyGatewayDNSIsRemapped(t *testing.T) {
	e, err := NewWithDNS(socks.Client{Address: "127.0.0.1:7897"}, netip.MustParseAddr("10.254.83.1"), "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for original, want := range map[string]string{
		"10.254.83.1:53": "1.1.1.1:53",
		"10.254.83.1:80": "10.254.83.1:80",
		"8.8.8.8:53":     "8.8.8.8:53",
	} {
		if got := e.destination(original); got != want {
			t.Fatalf("%s mapped to %s", original, got)
		}
	}
}

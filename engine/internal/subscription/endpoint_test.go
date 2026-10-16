package subscription

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestNodeEndpointProtocolAndSafeFailures(t *testing.T) {
	for _, protocol := range []string{"anytls", "ss", "hysteria", "hysteria2", "tuic"} {
		config := fmt.Sprintf("proxies: [{name: game, type: %s, server: 203.0.113.1, port: 443, password: TOP_SECRET}]", protocol)
		endpoint, err := NodeEndpoint([]byte(config), "game")
		if err != nil || endpoint.Server != "203.0.113.1" || endpoint.Port != 443 ||
			endpoint.UDPOnly != (strings.HasPrefix(protocol, "hysteria") || protocol == "tuic") {
			t.Fatalf("protocol %s: %+v %v", protocol, endpoint, err)
		}
	}
	for _, fields := range []string{
		"server: 198.18.0.1, port: 443", "server: 192.168.1.1, port: 443",
		"server: 100.64.0.1, port: 443", "server: localhost, port: 443",
		"server: box.local, port: 443", "server: 203.0.113.1, port: 0",
		"server: 203.0.113.1, port: 443, dialer-proxy: other",
	} {
		config := "proxies: [{name: game, type: anytls, password: TOP_SECRET, " + fields + "}]"
		_, err := NodeEndpoint([]byte(config), "game")
		if err == nil || strings.Contains(err.Error(), "TOP_SECRET") {
			t.Fatal("unsafe endpoint accepted or secret leaked")
		}
	}
	if PublicIPv4(netip.MustParseAddr("::ffff:198.18.0.1")) {
		t.Fatal("mapped fake IP accepted")
	}
}

package subscription

import (
	"strings"
	"testing"
)

func TestParseNodesWithoutExecutingConfig(t *testing.T) {
	summary, err := Parse([]byte("tun: {enable: true}\nproxies:\n  - {name: game, type: ss, udp: true, server: example.com, password: secret}\nproxy-groups:\n  - {name: PS5, type: select, proxies: [game]}\n"))
	if err != nil || summary.Nodes != 1 || summary.UDPMarked != 1 || summary.Groups != 1 {
		t.Fatalf("unexpected result: %+v %v", summary, err)
	}
}

func TestRejectInvalidAndDuplicateNodes(t *testing.T) {
	for _, content := range []string{
		"not a configuration",
		"proxies: []",
		"proxies: [{name: a, type: ss}, {name: a, type: ss}]",
		"proxies: [{name: a, type: ss}]\n---\ntun: {enable: true}",
		"proxies: [{name: a, type: ss, password: TOP_SECRET",
	} {
		_, err := Parse([]byte(content))
		if err == nil {
			t.Fatalf("accepted %q", content)
		}
		if strings.Contains(err.Error(), "TOP_SECRET") {
			t.Fatal("parser error exposed a credential")
		}
	}
}

package subscription

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func outboundRequest() OutboundRequest {
	return OutboundRequest{Node: "game", Interface: "WLAN", SOCKSPort: 32101,
		ControllerPort: 32102, Secret: strings.Repeat("a", 64)}
}

func TestOutboundIgnoresSubscriptionNetworkSettings(t *testing.T) {
	content := []byte(`tun: {enable: true, auto-route: true}
listeners: [{name: unsafe, type: mixed, port: 80, listen: "0.0.0.0"}]
external-controller: "0.0.0.0:80"
script: {code: unsafe}
rules: ["MATCH,DIRECT"]
proxies:
  - {name: game, type: anytls, server: example.com, port: 443, password: secret, udp: true, interface-name: Meta, routing-mark: 666}
  - {name: unrelated, type: ss, server: unrelated.example, port: 443}
`)
	result, err := BuildOutbound(content, outboundRequest())
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(result.Config), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["allow-lan"] != false || cfg["bind-address"] != "127.0.0.1" ||
		cfg["interface-name"] != "WLAN" || cfg["mode"] != "rule" ||
		cfg["external-controller"] != "127.0.0.1:32102" {
		t.Fatal("runtime isolation settings changed")
	}
	if cfg["tun"].(map[string]any)["enable"] != false {
		t.Fatal("subscription enabled TUN")
	}
	for _, key := range []string{"listeners", "script", "proxy-providers", "tunnels"} {
		if _, exists := cfg[key]; exists {
			t.Fatalf("subscription injected %s", key)
		}
	}
	nodes := cfg["proxies"].([]any)
	if len(nodes) != 1 {
		t.Fatal("unselected nodes copied")
	}
	node := nodes[0].(map[string]any)
	if node["password"] != "secret" || node["name"] != "PS5-EXIT" ||
		node["interface-name"] != "WLAN" || node["ip-version"] != "ipv4" {
		t.Fatal("selected node or interface binding incorrect")
	}
	if _, exists := node["routing-mark"]; exists {
		t.Fatal("subscription routing mark retained")
	}
}

func TestOutboundRejectsUnsupportedOrLocalNodesWithoutCredentials(t *testing.T) {
	for _, node := range []string{
		"{name: game, type: direct, server: example.com, port: 443}",
		"{name: game, type: anytls, server: 127.0.0.1, port: 443}",
		"{name: game, type: anytls, server: 192.168.1.1, port: 443}",
		"{name: game, type: anytls, server: 198.18.0.1, port: 443}",
		"{name: game, type: anytls, server: example.com, port: 443, dialer-proxy: OTHER}",
		"{name: game, type: ss, server: example.com, port: 443, plugin: CUSTOM}",
		"{name: game, type: anytls, server: example.com, port: 0, password: SECRET}",
	} {
		_, err := BuildOutbound([]byte("proxies: ["+node+"]"), outboundRequest())
		if err == nil {
			t.Fatalf("unsupported node accepted: %s", node)
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Fatal("error exposed credentials")
		}
	}
	request := outboundRequest()
	request.ControllerPort = request.SOCKSPort
	if _, err := BuildOutbound([]byte("proxies: [{name: game, type: ss}]"), request); err == nil {
		t.Fatal("same controller and SOCKS port accepted")
	}
}

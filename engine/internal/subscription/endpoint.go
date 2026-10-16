package subscription

import (
	"bytes"
	"errors"
	"net/netip"
	"strings"

	"gopkg.in/yaml.v3"
)

type Endpoint struct {
	Server  string
	Port    int
	UDPOnly bool
}

// Extract addresses only; credentials and subscription routing are never executed.
func NodeEndpoint(content []byte, name string) (Endpoint, error) {
	if _, err := Parse(content); err != nil {
		return Endpoint{}, err
	}
	var document struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.NewDecoder(bytes.NewReader(content)).Decode(&document); err != nil {
		return Endpoint{}, errors.New("无法读取节点地址")
	}
	for _, node := range document.Proxies {
		if node["name"] != name {
			continue
		}
		kind, _ := node["type"].(string)
		server, _ := node["server"].(string)
		port, _ := node["port"].(int)
		switch kind {
		case "ss", "ssr", "vmess", "vless", "trojan", "anytls", "snell", "socks5", "http",
			"hysteria", "hysteria2", "tuic":
		default:
			return Endpoint{}, errors.New("节点不支持直接延迟检测")
		}
		if server == "" || port < 1 || port > 65535 || strings.ContainsAny(server, "/\\\r\n\x00") ||
			strings.EqualFold(server, "localhost") || strings.HasSuffix(strings.ToLower(server), ".local") {
			return Endpoint{}, errors.New("节点地址不支持直接延迟检测")
		}
		if _, exists := node["dialer-proxy"]; exists {
			return Endpoint{}, errors.New("链式节点无法测量直达延迟")
		}
		if ip, err := netip.ParseAddr(server); err == nil && !PublicIPv4(ip) {
			return Endpoint{}, errors.New("节点需要公网 IPv4 地址")
		}
		return Endpoint{Server: server, Port: port, UDPOnly: kind == "hysteria" || kind == "hysteria2" || kind == "tuic"}, nil
	}
	return Endpoint{}, errors.New("找不到所选节点")
}

func PublicIPv4(ip netip.Addr) bool {
	return ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) &&
		!netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
}

package subscription

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type OutboundRequest struct {
	Node           string `json:"node"`
	Interface      string `json:"interface"`
	SOCKSPort      int    `json:"socksPort"`
	ControllerPort int    `json:"controllerPort"`
	Secret         string `json:"secret"`
}

type Outbound struct {
	Config string `json:"config"`
	Node   string `json:"node"`
}

// Extract only a selected outbound. Subscription listeners, rules, providers,
// TUN settings and controllers never become part of the executable config.
func BuildOutbound(content []byte, request OutboundRequest) (Outbound, error) {
	if _, err := Parse(content); err != nil {
		return Outbound{}, err
	}
	if request.Interface == "" || strings.ContainsAny(request.Interface, "\r\n\x00") ||
		request.SOCKSPort < 1024 || request.SOCKSPort > 65535 ||
		request.ControllerPort < 1024 || request.ControllerPort > 65535 ||
		request.SOCKSPort == request.ControllerPort || len(request.Secret) < 32 {
		return Outbound{}, errors.New("独立出口的网卡、端口或控制密钥无效")
	}
	var document struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.NewDecoder(bytes.NewReader(content)).Decode(&document); err != nil {
		return Outbound{}, errors.New("无法读取节点配置")
	}
	var node map[string]any
	for _, candidate := range document.Proxies {
		if candidate["name"] == request.Node {
			node = candidate
			break
		}
	}
	if node == nil {
		return Outbound{}, errors.New("请选择订阅中实际存在的节点；仅 provider 的订阅需先导出完整 YAML")
	}
	allowed := map[string]bool{
		"ss": true, "ssr": true, "vmess": true, "vless": true, "trojan": true,
		"anytls": true, "hysteria": true, "hysteria2": true, "tuic": true,
		"snell": true, "socks5": true, "http": true,
	}
	kind, _ := node["type"].(string)
	server, _ := node["server"].(string)
	port, _ := node["port"].(int)
	if !allowed[kind] || server == "" || port < 1 || port > 65535 || strings.ContainsAny(server, "/\\\r\n\x00") {
		return Outbound{}, errors.New("该节点类型或服务器配置暂不支持独立出口")
	}
	if ip, err := netip.ParseAddr(server); err == nil {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			netip.MustParsePrefix("100.64.0.0/10").Contains(ip) ||
			netip.MustParsePrefix("198.18.0.0/15").Contains(ip) {
			return Outbound{}, errors.New("节点服务器不能使用本地、私网或 fake-IP 地址")
		}
	} else if strings.EqualFold(server, "localhost") || strings.HasSuffix(strings.ToLower(server), ".local") {
		return Outbound{}, errors.New("节点服务器不能使用本地地址")
	}
	if _, exists := node["dialer-proxy"]; exists {
		return Outbound{}, errors.New("暂不支持需要其他节点拨号的链式节点")
	}
	if plugin, ok := node["plugin"].(string); ok && plugin != "" && plugin != "obfs" && plugin != "v2ray-plugin" {
		return Outbound{}, errors.New("该节点插件暂不支持")
	}
	delete(node, "routing-mark")
	node["interface-name"] = request.Interface
	node["ip-version"] = "ipv4"
	node["name"] = "PS5-EXIT"
	config := map[string]any{
		"socks-port": request.SOCKSPort, "port": 0, "mixed-port": 0,
		"allow-lan": false, "bind-address": "127.0.0.1",
		"mode": "rule", "ipv6": false, "log-level": "silent",
		"interface-name":      request.Interface,
		"external-controller": net.JoinHostPort("127.0.0.1", strconv.Itoa(request.ControllerPort)),
		"secret":              request.Secret,
		"tun":                 map[string]any{"enable": false},
		"profile":             map[string]any{"store-selected": false, "store-fake-ip": false},
		"dns": map[string]any{
			"enable": true, "ipv6": false, "enhanced-mode": "redir-host",
			"nameserver":              []string{"1.1.1.1", "8.8.8.8"},
			"default-nameserver":      []string{"1.1.1.1", "8.8.8.8"},
			"proxy-server-nameserver": []string{"1.1.1.1", "8.8.8.8"},
			"respect-rules":           false,
		},
		"proxies": []map[string]any{node},
		"rules": []string{
			"IP-CIDR,127.0.0.0/8,REJECT,no-resolve",
			"IP-CIDR,10.0.0.0/8,REJECT,no-resolve",
			"IP-CIDR,172.16.0.0/12,REJECT,no-resolve",
			"IP-CIDR,192.168.0.0/16,REJECT,no-resolve",
			"MATCH,PS5-EXIT",
		},
	}
	encoded, err := yaml.Marshal(config)
	if err != nil {
		return Outbound{}, errors.New("无法生成独立出口配置")
	}
	return Outbound{Config: string(encoded), Node: request.Node}, nil
}

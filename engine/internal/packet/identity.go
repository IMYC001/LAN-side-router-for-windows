package packet

import (
	"encoding/binary"
	"strings"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

// IdentityHint observes only validated client packets. Service traffic is a
// hint, never proof of a hardware model; no raw domains are retained.
type IdentityHint struct {
	mu       sync.Mutex
	families uint8
}

func (h *IdentityHint) Observe(ip []byte) {
	if len(ip) < 20 || ip[0]>>4 != 4 || ip[9] != 17 || binary.BigEndian.Uint16(ip[6:8])&0x3fff != 0 {
		return
	}
	ihl := int(ip[0]&15) * 4
	length := int(binary.BigEndian.Uint16(ip[2:4]))
	if ihl < 20 || length > len(ip) || length < ihl+8 {
		return
	}
	udp := ip[ihl:length]
	size := int(binary.BigEndian.Uint16(udp[4:6]))
	if binary.BigEndian.Uint16(udp[2:4]) != 53 || size < 20 || size > len(udp) || size > 4104 {
		return
	}
	var parser dnsmessage.Parser
	header, err := parser.Start(udp[8:size])
	if err != nil || header.Response || header.OpCode != 0 {
		return
	}
	question, err := parser.Question()
	if err != nil || question.Class != dnsmessage.ClassINET {
		return
	}
	name := strings.TrimSuffix(strings.ToLower(question.Name.String()), ".")
	var family uint8
	for _, suffix := range []string{"playstation.net", "playstation.com", "sonyentertainmentnetwork.com"} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			family = 1
		}
	}
	for _, suffix := range []string{"xboxlive.com", "xbox.com"} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			family = 2
		}
	}
	for _, suffix := range []string{"nintendo.net", "nintendo.com"} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			family = 4
		}
	}
	if family != 0 {
		h.mu.Lock()
		h.families |= family
		h.mu.Unlock()
	}
}

func (h *IdentityHint) Label() (kind, evidence string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch h.families {
	case 0:
		return "已连接设备", "类型未确认"
	case 1:
		return "疑似 PlayStation", "访问 PlayStation 服务；具体型号未确认"
	case 2:
		return "疑似 Xbox 设备", "访问 Xbox 服务；具体型号未确认"
	case 4:
		return "疑似 Nintendo 设备", "访问 Nintendo 服务；具体型号未确认"
	default:
		return "已连接设备", "发现多个平台的服务请求，类型未确认"
	}
}

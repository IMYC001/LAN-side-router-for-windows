// Package natprobe measures selected SOCKS UDP mappings without LAN capture.
package natprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"accelerator/engine/internal/socks"

	"github.com/pion/stun/v3"
)

type Observation struct {
	Session string `json:"session"`
	Server  string `json:"server"`
	Mapped  string `json:"mapped"`
}

type Result struct {
	Complete               bool          `json:"complete"`
	StableOnRepeat         bool          `json:"stableOnRepeat"`
	SameAcrossDestinations bool          `json:"sameAcrossDestinations"`
	SameAcrossAssociations bool          `json:"sameAcrossAssociations"`
	Observations           []Observation `json:"observations"`
	Note                   string        `json:"note"`
	Scope                  string        `json:"scope"`
}

type datagrams interface {
	WriteTo([]byte, string) error
	ReadFrom([]byte) ([]byte, string, error)
	SetDeadline(time.Time) error
}

// Run compares A/B/A through one association, then A through a second.
// This is not RFC 5780 filtering discovery and cannot certify PS5 NAT type.
func Run(parent context.Context, proxy string) (Result, error) {
	result := Result{
		Observations: []Observation{},
		Scope:        "现有 Clash SOCKS5 的四次 STUN 请求。没有捕获网卡或修改 PS5、路由、节点；不是 PS5 NAT 类型或完整 NAT 过滤测试。",
	}
	client := socks.Client{Address: proxy}
	if err := client.Validate(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(parent, 22*time.Second)
	defer cancel()
	a, err := resolve(ctx, "stun.cloudflare.com", 3478)
	if err != nil {
		return result, errors.New("第一个 STUN 服务解析失败，未能判断映射")
	}
	b, err := resolve(ctx, "stun.l.google.com", 19302)
	if err != nil || a.Addr() == b.Addr() {
		return result, errors.New("第二个独立 STUN 服务不可用，未能判断映射")
	}
	first, err := client.AssociateUDP(ctx)
	if err != nil {
		return result, err
	}
	defer first.Close()
	stopFirst := context.AfterFunc(ctx, func() { first.Close() })
	defer stopFirst()
	var mappings []netip.AddrPort
	for _, server := range []netip.AddrPort{a, b, a} {
		mapped, err := binding(ctx, first, server)
		if err != nil {
			result.Note = "STUN 未全部返回；超时、分流规则或节点策略均可能影响结果，不能据此认定 NAT 类型。"
			return result, nil
		}
		mappings = append(mappings, mapped)
		result.Observations = append(result.Observations, Observation{"same", server.String(), mapped.String()})
	}
	second, err := client.AssociateUDP(ctx)
	if err != nil {
		return result, err
	}
	defer second.Close()
	stopSecond := context.AfterFunc(ctx, func() { second.Close() })
	defer stopSecond()
	mapped, err := binding(ctx, second, a)
	if err != nil {
		result.Note = "第二个代理会话未得到 STUN 回应；已保留第一个会话的观察，不能判断跨会话行为。"
		return result, nil
	}
	result.Observations = append(result.Observations, Observation{"separate", a.String(), mapped.String()})
	result.Complete = true
	result.StableOnRepeat = mappings[0] == mappings[2]
	result.SameAcrossDestinations = mappings[0] == mappings[1]
	result.SameAcrossAssociations = mappings[0] == mapped
	switch {
	case !result.StableOnRepeat:
		result.Note = "同一会话重复请求的映射发生变化；不能据此分类，需核查现有 Clash 分流或出口变化。"
	case !result.SameAcrossDestinations:
		result.Note = "同一代理会话访问两个目标时映射不同；当前代理路径存在目标相关映射或分流，单改本地会话复用不能保证 NAT 2。"
	default:
		result.Note = "本次同一代理会话访问两个目标的映射一致；可进一步验证主机源端口复用，但尚未验证任意来源入站、游戏或 NAT 2。"
	}
	return result, nil
}

func resolve(parent context.Context, host string, port uint16) (netip.AddrPort, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return netip.AddrPort{}, err
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		if ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			return netip.AddrPortFrom(ip, port), nil
		}
	}
	return netip.AddrPort{}, errors.New("STUN 服务没有公共 IPv4")
}

func binding(ctx context.Context, socket datagrams, server netip.AddrPort) (netip.AddrPort, error) {
	if err := ctx.Err(); err != nil {
		return netip.AddrPort{}, err
	}
	request, err := stun.Build(stun.TransactionID, stun.BindingRequest, stun.Fingerprint)
	if err != nil {
		return netip.AddrPort{}, err
	}
	deadline := time.Now().Add(4 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := socket.SetDeadline(deadline); err != nil {
		return netip.AddrPort{}, err
	}
	if err := socket.WriteTo(request.Raw, server.String()); err != nil {
		return netip.AddrPort{}, err
	}
	buffer := make([]byte, 65535)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		data, source, err := socket.ReadFrom(buffer)
		if err != nil {
			return netip.AddrPort{}, err
		}
		from, err := netip.ParseAddrPort(source)
		if err != nil || from != server {
			continue
		}
		mapped, err := decode(data, request.TransactionID)
		if err == nil {
			return mapped, nil
		}
	}
	return netip.AddrPort{}, errors.New("STUN 回应超时")
}

func decode(data []byte, transaction [12]byte) (netip.AddrPort, error) {
	response := &stun.Message{Raw: data}
	if err := response.Decode(); err != nil {
		return netip.AddrPort{}, err
	}
	if response.Type != stun.BindingSuccess || response.TransactionID != transaction {
		return netip.AddrPort{}, errors.New("STUN 类型或事务不匹配")
	}
	if response.Contains(stun.AttrFingerprint) {
		if err := stun.Fingerprint.Check(response); err != nil {
			return netip.AddrPort{}, err
		}
	}
	var mapped stun.XORMappedAddress
	if err := mapped.GetFrom(response); err != nil {
		return netip.AddrPort{}, err
	}
	ip, ok := netip.AddrFromSlice(mapped.IP)
	ip = ip.Unmap()
	if !ok || !ip.IsGlobalUnicast() || ip.IsPrivate() || mapped.Port < 1 || mapped.Port > 65535 {
		return netip.AddrPort{}, fmt.Errorf("无效 STUN 映射")
	}
	return netip.AddrPortFrom(ip, uint16(mapped.Port)), nil
}

package nodelatency

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"time"

	"accelerator/engine/internal/subscription"
)

type Request struct {
	Node   string `json:"node"`
	Index  int    `json:"index"`
	Source string `json:"source"`
}

type Result struct {
	Delay  int    `json:"delay"`
	Method string `json:"method"`
}

func Measure(ctx context.Context, content []byte, request Request) (Result, error) {
	endpoint, err := subscription.NodeEndpoint(content, request.Node)
	if err != nil {
		return Result{}, err
	}
	source, err := netip.ParseAddr(request.Source)
	if err != nil || !source.Is4() || source.IsUnspecified() || source.IsLoopback() {
		return Result{}, errors.New("请选择已连接的局域网网卡")
	}
	iface, err := net.InterfaceByIndex(request.Index)
	if err != nil || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
		return Result{}, errors.New("所选网卡不可用")
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return Result{}, errors.New("无法确认网卡地址")
	}
	owns := false
	for _, address := range addresses {
		if prefix, e := netip.ParsePrefix(address.String()); e == nil && prefix.Addr() == source {
			owns = true
		}
	}
	if !owns {
		return Result{}, errors.New("网卡地址已变化，请刷新网络")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	return sample(ctx, endpoint,
		func(ctx context.Context, host string) ([]netip.Addr, error) {
			if ip, e := netip.ParseAddr(host); e == nil {
				return []netip.Addr{ip}, nil
			}
			// Physical-interface DNS avoids fake-IP replies from a local TUN resolver.
			for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
				attempt, stop := context.WithTimeout(ctx, 2*time.Second)
				resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return boundDialer(request, network).DialContext(ctx, network, server)
				}}
				ips, e := resolver.LookupNetIP(attempt, "ip4", host)
				stop()
				if e == nil && len(ips) > 0 {
					return ips, nil
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
			return nil, errors.New("节点域名解析失败")
		},
		func(ctx context.Context, ip netip.Addr, port int, udp bool) (time.Duration, error) {
			return probe(ctx, request, ip, port, udp)
		})
}

func sample(ctx context.Context, endpoint subscription.Endpoint,
	resolve func(context.Context, string) ([]netip.Addr, error),
	measure func(context.Context, netip.Addr, int, bool) (time.Duration, error)) (Result, error) {
	// Resolution is deliberately outside each RTT timer.
	ips, err := resolve(ctx, endpoint.Server)
	if err != nil {
		return Result{}, errors.New("节点域名解析失败")
	}
	usable := []netip.Addr{}
	for _, ip := range ips {
		if subscription.PublicIPv4(ip) {
			usable = append(usable, ip)
		}
	}
	if len(usable) == 0 {
		return Result{}, errors.New("节点没有可测量的公网 IPv4 地址")
	}
	samples := []time.Duration{}
	for i := 0; i < 3; i++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
		delay, e := measure(attempt, usable[i%len(usable)], endpoint.Port, endpoint.UDPOnly)
		cancel()
		if e == nil {
			samples = append(samples, delay)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(samples) == 0 {
		return Result{}, errors.New("节点未响应直接延迟检测")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	delay := max(1, int(samples[len(samples)/2].Milliseconds()))
	method := "TCP"
	if endpoint.UDPOnly {
		method = "ICMP"
	}
	return Result{Delay: delay, Method: method}, nil
}

package network

import (
	"net"
	"net/netip"
	"strings"
	"time"

	"accelerator/engine/internal/socks"
)

type ManualPlan struct {
	Backend       string  `json:"backend"`
	Adapter       Adapter `json:"adapter"`
	DeviceMAC     string  `json:"deviceMAC"`
	OriginalIP    string  `json:"originalIP"`
	DeviceIP      string  `json:"deviceIP"`
	Netmask       string  `json:"netmask"`
	Gateway       string  `json:"gateway"`
	DNS           string  `json:"dns"`
	UpstreamDNS   string  `json:"upstreamDNS"`
	Proxy         string  `json:"proxy"`
	Checks        []Check `json:"checks"`
	Prepared      bool    `json:"prepared"`
	NetworkWrites int     `json:"networkWrites"`
	Description   string  `json:"description"`
}

// BuildManualPlan reserves a userspace-only /30. No address is assigned to
// Windows, and no route, DHCP lease or real-router ARP mapping is changed.
func BuildManualPlan(snapshot Snapshot, request PlanRequest) ManualPlan {
	p := ManualPlan{OriginalIP: request.DeviceIP, Proxy: request.Proxy, Netmask: "255.255.255.252", UpstreamDNS: "1.1.1.1:53",
		Description: "参数已准备。暂勿填写设备；网关就绪后再填写，停止后恢复原来的 IP 和 DNS 设置。"}
	candidates := []string{
		"10.254.83.0/30",
		"10.253.84.0/30",
		"172.29.83.0/30",
		"192.168.247.0/30",
		"10.252.85.0/30",
		"10.251.86.0/30",
		"172.30.84.0/30",
		"192.168.246.0/30",
	}
	blocked := false
	add := func(name, status, detail string) {
		p.Checks = append(p.Checks, Check{Name: name, Status: status, Detail: detail})
		blocked = blocked || status == "block"
	}
	stamp, err := time.Parse(time.RFC3339, snapshot.Captured)
	if err != nil || time.Since(stamp) > 2*time.Minute || time.Until(stamp) > 5*time.Second {
		add("网络快照", "block", "请刷新网络信息。")
	}
	if request.Slot < 0 || request.Slot >= len(candidates) {
		add("设备槽位", "block", "设备数量超过可分配的独立网络参数范围。")
	}
	for _, a := range snapshot.Adapters {
		if a.Index == request.AdapterIndex {
			p.Adapter = a
			break
		}
	}
	a := p.Adapter
	local, localErr := netip.ParseAddr(a.IPv4)
	router, routerErr := netip.ParseAddr(a.Gateway)
	if !a.Physical || a.Status != "Up" || a.Index == 0 || localErr != nil || !local.Is4() ||
		routerErr != nil || !router.Is4() || a.Prefix < 1 || a.Prefix > 30 ||
		!netip.PrefixFrom(local, a.Prefix).Contains(router) {
		add("局域网网卡", "block", "需要已连接且有 IPv4 网关的物理网卡。")
	} else {
		add("局域网网卡", "pass", a.Name+" / "+a.IPv4)
	}
	hostMAC, hostErr := net.ParseMAC(a.MAC)
	mac, macErr := net.ParseMAC(strings.ReplaceAll(request.DeviceMAC, "-", ":"))
	if request.AutoDiscover && hostErr == nil && len(hostMAC) == 6 && hostMAC[0]&1 == 0 && hostMAC.String() != "00:00:00:00:00:00" {
		add("主机识别", "review", "等待填写本次参数的主机请求独立网关；识别后仅绑定这一台设备。")
	} else if macErr != nil || len(mac) != 6 || mac[0]&1 != 0 || mac.String() == "00:00:00:00:00:00" ||
		hostErr != nil || len(hostMAC) != 6 || hostMAC[0]&1 != 0 ||
		hostMAC.String() == "00:00:00:00:00:00" || mac.String() == hostMAC.String() {
		add("主机身份", "block", "请填写从设备设置中确认的单播 MAC 地址。")
	} else {
		p.DeviceMAC = mac.String()
		// Manual settings change the PS5 IP; stale neighbor entries are not
		// identity proof. Live traffic must match the user-confirmed MAC.
		add("主机身份", "pass", "仅接收已确认的 MAC："+p.DeviceMAC)
	}
	if a.Forwarding {
		add("系统转发", "block", "该物理网卡开启了系统转发，需先确认其他软件的使用情况。")
	}
	if err := (socks.Client{Address: request.Proxy}).Validate(); err != nil {
		add("代理入口", "block", err.Error())
	} else {
		add("代理入口", "review", "使用界面选定的回环 SOCKS5 出口；启动时验证 TCP/UDP。")
	}
	if snapshot.Npcap {
		p.Backend = "npcap"
		add("网卡收发", "review", "使用已安装的 Npcap；不安装或替换驱动。")
	} else if snapshot.WinpkFilter {
		p.Backend = "winpkfilter"
		add("网卡收发", "review", "使用已安装的 Windows Packet Filter，无需 Npcap；网卡收发能力需实测。")
	} else {
		add("网卡收发", "block", "需要 Windows Packet Filter 或 Npcap 其中一种；安装可能短暂断网，软件不会自动安装。")
	}
	if !request.IPv6Absent && !request.IPv6UnverifiedAccepted {
		add("设备 IPv6", "block", "请确认设备没有公网 IPv6；链路本地 fe80 地址不代表已确认。")
	} else if !request.IPv6Absent {
		add("设备 IPv6", "review", "用户接受仅 IPv4 的限时测试；公网 IPv6 和绕行尚未验证。")
	} else {
		add("设备 IPv6", "review", "仍需在实机连接时确认没有 IPv6 绕行。")
	}
	if request.Slot >= 0 && request.Slot < len(candidates) {
		subnet := netip.MustParsePrefix(candidates[request.Slot])
		conflict := false
		for _, other := range snapshot.Adapters {
			ip, e := netip.ParseAddr(other.IPv4)
			if e != nil || !ip.Is4() || other.Prefix < 1 || other.Prefix > 32 {
				continue
			}
			existing := netip.PrefixFrom(ip, other.Prefix).Masked()
			if existing.Overlaps(subnet) {
				conflict = true
			}
		}
		if !conflict {
			p.Gateway = subnet.Addr().Next().String()
			p.DNS = p.Gateway
			p.DeviceIP = subnet.Addr().Next().Next().String()
		}
	}
	if p.Gateway == "" {
		add("主机网段", "block", "此设备的专用网段与现有网卡网段冲突，停止自动分配。")
	} else {
		add("主机网段", "pass", "仅供设备手动填写的独立网段，未写入 Windows。")
	}
	add("恢复方式", "review", "退出或电脑断电后，设备需手动恢复原来的 IP 和 DNS 设置；不会自动变回原网关。")
	p.Prepared = !blocked
	return p
}

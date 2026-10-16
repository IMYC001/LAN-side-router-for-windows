package network

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"accelerator/engine/internal/packet"
	"accelerator/engine/internal/socks"
)

type PlanRequest struct {
	AutoDiscover           bool   `json:"autoDiscover"`
	AdapterIndex           int    `json:"adapterIndex"`
	Slot                   int    `json:"slot"`
	DeviceIP               string `json:"deviceIP"`
	DeviceMAC              string `json:"deviceMAC"`
	Proxy                  string `json:"proxy"`
	IPv6Absent             bool   `json:"ipv6Absent"`
	IPv6UnverifiedAccepted bool   `json:"ipv6UnverifiedAccepted"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type Plan struct {
	Checks        []Check `json:"checks"`
	CanStart      bool    `json:"canStart"`
	NetworkWrites int     `json:"networkWrites"`
	Description   string  `json:"description"`
	DeviceIP      string  `json:"deviceIP"`
	DeviceMAC     string  `json:"deviceMAC"`
	Gateway       string  `json:"gateway"`
	Proxy         string  `json:"proxy"`
}

func BuildPlan(snapshot Snapshot, request PlanRequest) Plan {
	result := Plan{
		CanStart: false, DeviceIP: request.DeviceIP, DeviceMAC: request.DeviceMAC, Proxy: request.Proxy,
		Description: "旧接管方案仅预览，不发送 ARP、不改路由、不启动 TUN；限时手动网关请使用主机加速页。",
	}
	add := func(name, status, detail string) {
		result.Checks = append(result.Checks, Check{Name: name, Status: status, Detail: detail})
	}
	stamp, err := time.Parse(time.RFC3339, snapshot.Captured)
	if err != nil || time.Since(stamp) > 2*time.Minute {
		add("网络快照", "block", "网络信息已过期，需要重新读取。")
	} else {
		add("网络快照", "pass", "只读快照有效。实际接管前仍须重新验证。")
	}
	var chosen *Adapter
	for i := range snapshot.Adapters {
		if snapshot.Adapters[i].Index == request.AdapterIndex {
			chosen = &snapshot.Adapters[i]
			break
		}
	}
	if chosen == nil || !chosen.Physical || chosen.Status != "Up" {
		add("物理网卡", "block", "必须选择已连接的物理网卡，不能选择 Clash、UU 或 VMware 虚拟网卡。")
	} else {
		add("物理网卡", "pass", chosen.Name+" / "+chosen.Description)
		result.Gateway = chosen.Gateway
		if chosen.Forwarding {
			add("系统转发", "block", "该网卡已有 Windows IP 转发。需先确认其他软件依赖，不能自动关闭。")
		} else {
			add("系统转发", "pass", "该网卡未开启系统 IP 转发，可避免与独立协议栈重复转发。")
		}
		device, ipErr := netip.ParseAddr(request.DeviceIP)
		local, localErr := netip.ParseAddr(chosen.IPv4)
		gateway, gwErr := netip.ParseAddr(chosen.Gateway)
		valid := ipErr == nil && localErr == nil && gwErr == nil && device.Is4() && local.Is4() && gateway.Is4() && chosen.Prefix > 0 && chosen.Prefix < 31
		if valid {
			prefix := netip.PrefixFrom(local, chosen.Prefix).Masked()
			broadcast := prefix.Addr().As4()
			bits := chosen.Prefix
			for i := range broadcast {
				for b := 7; b >= 0; b-- {
					if bits > 0 {
						bits--
					} else {
						broadcast[i] |= 1 << b
					}
				}
			}
			valid = prefix.Contains(device) && prefix.Contains(gateway) && device != local && device != gateway && device != prefix.Addr() && device != netip.AddrFrom4(broadcast)
		}
		if !valid {
			add("设备地址", "block", "设备需与电脑和主路由位于同一 IPv4 子网，不能是本机、网关、网络或广播地址。")
		} else {
			add("设备地址", "pass", request.DeviceIP)
		}
		localMAC, _ := net.ParseMAC(chosen.MAC)
		deviceMAC, macErr := net.ParseMAC(strings.ReplaceAll(request.DeviceMAC, "-", ":"))
		var gatewayMAC net.HardwareAddr
		verified := false
		for _, n := range snapshot.Neighbors {
			if n.Index != chosen.Index {
				continue
			}
			if n.IP == chosen.Gateway {
				gatewayMAC, _ = net.ParseMAC(n.MAC)
			}
			if n.IP == request.DeviceIP {
				m, _ := net.ParseMAC(n.MAC)
				verified = m.String() == deviceMAC.String() && macErr == nil
			}
		}
		if !verified {
			add("设备绑定", "block", "邻居表未确认该 IP/MAC。先让 PS5 联网，再刷新；不主动扫描或接管未知设备。")
		} else {
			add("设备绑定", "pass", deviceMAC.String())
		}
		target := packet.Target{LocalMAC: localMAC, DeviceMAC: deviceMAC, GatewayMAC: gatewayMAC, DeviceIP: device, GatewayIP: gateway}
		if err := target.Validate(); err != nil {
			add("恢复信息", "block", "缺少有效网关 MAC 或设备信息，无法构造恢复帧。")
		} else {
			add("恢复信息", "pass", fmt.Sprintf("真实网关 %s / %s。只构造恢复帧，未发送。", gateway, gatewayMAC))
		}
	}
	if err := (socks.Client{Address: request.Proxy}).Validate(); err != nil {
		add("代理入口", "block", err.Error())
	} else {
		add("代理入口", "review", "地址格式正确；还需通过 TCP/UDP 检查。PS5 将使用现有 Clash 的节点和分流规则。")
	}
	if snapshot.Npcap {
		add("二层驱动", "review", "检测到 Npcap 文件；版本、签名、权限及 WLAN 注入能力尚未实测。")
	} else {
		add("二层驱动", "block", "未检测到 Npcap。驱动安装可能短暂影响网络，必须由用户决定后再进行。")
	}
	if !request.IPv6Absent {
		add("PS5 IPv6", "block", "IPv6 尚未确认。仅 IPv4 接管会让 IPv6 流量绕过加速；不自动改电脑或路由 IPv6。")
	} else {
		add("PS5 IPv6", "review", "用户确认该设备没有公网 IPv6，仍需实机验证。")
	}
	add("Clash TUN 共存", "review", "本软件不改 TUN 和路由；需实测物理网卡收发、DNS、游戏 UDP 及原有电脑连接。")
	add("接管执行", "block", "等待用户决定驱动安装与实机测试。当前程序只能诊断、检查和预览。")
	return result
}

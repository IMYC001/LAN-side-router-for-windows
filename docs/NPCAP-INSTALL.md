# Npcap 安装与停止边界

> Public archive: local network/device identifiers below are illustrative replacements. Historical sections describe their original development stage.

用户已允许安装并手动完成。当前确认 Npcap 1.89 已安装；无需再次运行安装器。

## 当前进度（2026-10-13）

旧版 `INSECURE_NPCAP` 已于 16:49 成功移除。用户再次重启后，16:58 只读复查确认旧组件、旧网卡绑定、Npcap 待重启文件操作均为空；Windows 本次启动时间为 16:56:23，WLAN 和 Meta 均为 Up，Clash 仍监听 `127.0.0.1:7897`。

用户手动完成交互安装后，只读复查确认驱动 `npcap` 为 Running；`System32\Npcap\wpcap.dll` 版本 1.10.7，`Packet.dll` 版本 1.89。注册表确认 AdminOnly=1、Dot11Support=0、WinPcapCompatible=0。程序能加载系统库并枚举 Intel AX201 WLAN。随后已完成授权的目标只读捕获：收到确认 PS5 MAC 的 ARP/IPv4，捕获程序发送帧数 0，辅助进程正常退出。WLAN 注入能力仍未验证。

## 已准备

- 文件：项目目录下 `.tools/npcap-1.89.exe`。
- 下载源：`https://npcap.com/dist/npcap-1.89.exe`。
- Windows Authenticode：`Valid`。
- 签名者：`Nmap Software LLC`。
- 本次下载的 SHA256：`8AED85E900D783D1308506E919587D3E540451947AF8A82F2D04F819E44305CC`。

SHA256 是本次文件的记录；可信来源判断依据是官方 HTTPS 下载和有效的 Windows 数字签名。

## 已完成的安装步骤（历史）

以下是已由用户完成的交互安装选项，保留供记录；不要因本文重复安装。

1. 在方便短暂中断网络的时间打开安装器，确认发布者为 `Nmap Software LLC`。
2. 阅读许可，完成 Windows 管理员授权。
3. 勾选 `Restrict Npcap driver's access to Administrators only`。
4. 保持 `Support raw 802.11 traffic (and monitor mode)` 不勾选；本方案先验证正常连接模式下的以太网数据帧，不切换无线监控模式。
5. 保持 `Install Npcap in WinPcap API-compatible Mode` 不勾选；后续通过 Npcap 自身目录加载库。
6. 完成安装。若安装器要求重启，保存当前工作后由用户自行决定重启时间，不自动重启。

管理员限制意味着后续抓包辅助程序需要单独授权；桌面程序仍以普通权限运行。安装 Npcap 本身不等于已接管 PS5，也不能证明当前 WLAN 支持目标帧注入。

## 下一次实机检查

用户确认原 IPv4 `192.168.50.20`、MAC `02-00-00-00-00-20`。提供的 `fe80::20` 是链路本地地址，公网 IPv6 是否存在仍需用户确认。不能仅根据电脑的邻居表猜测设备。

最长 20 秒的目标只读观察已通过，PS5 设置保持原样。用户已持续授权不涉及断网的操作；下一步仍需停下来确认首次网关注入、PS5 手动参数和公网 IPv6 情况。当前软件采用自建 /30 网关，不冒用真实路由器、不修改电脑网络设置。

官方说明：`https://npcap.com/guide/npcap-users-guide.html`。

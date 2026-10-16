# Clash 出口版主机加速器 —— 开发执行方案

> Public archive: local network/device identifiers below are illustrative replacements. Historical sections describe their original development stage.

日期：2026-10-15　状态：历史草案，基于 0.5.4，不代表当前版本待办

2026-10-16 更新：下文保留当时的设计背景。转发速度问题已在 0.5.5 修复并获用户实测确认；
当前版本为 0.5.14，设备按用户命名显示，已移除设备类型推测。当前功能与边界以 README.md 为准。

---

## 0. 结论先行

| 项 | 内容 |
|---|---|
| 目标 | 复刻网易 UU 的「主机加速」（PS5 / Xbox / Switch），唯一差别：出口节点来自用户自己的 Clash 订阅 |
| 基线 | **复用现有项目** `repository root`（v0.5.4，已实机跑通、用户已确认 NAT 类型 2） |
| 复用度 | 约 80% 代码直接可用（二层旁路收发、gVisor 转发栈、独立 Mihomo 出口、订阅卡片、DPAPI 加密存储、安装包） |
| 主要缺口 | ①订阅生态不如 Clash 完整　②转发吞吐 ~10 Mbps 未解　③主机识别不如 UU 精细　④无联机房间 |
| 不写驱动 | 不需要自研 WFP/NetFilter 驱动；复用本机已安装的 Npcap（1.89，Running）或 Windows Packet Filter |

---

## 1. UU 原理 → 本软件等价实现 对照表

UU 的主机加速 = **以太网层旁路** + **让主机把 PC 当网关** + **用户态协议栈** + **出口隧道**。四层里我们只换掉出口。

| 机制 | UU 实现（逆向实测） | 本软件等价实现 | 现状 |
|---|---|---|---|
| 二层旁路（看到主机的 ARP/DNS/裸 IP 帧） | 自研 WFP 驱动 `uunetfilter.sys`（NetFilter SDK 的 `UU Recv SublayerETH` 把 ETH 子层送上用户态）+ `nf_macPostSend` 反注入 | Npcap 在物理网卡上按 `ether src <设备MAC>` 抓帧 + `pcap_sendqueue_transmit` 批量注入 | ✅ 已实现并跑通 |
| 让主机把 PC 当网关 | 伪造 DHCP offer/ack + 对真实路由器回 fake NACK + 广播 ARP 抢网关（`console_ip_use_lan_ip=0`）；或旁路镜像（`=1`） | **用户手动填 4 参数**（IP/掩码/网关/DNS），与 UU 教程模式①完全一致；本机只回应虚拟网关自身的 ARP | ✅ 已实现 |
| 主机识别 | DHCP vendor/hostname、mDNS 主机名前缀、SSDP、TCP/DNS TTL 指纹、云端 MAC OUI 库 | 首台使用本次虚拟 /30 参数的设备 MAC 绑定 + DNS 观察猜测平台（粗） | ⚠️ 功能可用，精度不如 UU |
| DNS 劫持 + fake-IP | `nf_udpPostReceive` 捞 UDP + `fake reply ip` | 网关 DNS 映射到公共 DNS，经出口转发；不做 fake-IP | ✅ 已实现 |
| TCP 隧道 | 逐连接 tproxy，可 TLS/XOR 加密 | gVisor 用户态 TCP/IP 栈（tun2socks）→ SOCKS5 → 节点 | ✅ 已实现 |
| UDP 隧道 | KCP / GRP | SOCKS5 UDP ASSOCIATE，按主机源端口复用会话 | ✅ 已实现 |
| ICMP | `fakeping` 延迟伪造回包 | 只回应**虚拟网关自身**的 ping；公网 ICMP 不转发、不伪造 | ⚠️ 有意差异 |
| 出口节点 | UU 专线（服务端下发 nodeId/nodeName） | **Clash 订阅里的节点** | ✅ 已有独立 Mihomo 出口 |
| 出口授权 | 服务端按 uid+会员返回 403 | 无（本地自选） | — 这正是我们要绕开的那道门 |
| 精准锁区 | `RegionLockLogic` 按 ICMP RTT 锁国家/区服 | 未实现 | ❌ 可选 |
| 局域网联机房间 | `lanplay_tun.dll` join_room + P2P 打洞 + 中继 | 未实现 | ❌ 不在默认范围 |
| 系统副作用 | `netsh` 抢改路由/网卡/弱主机发送 | **完全不改本机路由、DNS、防火墙、转发** | ✅ 更干净 |

**关键设计取舍**：UU 靠驱动做 DHCP/ARP 劫持，所以主机「什么都不用设」。我们不写驱动，改为主机侧手动填一次 4 参数（UU 教程本身也提供这个模式）。代价是首次要手动设置一次；收益是不需要自研内核驱动、不改本机网络配置、不与其他软件抢网关。

---

## 2. 唯一差异：出口 = Clash 订阅

- 引擎强制把选定节点展开成**独立运行**的极简 Mihomo 配置：`tun.enable=false`、仅回环监听、随机端口、指定物理网卡、强制 IPv4、`MATCH→选定节点`。
- 订阅里原有的 TUN / 规则 / 监听 / 脚本 / 控制器 / provider **一律不落地执行**。
- 出口二选一：①订阅独立节点（默认）②复用本机现役 Clash/Verge 的 SOCKS5/Mixed 入口（需先切全局模式）。
- 节点质量由机场决定；不承诺 NAT 2、不承诺所有节点支持 UDP。

---

## 3. 分阶段执行

### P0 — 基线自测（0.5 天）
1. `scripts/Get-Tools.ps1`（工具已在 `.tools`，含 Go 1.27.2 / .NET 10 SDK / mihomo v1.19.32）
2. `scripts/Build.ps1 -SmokeTest` → 复现 0.5.4 构建与冒烟
3. 核对本机：Npcap 1.89 Running、Clash Verge 在跑（`verge-mihomo.exe`）、物理网卡 = WLAN
4. 产出：可复现的构建日志 + 冒烟结果

### P1 — 订阅生态补齐（1.5–2 天）★核心
| # | 任务 | 现状 → 目标 |
|---|---|---|
| 1 | **proxy-providers 支持** | 现在直接报错「仅 provider 的订阅需先导出完整 YAML」→ 解析 `proxy-providers`（type/url/path/interval），拉取并展开为可选节点 |
| 2 | **多订阅聚合视图** | 现在只按卡片独立选节点 → 合并所有订阅的节点为一个列表，支持延迟测速排序 |
| 3 | **导入方式补齐** | URL / 本地 YAML 拖拽 / 粘贴 YAML / 剪贴板，与 Clash 客户端一致 |
| 4 | **自动刷新** | 按卡片设 interval 定时重拉；失败保留旧内容不丢配置 |
| 5 | **一键读本机 Clash Verge** | 读 `%APPDATA%\io.github.clash-verge-rev.clash-verge-rev\profiles.yaml` + `profiles\*.yaml`，列出 profile 供选择（**只读，不改动 Verge**） |
| 6 | **节点类型确认** | 用户订阅实测：`anytls ×45`、`http ×16`、`select/fallback` 组。确认 anytls 的 UDP 能力并补 http 类型 SOCKS 上游 |

**验收**：导入用户真实订阅 → 列出 45 个节点 → 任选一个 → 独立核心起来 → 通过随机回环 SOCKS 端口验证 TCP(example.com:443) 与 UDP(1.1.1.1:53) 通。

### P2 — 出口与节点能力对齐（0.5–1 天）
1. 真实 UDP 探针（SOCKS5 UDP ASSOCIATE），不再只看 YAML 的 `udp: true` 标记
2. 节点延迟测速 / 按 proxy-group 选择 / 运行中快速切节点
3. 明确拒绝不支持的节点（链式 `dialer-proxy`、纯 provider、私网/fake-IP 服务器）

### P3 — 转发性能攻关（1–2 天）★与 UU 差距最大处
现状：实测约 **10 Mbps**（未加速时百兆），用户此前要求暂缓。
1. **定位而非猜**：把链路切成三段独立计时 ——「Npcap 读→Inject」「gVisor 处理→SOCKS 写」「SOCKS 读→Npcap 写」「发送队列阻塞时长」，出真实数据
2. **接收侧批量化**：当前是单包 `pcap_next_ex` + 单包 `Inject`；改为排空内核缓冲后批量注入，并扩大 gVisor link 队列（现 `channel.New(4096, 1500, "")`）
3. **去掉每包磁盘检查**：`leaseValid()` 每批都 `stat` 一次心跳文件 → 改为内存心跳 + 1 秒定时刷新
4. **对照实验**：同一条链路让 PS5 走本机 Clash Verge 全局模式（用户已知有效），对比速率 → 区分「机场/节点本身慢」与「我们的引擎慢」
5. **环形缓冲区**：Npcap 接收缓冲 4 MiB → 增大 + 非阻塞排空策略复核

**验收**：PS5 侧测速显著提升；若瓶颈确认在节点侧，如实报告并给出证据，不假称已修复。

### P4 — 主机识别与体验（1 天，可选）
1. 补 mDNS / SSDP / DHCP vendor 指纹识别，把「未知设备」提升为「疑似 PS5 / Xbox / Switch」（对齐 UU 的 `pc_console_device_*`）
2. 可选（高风险，默认关闭）：内置 DHCP 服务器替代手动填参数 —— 需要与真实路由器抢租约（UU 用 fake NACK 实现）。**不默认启用。**
3. 首次设置向导：把 4 参数按平台（PS/Xbox/Switch）分步显示，参照物与 UU 教程一致

### P5 — 打包交付（0.5–1 天）
1. 品牌/命名定稿，界面与文档同步改名
2. `Build-Installer.ps1` 重打包：自包含绿色目录 + Inno 安装包（含原厂 Windows Packet Filter MSI）
3. 交付物：`<Name>-<ver>-Setup-win-x64.exe` + 绿色目录 + README/INSTALLATION/TEST-REPORT

---

## 4. 与 UU 的已知差异（诚实清单）

| 差异 | 原因 | 影响 |
|---|---|---|
| 主机需手动填一次 4 参数 | 不做 DHCP/ARP 劫持，不写内核驱动 | 首次设置麻烦一次；之后不用改 |
| 公网 ICMP 不转发 | SOCKS5 不承载 ICMP；不伪造 | 主机内置「网络测试」可能报连接失败，游戏 TCP/UDP 不受影响 |
| 无联机房间 / P2P 打洞 | 属 UU 的另一条产品线 | 只影响「与好友联机」，不影响加速 |
| 无精准锁区 | UU 用 ICMP RTT 锁区服 | 多区服游戏可能不自动选最优区 |
| 依赖已装包驱动 | Windows 原始套接字收不到 ARP、WSA 禁止 raw TCP 发送 | 安装包会带原厂驱动，新机器需同意安装驱动（可能短暂断网/重启） |
| NAT 类型 | 取决于机场与节点 | 不保证 NAT 2 |

---

## 5. 验收方式

1. **离线**：Go 单元测试 + 静态检查 + 自包含发布 + WPF 桌面冒烟截图（现有脚本）
2. **本机**：引擎探针 —— 经出口 TCP 到 example.com:443、UDP 到 1.1.1.1:53
3. **实机**（需你配合）：PS5 与 PC 同一局域网 → PS5 手动填显示的 4 参数 → 连接测试 / 游戏 / 语音 / 测速 / NAT 类型
4. **停止后**：PS5 必须手动恢复原来的 IP/DNS（自动获取或原自定义值）—— 软件无法远程改回

---

## 6. 时间估算

| 阶段 | 工期 | 依赖你 |
|---|---|---|
| P0 基线自测 | 0.5 天 | 否 |
| P1 订阅生态 | 1.5–2 天 | 需要你提供订阅 URL |
| P2 出口能力 | 0.5–1 天 | 否 |
| P3 性能攻关 | 1–2 天 | 需要 PS5 实机测速 |
| P4 识别与体验 | 1 天（可选） | 需要 PS5 |
| P5 打包交付 | 0.5–1 天 | 需要你验收 |

合计约 **5–7 天**（P4 可选）。

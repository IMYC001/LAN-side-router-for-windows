using System.ComponentModel;
using System.Globalization;
using System.Text.RegularExpressions;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;
using System.Windows.Markup;

namespace Accelerator.App;

// Only presentation text is translated. Stored names, protocol values and engine
// requests stay unchanged when the user switches languages during a session.
internal sealed class UiText : INotifyPropertyChanged
{
    public static UiText Instance { get; } = new();
    public string Language { get; private set; } = "zh-CN";
    public event PropertyChangedEventHandler? PropertyChanged;
    public static bool IsEnglish => Instance.Language == "en-US";
    public static string ProductTitle => Get("局域网设备加速（旁路由模式）");
    public static readonly DependencyProperty SourceProperty =
        DependencyProperty.RegisterAttached("Source", typeof(string), typeof(UiText), new PropertyMetadata(""));

    private static readonly Dictionary<string, string> Translations = new(StringComparer.Ordinal)
    {
        ["局域网设备加速（旁路由模式）"] = "LAN Device Acceleration (Side Router Mode)",
        ["局域网设备加速器"] = "LAN Device Accelerator",
        ["局域网设备加速"] = "LAN Device Acceleration",
        ["加速"] = "Acceleration", ["多设备管理"] = "Devices", ["订阅"] = "Subscriptions",
        ["设置"] = "Settings", ["关于"] = "About", ["语言"] = "Language",
        ["特别鸣谢：GPT 6 Astra"] = "Special thanks: GPT 6 Astra",
        ["添加设备"] = "Add device", ["新增一台设备"] = "Add a new device",
        ["每台设备有独立名称、节点和网络参数。新增设备会自动显示在加速页面。"] =
            "Each device has its own name, node and network settings. New devices appear automatically.",
        ["设备名称"] = "Device name", ["加速节点"] = "Proxy node", ["删除此设备"] = "Delete device",
        ["设备 {0}"] = "Device {0}", ["未命名设备"] = "Unnamed device",
        ["HTTPS 订阅地址"] = "HTTPS subscription URL", ["添加"] = "Add", ["导入"] = "Import",
        ["导入本地订阅文件"] = "Import a local subscription file", ["尚未导入订阅"] = "No subscriptions imported",
        ["已用 / 总额度：{0}"] = "Used / quota: {0}", ["更新订阅"] = "Update subscription",
        ["重命名"] = "Rename", ["删除订阅"] = "Delete subscription", ["节点"] = "Nodes",
        ["测量电脑到节点服务器的延迟"] = "Measure latency from this PC to the node",
        ["测延迟"] = "Test latency", ["取消测试"] = "Cancel test",
        ["外观主题"] = "Appearance", ["浅色"] = "Light", ["深色"] = "Dark", ["主题色"] = "Accent color",
        ["绿色"] = "Green", ["蓝色"] = "Blue", ["浅鲑红"] = "Light salmon", ["紫色"] = "Violet", ["玫红色"] = "Rose",
        ["窗口"] = "Window", ["收起到托盘"] = "Minimize to tray", ["加速出口"] = "Proxy mode",
        ["现有 Clash / Mihomo"] = "Existing Clash / Mihomo", ["订阅独立节点"] = "Independent subscription",
        ["独立节点待准备"] = "Independent node not ready", ["使用现有代理入口"] = "Using existing local proxy",
        ["请将 Clash 切换为全局模式；规则模式可能让部分设备流量直连，导致加速失败。"] =
            "Set Clash to Global mode. Rule mode may bypass the proxy for some device traffic.",
        ["本机 SOCKS5 / Mixed 入口"] = "Local SOCKS5 / Mixed proxy",
        ["检查 TCP / UDP"] = "Check TCP / UDP", ["检查 UDP 映射"] = "Check UDP mapping",
        ["TCP · 未检查"] = "TCP · Not checked", ["UDP · 未检查"] = "UDP · Not checked",
        ["UDP 映射 · 未检查"] = "UDP mapping · Not checked",
        ["TCP · 检查中"] = "TCP · Checking", ["UDP · 检查中"] = "UDP · Checking",
        ["UDP 映射 · 检查中"] = "UDP mapping · Checking",
        ["网络与设备检测"] = "Network and discovery", ["刷新网卡和已知设备"] = "Refresh adapters and known devices",
        ["局域网网卡"] = "LAN adapter", ["正在读取网络信息…"] = "Reading network information…",
        ["IP 地址"] = "IP address", ["MAC 地址"] = "MAC address", ["状态"] = "Status",
        ["设备 · 等待接入"] = "Device · Waiting", ["设备 · 未连接"] = "Device · Disconnected",
        ["目标设备无可用公网 IPv6，使用 IPv4 加速"] = "Target device has no usable public IPv6; use IPv4",
        ["诊断与日志"] = "Diagnostics and logs", ["导出诊断"] = "Export diagnostics", ["离线验证"] = "Offline check",
        ["捕获丢包 · 等待开始"] = "Capture drops · Idle", ["UDP 会话 · 等待开始"] = "UDP sessions · Idle",
        ["运行诊断 · 等待开始"] = "Runtime diagnostics · Idle", ["测速诊断 · 等待网关就绪"] = "Diagnostics · Waiting for gateway",
        ["未启动 · 填写前记录设备原设置"] = "Idle · Record the device's original network settings",
        ["未启动 · 设备已修改时请恢复原来的 IP 和 DNS 设置"] = "Idle · Restore the device's original IP and DNS if changed",
        ["准备就绪"] = "Ready", ["下载"] = "Download", ["上传"] = "Upload", ["延迟"] = "Latency",
        ["本次加速累计下载载荷；此设备下次开始归零"] = "Downloaded this session; resets when this device starts again",
        ["本次加速累计上传载荷；此设备下次开始归零"] = "Uploaded this session; resets when this device starts again",
        ["这是电脑到你选的节点的延迟，不代表实际游戏延迟"] =
            "Latency from this PC to the selected node, not the actual game latency",
        ["设备网络参数"] = "Network settings", ["展开或收起网络参数"] = "Expand or collapse network settings",
        ["复制四项网络参数"] = "Copy the four network settings", ["IPv4 地址"] = "IPv4 address",
        ["子网掩码"] = "Subnet mask", ["默认网关"] = "Default gateway", ["首选 DNS"] = "Preferred DNS",
        ["此设备使用的订阅节点"] = "Subscription node for this device", ["Clash 全局模式 · 查看设置"] = "Clash Global · Settings",
        ["独立订阅"] = "Independent", ["Clash 共存"] = "Clash coexistence",
        ["开始加速"] = "Start", ["停止加速"] = "Stop", ["正在停止…"] = "Stopping…", ["准备中…"] = "Preparing…",
        ["等待开始加速"] = "Ready to start", ["尚无设备接入"] = "No device connected",
        ["开始加速后生成网络参数。"] = "Network settings appear after starting.",
        ["累计 {0}"] = "Total {0}", ["正在准备"] = "Preparing", ["正在启动网关"] = "Starting gateway",
        ["参数准备未完成"] = "Network settings not ready", ["正在准备，暂勿填写设备网络参数。"] =
            "Preparing. Wait before changing the device's network settings.",
        ["网关已就绪，可在此设备填写以上参数。停止后恢复原设置。"] =
            "Gateway ready. Enter these settings on this device. Restore the originals after stopping.",
        ["正在停止，请恢复此设备原来的 IP 和 DNS 设置。"] = "Stopping. Restore this device's original IP and DNS.",
        ["加速已结束，请恢复此设备原来的 IP 和 DNS 设置。"] = "Stopped. Restore this device's original IP and DNS.",
        ["加速已停止 · 请恢复此设备原来的 IP 和 DNS 设置"] = "Stopped · Restore this device's original IP and DNS",
        ["加速已停止，请恢复原网络设置。"] = "Stopped. Restore the original network settings.",
        ["已连接 · 加速中"] = "Connected · Accelerating", ["已连接 · 正在建立转发"] = "Connected · Establishing relay",
        ["正在停止"] = "Stopping", ["连接失败"] = "Connection failed", ["等待设备连接"] = "Waiting for device",
        ["正在准备，等待设备接入"] = "Preparing · Waiting for device", ["启动未完成"] = "Start incomplete",
        ["未加入加速"] = "Inactive", ["此设备网关就绪后可复制网络参数"] = "Copy settings after this device's gateway is ready",
        ["开始加速后可复制网络参数"] = "Start acceleration before copying settings",
        ["剪贴板暂时被占用，请稍后再试"] = "Clipboard busy. Try again shortly.",
        ["四项网络参数已复制"] = "Network settings copied",
        ["至少保留一台设备"] = "Keep at least one device",
        ["请先导入订阅，并为每台加入加速的设备选择节点"] = "Import a subscription and choose a node for each device",
        ["请确认主机没有可用公网 IPv6，本版仅加速 IPv4"] = "Confirm the device has no usable public IPv6. Only IPv4 is accelerated.",
        ["启动网关…"] = "Starting gateway…", ["正在更新加速设备…"] = "Updating active devices…",
        ["{0} 已停止，其他设备继续加速"] = "{0} stopped. Other devices remain active.",
        ["{0} 未能完成停止：{1}"] = "{0} could not stop: {1}",
        ["{0} 加速准备中"] = "{0} preparing", ["{0} 启动未完成"] = "{0} start incomplete",
        ["操作未完成"] = "Operation incomplete", ["恢复设备网络设置"] = "Restore device network settings",
        ["等待结束 · 可以重新开始加速"] = "Ready · Acceleration can be started again",
        ["加速已结束 · 请恢复设备原来的 IP 和 DNS 设置"] = "Stopped · Restore the device's original IP and DNS",
        ["只读检查已结束"] = "Read-only check finished", ["只读检查 · 设备设置保持原样"] = "Read-only check · Leave device settings unchanged",
        ["网关准备中 · 暂勿填写设备"] = "Preparing gateway · Wait before entering settings",
        ["正在停止 · 请恢复设备原来的 IP 和 DNS 设置"] = "Stopping · Restore the device's original IP and DNS",
        ["请等待当前操作结束后再退出"] = "Wait for the current operation before exiting",
        ["完成 · 电脑网络配置未修改"] = "Done · PC network settings unchanged",
        ["加速准备中"] = "Preparing acceleration", ["操作超时或已取消"] = "Timed out or canceled",
        ["未完成 · 网络配置未修改"] = "Incomplete · Network settings unchanged",
        ["状态连接失败 · 测试将在心跳超时后停止"] = "Status connection lost · Stops after heartbeat timeout",
        ["测试停止"] = "Test stopped", ["停止测试"] = "Stop test",
        ["本地订阅请通过“导入文件”添加新版本"] = "Use Import to update a local subscription",
        ["导出诊断失败，请选择可写入的位置。"] = "Export failed. Choose a writable location.",
        ["诊断导出失败，请选择可写入的位置。"] = "Export failed. Choose a writable location.",
        ["保存"] = "Save", ["订阅名称"] = "Subscription name", ["重命名订阅"] = "Rename subscription",
        ["打开窗口"] = "Open window", ["退出"] = "Exit",
        ["软件已收起，加速未启动。"] = "Minimized to tray. Acceleration is idle.",
        ["加速继续运行；可从托盘停止或打开窗口。"] = "Acceleration continues. Open or stop it from the tray.",
        ["等待"] = "Waiting", ["等待测试"] = "Waiting for test", ["测试中"] = "Testing",
        ["未测试"] = "Not tested", ["超时"] = "Timeout", ["不可测试"] = "Unavailable", ["未连通"] = "Unreachable",
        ["正在测量电脑到节点服务器的延迟"] = "Measuring latency from this PC to the node",
        ["电脑到节点 {0} ms；不代表实际游戏延迟"] = "PC to node: {0} ms; not actual game latency",
        ["未收到节点直连响应；不代表游戏不可用"] = "No direct node response; games may still work",
        ["订阅已导入，即将测试延迟；请选择节点"] = "Subscription imported. Testing latency; choose a node.",
        ["请选择已连接的局域网网卡后重试；请选择节点"] = "Select a connected LAN adapter and retry, then choose a node.",
        ["正在测试 {0} / {1}；请选择节点"] = "Testing {0} / {1}; choose a node",
        ["测试完成 · {0} / {1} 可达，请选择节点"] = "Test complete · {0} / {1} reachable; choose a node",
        ["测试已取消，已完成结果保留；请选择节点"] = "Canceled. Completed results kept; choose a node.",
        ["测试未完成，可重试；请选择节点"] = "Test incomplete. Retry, then choose a node.",
        ["测试未完成，可重试"] = "Test incomplete; retry",
        ["本地文件"] = "Local file", ["不限量或未提供"] = "Unlimited or unspecified", ["用量未提供"] = "Usage unavailable",
        ["到期时间未提供"] = "Expiry unavailable", ["更新时间未知"] = "Update time unknown", ["刚刚更新"] = "Just updated",
        ["{0} 分钟前"] = "{0} minutes ago", ["{0} 小时前"] = "{0} hours ago",
        ["已用 / 总额度；按 1024 进制换算。用量来自服务商上次更新的记录，可能延迟。"] =
            "Used / quota, using binary units. Provider usage may be delayed.",
        ["记录时间：{0}"] = "Recorded: {0}",
        ["点击卡片的更新按钮重新获取；本次加速流量不等于整个订阅的计费用量。"] =
            "Refresh the subscription for newer usage. Device session traffic differs from billed subscription usage.",
        ["{0} · {1} 个节点"] = "{0} · {1} nodes",
        ["{0} 个节点 · {1}"] = "{0} nodes · {1}",
        ["电脑 {0}/{1}   ·   主路由 {2}   ·   系统 IP 转发 {3}"] = "PC {0}/{1} · Router {2} · System forwarding {3}",
        ["已开启"] = "On", ["未开启"] = "Off", ["通过"] = "Passed", ["未通过"] = "Failed",
        ["独立节点已准备"] = "Independent node ready", ["请先导入订阅"] = "Import a subscription first",
        ["网关已就绪 · 停止后恢复设备原来的 IP 和 DNS 设置"] = "Gateway ready · Restore the device's original IP and DNS after stopping",
        ["网关准备或已停止 · 设备已修改时请恢复原设置"] = "Gateway preparing or stopped · Restore original device settings if changed",
        ["未启动 · 填写前记录设备原设置，停止后恢复原设置。"] = "Idle · Record original device settings and restore them after stopping.",
        ["未启动 · 需要 Windows Packet Filter 或 Npcap；软件不会自动安装驱动。"] =
            "Idle · Windows Packet Filter or Npcap is required; drivers are not installed automatically.",
        ["没有发现常用代理端口。可填写 Clash 设置中的本机 SOCKS / Mixed 端口。"] =
            "No common proxy ports found. Enter the local SOCKS / Mixed port from Clash settings.",
        ["已监听的常用端口：{0}"] = "Listening proxy ports: {0}",
        ["读取网卡和邻居表…"] = "Reading adapters and neighbors…",
        ["运行内存 TCP / UDP 转发验证…"] = "Running offline TCP / UDP check…",
        ["检查代理 TCP / UDP…"] = "Checking proxy TCP / UDP…",
        ["检查代理 UDP 映射（最长 22 秒）…"] = "Checking proxy UDP mapping (up to 22 seconds)…",
        ["解析本地订阅…"] = "Parsing local subscription…", ["读取 HTTPS 订阅…"] = "Fetching HTTPS subscription…",
        ["恢复加密订阅…"] = "Restoring encrypted subscription…", ["切换订阅…"] = "Switching subscription…",
        ["更新订阅…"] = "Updating subscription…", ["删除订阅…"] = "Deleting subscription…",
        ["TCP · {0} · {1} ms"] = "TCP · {0} · {1} ms", ["UDP · {0} · {1} ms"] = "UDP · {0} · {1} ms",
        ["捕获丢包 {0}"] = "Capture drops: {0}", ["捕获丢包未知"] = "Capture drops unknown",
        ["UDP {0} 流 / {1} 代理会话"] = "UDP: {0} flows / {1} proxy sessions",
        ["电脑到节点服务器的延迟"] = "Latency from this PC to the node",
        ["测试超时，可稍后重试"] = "Timed out; try again later",
        ["节点未响应直连检测，或不支持该检测；不代表游戏不可用"] = "Direct probe unavailable; games may still work",
        ["未收到节点响应；这不一定代表游戏不可用"] = "No node response; games may still work",
        ["网卡变化后需重新检测"] = "Retest after changing the adapter",
        ["请先在 Clash 中切换为“全局”模式。"] = "Switch Clash to Global mode first.",
        ["规则模式可能让部分设备流量直连，导致加速失败。"] = "Rule mode may bypass the proxy for some device traffic.",
        ["加速已结束。如果已填写参数，请恢复设备原来的 IP 与 DNS 设置：原先自动获取则改回自动，原先自定义则填回原值。"] =
            "Acceleration stopped. Restore the device's original IP and DNS: automatic if previously automatic, otherwise the original values.",
        ["状态连接失败。若已修改设备，请恢复原来的 IP 和 DNS 设置。停止最长需等待 8 秒。"] =
            "Status connection lost. Restore the device's original IP and DNS if changed. Stopping can take up to 8 seconds.",
        ["软件已在运行，请从任务栏或托盘打开。"] = "Already running. Open the window from the taskbar or tray.",
        ["软件发生异常，加速已请求停止。如已修改设备，请恢复原来的 IP 和 DNS 设置。"] =
            "An error occurred and acceleration was asked to stop. Restore the device's original IP and DNS if changed.",
        ["当前加速正在运行。退出将停止加速；请先准备好恢复设备原来的 IP 和 DNS 设置。继续？"] =
            "Acceleration is running. Exiting will stop it. Be ready to restore the device's original IP and DNS. Continue?",
        ["诊断包 (*.zip)|*.zip"] = "Diagnostics (*.zip)|*.zip",
        ["Clash 配置 (*.yaml;*.yml)|*.yaml;*.yml|所有文件|*.*"] = "Clash configuration (*.yaml;*.yml)|*.yaml;*.yml|All files|*.*",
        ["请输入有效的 HTTPS 订阅地址。"] = "Enter a valid HTTPS subscription URL.",
        ["订阅超过 8 MiB。"] = "Subscription exceeds 8 MiB.",
        ["请选择已连接的物理网卡。"] = "Select a connected physical adapter.",
        ["尚未发现已连接的物理网卡。"] = "No connected physical adapter found.",
        ["请选择物理网卡。"] = "Select a physical adapter.",
        ["加速会话已停止，请重新开始。"] = "The session stopped. Start acceleration again.",
        ["请先导入订阅，并为“{0}”选择独立节点。"] = "Import a subscription and select a node for {0}.",
        ["最多保存 32 份订阅，总内容不超过 24 MiB。请删除不再使用的订阅。"] =
            "Up to 32 subscriptions and 24 MiB total. Delete unused subscriptions.",
        ["TCP 重传 {0} · 超时 {1} · 待发送 {2} 包 · 最长发包等待 {3} ms"] =
            "TCP retransmits: {0} · Timeouts: {1} · Queued: {2} · Longest send wait: {3} ms",
        ["TCP 重传 {0} · 超时 {1} · 待发送 {2} 包 · 最长发包等待 {3} ms · 诊断已记录"] =
            "TCP retransmits: {0} · Timeouts: {1} · Queued: {2} · Longest send wait: {3} ms · Recorded",
        ["UDP 映射 · 同一会话重复 {0} · 跨目标 {1} · 跨会话 {2}"] =
            "UDP mapping · Repeat: {0} · Targets: {1} · Sessions: {2}",
        ["UDP 映射 · 未完成"] = "UDP mapping · Incomplete",
        ["稳定"] = "Stable", ["变化"] = "Changed", ["一致"] = "Same", ["不同"] = "Different",
        ["{0} / 不限量或未提供"] = "{0} / Unlimited or unspecified",
        ["关闭将停止网关。若已修改设备，请恢复原来的 IP 和 DNS 设置。\n停止后关闭？"] =
            "Closing stops the gateway. Restore the device's original IP and DNS if changed.\nStop and close?",
        ["停止只读检查并关闭？"] = "Stop the read-only check and close?",
        ["软件正在安装或卸载，请完成后再打开。"] = "Installation or removal is in progress. Finish it before opening the app.",
        ["设备配置不存在。"] = "Device configuration no longer exists.",
        ["请至少添加一台设备到加速。"] = "Add at least one device.",
        ["{0} 未收到有效网络参数。"] = "{0} did not receive valid network settings.",
        ["无法打开默认浏览器。"] = "Could not open the default browser.",
        ["系统连接"] = "System connection", ["直接连接"] = "Direct connection", ["本机 Clash 代理"] = "Local Clash proxy",
        ["订阅读取失败。"] = "Could not fetch subscription.",
        ["网络请求失败：{0}"] = "Network request failed: {0}",
        ["可导入已下载的 YAML 文件；仍失败时请提供此提示的完整截图。"] =
            "You can import a downloaded YAML file. If that also fails, provide a screenshot of this message.",
        ["连接或下载超时"] = "Connection or download timed out",
        ["下载过程中连接中断"] = "Connection lost during download",
        ["下载引擎不可用，请确认安装完整"] = "Download engine unavailable. Check that the installation is complete.",
        ["证书链不受这台 Windows 信任（可能缺少根或中间证书）"] =
            "This Windows installation does not trust the certificate chain (a root or intermediate may be missing)",
        ["证书与订阅域名不符"] = "Certificate does not match the subscription domain",
        ["证书已过期或尚未生效"] = "Certificate expired or not yet valid",
        ["证书校验失败"] = "Certificate validation failed",
        ["DNS 无法解析订阅域名"] = "DNS could not resolve the subscription domain",
        ["订阅返回重定向，请使用最终 HTTPS 地址"] = "Subscription redirected. Use the final HTTPS URL.",
        ["HTTP 状态 {0}"] = "HTTP status {0}",
        ["订阅超过 8 MiB"] = "Subscription exceeds 8 MiB",
        ["服务器返回了未协商的压缩格式"] = "Server returned an unnegotiated compression format",
        ["代理地址不支持兼容连接"] = "Proxy address is not supported by the compatibility connection",
        ["请输入有效的 HTTPS 订阅地址"] = "Enter a valid HTTPS subscription URL",
        ["连接或 TLS 握手失败"] = "Connection or TLS handshake failed",
        ["服务器未提供证书"] = "Server did not provide a certificate",
        ["证书已被吊销"] = "Certificate revoked",
        ["证书根不受这台 Windows 信任"] = "This Windows installation does not trust the certificate root",
        ["证书链不完整（缺少根或中间证书）"] = "Incomplete certificate chain (missing root or intermediate)",
        ["系统代理或代理隧道连接失败"] = "System proxy or proxy tunnel failed",
        ["TLS 握手失败"] = "TLS handshake failed",
        ["无法连接订阅服务器或代理"] = "Could not connect to the subscription server or proxy",
        ["服务器或代理返回了无效响应"] = "Server or proxy returned an invalid response",
        ["下载尚未完成，服务器已断开连接"] = "Server disconnected before the download completed",
        ["订阅读取失败，HTTP 状态 {0}。"] = "Could not fetch subscription. HTTP status {0}.",
        ["订阅读取失败，HTTP 状态 {0}。服务商拒绝了请求，请核对链接是否有效及订阅访问限制。"] =
            "Could not fetch subscription. HTTP status {0}. The provider refused the request. Check the URL and access restrictions.",
        ["{0}（兼容 TLS）：{1}"] = "{0} (compatibility TLS): {1}",
        ["{0}（{1}）"] = "{0} ({1})",
        ["{0}：{1}"] = "{0}: {1}",
        ["加速未自动启动。"] = "Acceleration was not started automatically.",
        ["已加载保存的订阅；加速未自动启动。"] = "Saved subscriptions loaded. Acceleration was not started automatically.",
        ["设置保存失败，本次已读取的订阅仍可使用；下次启动可能需要重新导入。"] =
            "Could not save settings. Loaded subscriptions still work; you may need to import them after restarting.",
        ["遗留的临时配置清理未完成；不会启动或终止其他进程。"] =
            "Could not finish cleaning old temporary settings. Other processes were left running.",
        ["保存的设置未能读取，原文件已保留；重新导入成功前不会覆盖它。"] =
            "Could not read saved settings. The original is kept until a successful import.",
        ["订阅读取成功 · {0}"] = "Subscription fetched · {0}",
        ["独立节点核心已准备，未创建 TUN 或修改电脑网络设置。"] =
            "Independent core ready. No TUN created or PC network settings changed.",
        ["独立节点核心缺失；请从完整发布目录运行。"] = "Independent core missing. Run from the complete release folder.",
        ["独立节点需要已连接的物理网卡。"] = "Independent mode requires a connected physical adapter.",
        ["无法启动独立节点核心。"] = "Could not start the independent core.",
        ["独立节点核心启动失败；节点参数、协议或端口可能不受支持。"] =
            "Independent core failed to start. The node settings, protocol or port may be unsupported.",
        ["独立节点的回环监听或 TUN 隔离检查未通过。"] = "Independent core failed its loopback or TUN isolation check.",
        ["设备会话需要 1 到 8 台设备。"] = "A session requires between 1 and 8 devices.",
        ["未能启动管理员辅助进程。"] = "Could not start the administrator helper.",
        ["软件长时间暂停，已请求停止网关。"] = "The app paused too long. Gateway stop requested.",
        ["辅助进程没有返回有效的新状态，已请求停止网关。"] = "The helper stopped reporting valid status. Gateway stop requested.",
        ["辅助进程异常退出；若已修改设备，请恢复原来的 IP 和 DNS 设置。"] =
            "The helper exited unexpectedly. Restore the device's original IP and DNS if changed.",
        ["辅助进程已退出，没有可用结果。"] = "The helper exited without a usable result.",
        ["加速会话不可用。"] = "Acceleration session unavailable.",
        ["加速会话已停止。"] = "Acceleration session stopped.",
        ["设备变更确认超时，已请求安全停止；请恢复设备原网络设置。"] =
            "Device update timed out. Stop requested; restore the device's original network settings.",
        ["请刷新网络信息。"] = "Refresh network information.",
        ["设备数量超过可分配的独立网络参数范围。"] = "Too many devices for the available network ranges.",
        ["需要已连接且有 IPv4 网关的物理网卡。"] = "A connected physical adapter with an IPv4 gateway is required.",
        ["该物理网卡开启了系统转发，需先确认其他软件的使用情况。"] =
            "System forwarding is enabled on this adapter. Check whether other software depends on it first.",
        ["需要 Windows Packet Filter 或 Npcap 其中一种；安装可能短暂断网，软件不会自动安装。"] =
            "Windows Packet Filter or Npcap is required. Installation may briefly disconnect the network and is not automatic.",
        ["请确认设备没有公网 IPv6；链路本地 fe80 地址不代表已确认。"] =
            "Confirm the device has no public IPv6. A link-local fe80 address is not sufficient confirmation.",
        ["此设备的专用网段与现有网卡网段冲突，停止自动分配。"] =
            "This device's network range conflicts with an existing adapter. Automatic assignment stopped.",
    };

    private static readonly (Regex Pattern, string English)[] Formats = Translations
        .Where(entry => entry.Key.Contains("{0}", StringComparison.Ordinal))
        .Select(entry => (new Regex("^" + Regex.Replace(Regex.Escape(entry.Key), @"\\\{(\d+)}",
            match => $"(?<p{match.Groups[1].Value}>.+?)") + "$",
            RegexOptions.CultureInvariant | RegexOptions.Singleline, TimeSpan.FromMilliseconds(50)), entry.Value)).ToArray();

    public static void Apply(string language)
    {
        language = language == "en-US" ? "en-US" : "zh-CN";
        if (Instance.Language == language) return;
        Instance.Language = language;
        Instance.PropertyChanged?.Invoke(Instance, new PropertyChangedEventArgs(nameof(Language)));
    }

    public static string Get(string text)
    {
        if (!IsEnglish || string.IsNullOrEmpty(text)) return text;
        if (Translations.TryGetValue(text, out var value)) return value;
        if (text.Contains('\n')) return string.Join("\n", text.Split('\n').Select(Get));
        foreach (var (pattern, english) in Formats)
        {
            var match = pattern.Match(text);
            if (!match.Success) continue;
            return Regex.Replace(english, @"\{(\d+)\}", part => Get(match.Groups["p" + part.Groups[1].Value].Value));
        }
        return text;
    }

    public static void Set(TextBlock target, string? text)
    {
        if (BindingOperations.GetMultiBinding(target, TextBlock.TextProperty)?.Converter is not TextTranslationConverter)
        {
            var binding = new MultiBinding { Mode = BindingMode.OneWay, Converter = new TextTranslationConverter() };
            binding.Bindings.Add(new Binding { Source = target, Path = new PropertyPath(SourceProperty) });
            binding.Bindings.Add(new Binding(nameof(Language)) { Source = Instance });
            target.SetBinding(TextBlock.TextProperty, binding);
        }
        target.SetValue(SourceProperty, text ?? "");
    }
}

public sealed class LocExtension(string text) : MarkupExtension
{
    public override object ProvideValue(IServiceProvider serviceProvider) =>
        new Binding(nameof(UiText.Language))
        {
            Source = UiText.Instance, Mode = BindingMode.OneWay,
            Converter = new StaticTranslationConverter(), ConverterParameter = text,
        }.ProvideValue(serviceProvider);
}

public sealed class TranslatedBindingExtension(string path) : MarkupExtension
{
    public string? Format { get; set; }
    public override object ProvideValue(IServiceProvider serviceProvider)
    {
        var binding = new MultiBinding { Mode = BindingMode.OneWay, Converter = new TextTranslationConverter(), ConverterParameter = Format };
        binding.Bindings.Add(new Binding(path));
        binding.Bindings.Add(new Binding(nameof(UiText.Language)) { Source = UiText.Instance });
        return binding.ProvideValue(serviceProvider);
    }
}

internal sealed class StaticTranslationConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, CultureInfo culture) => UiText.Get((string)parameter);
    public object ConvertBack(object value, Type targetType, object parameter, CultureInfo culture) => Binding.DoNothing;
}

internal sealed class TextTranslationConverter : IMultiValueConverter
{
    public object Convert(object[] values, Type targetType, object parameter, CultureInfo culture)
    {
        var text = values[0] as string ?? "";
        text = UiText.Get(text);
        return parameter is string format ? string.Format(culture, UiText.Get(format), text) : text;
    }
    public object[] ConvertBack(object value, Type[] targetTypes, object parameter, CultureInfo culture) =>
        targetTypes.Select(_ => Binding.DoNothing).ToArray();
}

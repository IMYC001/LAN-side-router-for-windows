using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;

namespace Accelerator.App;

public partial class MainWindow
{
    private async Task RenderSmokeTestAsync()
    {
        if (devices.Count != 1)
            throw new InvalidOperationException("首次启动必须且只能创建一台默认设备。");
        // These checks use loopback fixtures and rendered controls, never a LAN handle.
        PassiveProof.VerifyOffline();
        ProfileStore.VerifyOffline();
        SubscriptionProfile.VerifyOffline();
        await SubscriptionDownloader.VerifyOfflineAsync();
        await ProfileStore.VerifyRestartAsync();
        DiagnosticsExport.VerifyOffline();
        await IndependentCore.VerifyOfflineAsync();
        await NodeLatency.VerifyOfflineAsync();
        GatewaySession.VerifyOffline();
        await GatewaySession.VerifyControlOfflineAsync();
        InstallerMaintenance.VerifyOffline();
        DriverInstaller.VerifyOffline();
        var version = await EngineClient.CallAsync(new { command = "version" });
        if (version.GetProperty("networkTakeoverEnabled").GetBoolean())
            throw new InvalidOperationException("普通引擎入口不应自动启用网卡收发。");
        var simulation = await EngineClient.CallAsync(new { command = "selftest" });
        if (simulation.GetProperty("tcpBytes").GetInt32() != 26000 || simulation.GetProperty("udpSizes").GetArrayLength() != 4)
            throw new InvalidOperationException("离线转发验证失败。");
        await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(
            "tun: {enable: true}\nproxies: [{name: Offline AnyTLS, type: anytls, server: 203.0.113.1, port: 443, password: OFFLINE, udp: true}," +
            "{name: Offline Game UDP, type: ss, server: 203.0.113.2, port: 443, cipher: aes-128-gcm, password: OFFLINE, udp: true}]\n"));
        OutletBox.SelectedIndex = 1;
        MainTabs.SelectedIndex = 0;
        await FlushUiAsync();
        NodesList.SelectedIndex = 1;
        if (PrimaryDevice.SelectedNode != "Offline Game UDP" || PrimaryCard.QuickNodeBox.SelectedIndex != 1)
            throw new InvalidOperationException("订阅页与设备节点未同步。");
        PrimaryCard.QuickNodeBox.SelectedIndex = 0;
        if (NodeBox.SelectedIndex != 0 || NodesList.SelectedIndex != 0)
            throw new InvalidOperationException("设备节点与订阅页未同步。");

        var firstSubscription = activeSubscription!;
        firstSubscription.Name = "Game.yaml";
        firstSubscription.Upload = 1073741824;
        firstSubscription.Download = 95240899789;
        firstSubscription.Total = 214748364800;
        var secondSubscription = new SubscriptionProfile { Name = "备用线路.yaml", SelectedNode = "Restart Second" };
        await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(ProfileStore.RestartSubscription), secondSubscription);
        await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(firstSubscription.Content), firstSubscription);
        try
        {
            await ParseSubscriptionAsync("not a subscription"u8.ToArray());
            throw new IOException("错误订阅被接受。");
        }
        catch (InvalidOperationException) { }
        if (activeSubscription != firstSubscription || subscriptions.Count != 2)
            throw new InvalidOperationException("导入失败破坏已有订阅。");
        await DeleteSubscriptionAsync(firstSubscription);
        if (activeSubscription != secondSubscription)
            throw new InvalidOperationException("删除订阅后没有切换。");
        await DeleteSubscriptionAsync(secondSubscription);
        await FlushUiAsync();
        if (NodeOptions.Count != 0 || PrimaryCard.QuickNodeBox.Items.Count != 0)
            throw new InvalidOperationException("删除订阅后残留节点。");
        await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(secondSubscription.Content), secondSubscription);
        await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(firstSubscription.Content), firstSubscription);
        AdapterBox.ItemsSource = new[] { new AdapterInfo { Name = "WLAN", Description = "Offline adapter",
            IPv4 = "192.168.1.20", Prefix = 24, Gateway = "192.168.1.1", Physical = true, Status = "Up", Index = 11 } };
        AdapterBox.SelectedIndex = 0;
        await VerifyLatencyPresentationAsync(firstSubscription, secondSubscription);

        AddDevice_Click(AddDeviceButton, new RoutedEventArgs());
        if (devices.Count != 2 || !devices[1].IncludedInAcceleration)
            throw new InvalidOperationException("新增设备默认状态错误。");
        var first = PrimaryDevice;
        var second = devices[1];
        second.Name = "客厅主机";
        second.SelectedNode = "Offline Game UDP";
        MainTabs.SelectedIndex = 0;
        await FlushUiAsync();
        var secondCard = FindVisualChildren<DeviceCard>(AdditionalDeviceCards).Single();
        if (PrimaryCard.GetType() != secondCard.GetType() ||
            PrimaryCard.GatewayParameters.Visibility != Visibility.Collapsed ||
            secondCard.GatewayParameters.Visibility != Visibility.Collapsed)
            throw new InvalidOperationException("空闲设备必须使用相同组件且隐藏参数框。");

        var directory = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "..", "..", "test-results", "ui"));
        Directory.CreateDirectory(directory);
        foreach (var theme in new[] { "Dark", "Light" })
        {
            ApplyTheme(theme);
            foreach (var size in new[] { (800d, 560d), (720d, 500d) })
            {
                Width = size.Item1;
                Height = size.Item2;
                MainTabs.SelectedIndex = 0;
                await FlushUiAsync();
                SaveCardImage(PrimaryCard, $"{theme}-{(int)Width}-first-idle.png");
                SaveCardImage(secondCard, $"{theme}-{(int)Width}-second-idle.png");
                AssertSameLayout(PrimaryCard, secondCard);
                AssertNodeTheme(PrimaryCard);
                AssertNodeTheme(secondCard);
            }
        }

        // Start the second card first: neither button nor prompts on the first may change.
        second.ActionPreparing = true;
        PresentGatewayAction(true, true, false, false);
        if (first.ActionText != "开始加速" || second.ActionText != "准备中…" || first.ParametersReady)
            throw new InvalidOperationException("准备第二台设备影响第一台设备。");
        second.ActionPreparing = false;
        PrepareFixture(second, "10.253.84");
        activeDeviceIds.Add(second.Id);
        var secondStatus = FixtureStatus(second, "10.253.84.2", "02:11:22:33:44:77", 262_144_000);
        UpdateAllDevicePresentation(new GatewayStatus { Devices = [secondStatus] });
        PresentGatewayAction(true, false, false, false);
        RefreshDeviceViews();
        await FlushUiAsync();
        if (!second.ParametersReady || first.ParametersReady || first.Identity != "尚无设备接入" ||
            first.DownloadTotal != "累计 0 B" || second.DownloadSpeed != "100.0" ||
            secondCard.GatewayParameters.Visibility != Visibility.Visible ||
            PrimaryCard.GatewayParameters.Visibility != Visibility.Collapsed)
            throw new InvalidOperationException("只启动第二台设备时参数或连接状态错乱。");
        var secondTotal = second.DownloadTotal;
        var secondIdentity = second.Identity;
        first.ActionPreparing = true;
        PresentGatewayAction(true, true, false, false);
        if (first.ActionText != "准备中…" || second.ActionText != "停止加速" ||
            second.DownloadTotal != secondTotal || second.Identity != secondIdentity)
            throw new InvalidOperationException("准备第一台设备改变第二台设备运行状态。");
        first.ActionPreparing = false;
        PrepareFixture(first, "10.254.83");
        activeDeviceIds.Add(first.Id);
        var firstStatus = FixtureStatus(first, "10.254.83.2", "02:11:22:33:44:66", 524_288_000);
        UpdateAllDevicePresentation(new GatewayStatus { Devices = [firstStatus, secondStatus] });
        PresentGatewayAction(true, false, false, false);
        await FlushUiAsync();
        await VerifyLanguagePresentationAsync(secondCard, SaveWindowImage);
        foreach (var theme in new[] { "Dark", "Light" })
        {
            ApplyTheme(theme);
            await FlushUiAsync();
            AssertSameLayout(PrimaryCard, secondCard);
            AssertNodeTheme(PrimaryCard);
            AssertNodeTheme(secondCard);
            SaveCardImage(PrimaryCard, $"{theme}-first-active.png");
            SaveCardImage(secondCard, $"{theme}-second-active.png");
            ToggleDeviceParameters_Click(new Button { Tag = second }, new RoutedEventArgs());
            await FlushUiAsync();
            if (secondCard.GatewayParameters.Visibility != Visibility.Collapsed ||
                PrimaryCard.GatewayParameters.Visibility != Visibility.Visible)
                throw new InvalidOperationException("收起第二张卡片影响第一张卡片。");
            ToggleDeviceParameters_Click(new Button { Tag = second }, new RoutedEventArgs());
        }
        first.ActionEnding = true;
        PresentGatewayAction(true, true, false, false);
        if (second.ActionText != "停止加速" || first.ActionText != "正在停止…")
            throw new InvalidOperationException("停止按钮状态串到其他设备。");
        ClearDeviceRuntime(first, "加速已停止，请恢复原网络设置。");
        activeDeviceIds.Remove(first.Id);
        manualPlans.Remove(first.Id);
        UpdateAllDevicePresentation(new GatewayStatus { Devices = [firstStatus, secondStatus] });
        RefreshDeviceViews();
        if (first.ParametersReady || first.Identity != "尚无设备接入" || second.Identity != secondIdentity ||
            second.DownloadTotal != secondTotal || !second.ParametersReady)
            throw new InvalidOperationException("停止第一台或延迟状态快照影响另一台设备。");
        ClearDeviceRuntime(second);
        activeDeviceIds.Clear();
        manualPlans.Clear();
        RefreshDeviceViews();
        if (second.DownloadTotal != secondTotal)
            throw new InvalidOperationException("停止时不应清空累计流量。");
        ResetDeviceTraffic(first);
        if (second.DownloadTotal != secondTotal || first.DownloadTotal != "累计 0 B")
            throw new InvalidOperationException("重开单台设备重置了其他设备流量。");

        foreach (var theme in new[] { "Dark", "Light" })
        foreach (var accent in ThemeManager.Accents)
        {
            ApplyTheme(theme, accent);
            foreach (var size in new[] { (800d, 560d), (720d, 500d) })
            {
                Width = size.Item1;
                Height = size.Item2;
                for (int tab = 0; tab < MainTabs.Items.Count; tab++)
                {
                    MainTabs.SelectedIndex = tab;
                    await FlushUiAsync();
                    if (tab == 2)
                    {
                        busy = true;
                        UpdateControls();
                        await FlushUiAsync();
                        if (SubscriptionsList.Background is not SolidColorBrush brush ||
                            brush.Color != ((SolidColorBrush)FindResource("WindowBrush")).Color)
                            throw new InvalidOperationException("禁用订阅页主题背景错误。");
                        busy = false;
                        UpdateControls();
                    }
                    SaveWindowImage($"{theme}-{accent}-{(int)Width}-tab{tab}.png");
                }
            }
        }
        retryAfterTick = Environment.TickCount64 + 9000;
        UpdateControls();
        if (PrimaryCard.StartGatewayButton.IsEnabled || RefreshButton.IsEnabled)
            throw new InvalidOperationException("状态失联保护没有锁定重试。");
        retryAfterTick = 0;
        DeleteDevice_Click(new Button { Tag = second }, new RoutedEventArgs());
        DeleteDevice_Click(new Button { Tag = first }, new RoutedEventArgs());
        if (devices.Count != 1) throw new InvalidOperationException("至少一台设备保护失败。");
        await File.WriteAllTextAsync(Path.Combine(directory, "..", "desktop-checks.json"), JsonSerializer.Serialize(new
        {
            version = "0.5.14", passed = true, time = DateTime.UtcNow,
            checks = new[] { "Encrypted profile restart and corruption fixtures", "Subscription import, switch, delete and failure preservation",
                "Subscription TLS/transport fallbacks", "Loopback proxy isolation and cleanup", "Offline TCP/UDP relay",
                "Shared device card geometry, dropdown themes, parameters and collapse placement",
                "Second device starts before first; first stays idle without parameters, prompts or traffic",
                "Per-device prepare/stop buttons; retained connection identity and traffic",
                "Stopped-device stale snapshot rejection, totals retention, targeted statistics reset",
                "Light/dark with five accents and two compact window sizes",
                "Latency display, stale-result rejection and bounded concurrency", "Driver ownership and uninstall fixtures",
                "Status snapshot replacement and cooldown", "Revisioned device control acknowledgement, rejection and retry without stopping retained session" },
            languageChecksPassed = true,
            languageRestartChecksPassed = true,
            aboutChecksPassed = true,
            uninstallReparseFixturePassed = InstallerMaintenance.ReparseTestPassed,
            subscriptionFetchChecksPassed = true, subscriptionTlsChecksPassed = true,
            appAssemblySHA256 = Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(Path.Combine(AppContext.BaseDirectory, "Accelerator.dll")))),
            engineSHA256 = Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(Path.Combine(AppContext.BaseDirectory, "accelerator-engine.exe")))),
            physicalGatewayStarted = false,
        }, new JsonSerializerOptions { WriteIndented = true }));

        void SaveCardImage(DeviceCard card, string name) => SaveElementImage(card, name);
        void SaveWindowImage(string name) => SaveElementImage((FrameworkElement)Content, name);
        void SaveElementImage(FrameworkElement content, string name)
        {
            var bitmap = new RenderTargetBitmap(Math.Max(1, (int)Math.Ceiling(content.ActualWidth)),
                Math.Max(1, (int)Math.Ceiling(content.ActualHeight)), 96, 96, PixelFormats.Pbgra32);
            bitmap.Render(content);
            var encoder = new PngBitmapEncoder();
            encoder.Frames.Add(BitmapFrame.Create(bitmap));
            using var file = File.Create(Path.Combine(directory, name));
            encoder.Save(file);
        }
    }

    private async Task FlushUiAsync()
    {
        await Dispatcher.InvokeAsync(() => { }, DispatcherPriority.ApplicationIdle);
        UpdateLayout();
    }

    private async Task VerifyLanguagePresentationAsync(DeviceCard secondCard, Action<string> saveWindow)
    {
        if (MainTabs.Items.Count != 5 || MainTabs.Items.IndexOf(AboutTab) != 4 ||
            AboutIcon.Source is not BitmapSource { PixelWidth: >= 88 } ||
            ProjectLink.NavigateUri.AbsoluteUri != "https://github.com/IMYC001/LAN-side-router-for-windows")
            throw new InvalidOperationException("关于页位置、图标或项目链接错误。");
        var deviceReferences = devices.ToArray();
        var sessionReference = gatewaySession;
        var runtimeBefore = RuntimeSnapshot();
        UiText.Set(StatusText, "四项网络参数已复制");
        foreach (var language in new[] { "en-US", "zh-CN", "en-US" })
        {
            // Use the actual selector event, including while a device operation
            // is in progress. No real network session is opened by this fixture.
            busy = true;
            LanguageBox.SelectedValue = language;
            busy = false;
            await FlushUiAsync();
            if (runtimeBefore != RuntimeSnapshot() || !deviceReferences.SequenceEqual(devices) ||
                !ReferenceEquals(sessionReference, gatewaySession))
                throw new InvalidOperationException("切换语言影响了设备、会话或订阅状态。");
            var english = language == "en-US";
            if (Title != (english ? "LAN Device Acceleration (Side Router Mode)" : "局域网设备加速（旁路由模式）") ||
                PrimaryCard.GatewayActionText.Text != (english ? "Stop" : "停止加速") ||
                secondCard.GatewayActionText.Text != (english ? "Stop" : "停止加速") ||
                StatusText.Text != (english ? "Network settings copied" : "四项网络参数已复制") ||
                PrimaryCard.DownloadTotalText.Text != UiText.Get(PrimaryDevice.DownloadTotal) ||
                secondCard.CurrentOutletText.Text != (english ? "Independent" : "独立订阅") ||
                PrimaryCard.PrimaryPageTitle.Text != PrimaryDevice.Name ||
                secondCard.PrimaryPageTitle.Text != devices[1].Name ||
                PrimaryCard.ManualIPText.Text != PrimaryDevice.DeviceIP ||
                secondCard.ManualGatewayText.Text != devices[1].Gateway)
                throw new InvalidOperationException("语言切换未正确更新显示或改变了用户参数。");
        }
        foreach (var (source, expected) in new[]
        {
            ("测试完成 · 2 / 3 可达，请选择节点", "Test complete · 2 / 3 reachable; choose a node"),
            ("累计 12.5 GB", "Total 12.5 GB"),
            ("512 B / 不限量或未提供", "512 B / Unlimited or unspecified"),
            ("2 分钟前", "2 minutes ago"),
            ("正在测试 2 / 3；请选择节点", "Testing 2 / 3; choose a node"),
            ("到期时间未提供\n记录时间：2026-10-16 12:00", "Expiry unavailable\nRecorded: 2026-10-16 12:00"),
            ("系统连接（兼容 TLS）：证书已过期或尚未生效", "System connection (compatibility TLS): Certificate expired or not yet valid"),
        })
        {
            var actual = UiText.Get(source);
            if (actual != expected)
                throw new InvalidOperationException($"格式化提示翻译错误：{source} -> {actual}");
        }

        foreach (var theme in new[] { "Dark", "Light" })
        foreach (var size in new[] { (800d, 560d), (720d, 500d) })
        {
            ApplyTheme(theme);
            Width = size.Item1;
            Height = size.Item2;
            for (var tab = 0; tab < MainTabs.Items.Count; tab++)
            {
                MainTabs.SelectedIndex = tab;
                await FlushUiAsync();
                foreach (TabItem item in MainTabs.Items)
                {
                    var header = (FrameworkElement)item.Header;
                    if (header.DesiredSize.Width > header.ActualWidth + .5)
                        throw new InvalidOperationException("英文选项卡标题被裁切。");
                    foreach (var label in FindVisualChildren<TextBlock>(header))
                    {
                        var right = label.TransformToAncestor(item).Transform(new Point(label.ActualWidth, 0)).X;
                        if (right > item.ActualWidth - item.Padding.Right + .5)
                            throw new InvalidOperationException("英文选项卡文字超出内边距。");
                    }
                }
                if (tab == 0)
                {
                    AssertSameLayout(PrimaryCard, secondCard);
                    if (secondCard.NodeLatencyPanel.ToolTip as string !=
                        "Latency from this PC to the selected node, not the actual game latency")
                        throw new InvalidOperationException("英文延迟说明错误。");
                }
                if (tab == 4)
                {
                    var panel = (FrameworkElement)AboutIcon.Parent;
                    var parent = (FrameworkElement)panel.Parent;
                    var origin = panel.TransformToAncestor(parent).Transform(new Point());
                    if (panel.ActualWidth > parent.ActualWidth + .5 ||
                        Math.Abs(origin.X + panel.ActualWidth / 2 - parent.ActualWidth / 2) > .5 ||
                        Math.Abs(origin.Y + panel.ActualHeight / 2 - parent.ActualHeight / 2) > .5)
                        throw new InvalidOperationException("关于页未居中或内容超出紧凑窗口。");
                }
                saveWindow($"English-{theme}-{(int)Width}-tab{tab}.png");
            }
        }
        LanguageBox.SelectedValue = "zh-CN";
        MainTabs.SelectedIndex = 0;
        UiText.Set(StatusText, "准备就绪");
        await FlushUiAsync();

        string RuntimeSnapshot() => JsonSerializer.Serialize(new
        {
            devices = devices.Select(device => typeof(DeviceProfile).GetProperties()
                .ToDictionary(property => property.Name, property => property.GetValue(device))),
            activeDeviceIds = activeDeviceIds.Order().ToArray(), manualPlans,
            subscriptions, activeSubscriptionId = activeSubscription?.Id, subscriptionContent,
            nodeOptions = NodeOptions.ToArray(), selectedNode = NodeBox.SelectedItem,
            selectedSubscription = (SubscriptionsList.SelectedItem as SubscriptionProfile)?.Id,
            selectedAdapter = AdapterBox.SelectedItem, proxy = ProxyBox.Text,
            independent = Independent, stopping, retryAfterTick, sessionMode,
            coreIds = independentCores.Keys.Order().ToArray(), gatewayTimer.IsEnabled,
        });
    }

    private void PrepareFixture(DeviceProfile device, string prefix)
    {
        var plan = new ManualGatewayPlan { DeviceIP = prefix + ".2", Gateway = prefix + ".1",
            DNS = prefix + ".1", Netmask = "255.255.255.252", Prepared = true };
        manualPlans[device.Id] = plan;
        device.DeviceIP = plan.DeviceIP;
        device.Netmask = plan.Netmask;
        device.Gateway = plan.Gateway;
        device.DNS = plan.DNS;
        device.NotifyRuntime();
    }

    private static GatewayDeviceStatus FixtureStatus(DeviceProfile device, string ip, string mac, ulong downloaded) => new()
    {
        Id = device.Id, Phase = "connected", DeviceIP = ip, DeviceMAC = mac,
        DownloadBytesPerSecond = 12_500_000, UploadBytesPerSecond = 625_000,
        Stats = new GatewayStats { Downloaded = downloaded, Uploaded = 13_107_200 },
    };

    private static void AssertSameLayout(DeviceCard first, DeviceCard second)
    {
        foreach (var pair in new (FrameworkElement Left, FrameworkElement Right)[]
        {
            (first.StartGatewayButton, second.StartGatewayButton), (first.QuickNodeBox, second.QuickNodeBox),
            (first.TogglePrimaryParametersButton, second.TogglePrimaryParametersButton),
            (first.GatewayParameters, second.GatewayParameters),
        })
        {
            var a = pair.Left.TransformToAncestor(first).Transform(new Point());
            var b = pair.Right.TransformToAncestor(second).Transform(new Point());
            if (Math.Abs(a.X - b.X) > .5 || Math.Abs(a.Y - b.Y) > .5 ||
                Math.Abs(pair.Left.ActualWidth - pair.Right.ActualWidth) > .5)
                throw new InvalidOperationException($"设备卡片布局不一致：{pair.Left.Name}");
        }
    }

    private void AssertNodeTheme(DeviceCard card)
    {
        var common = (Style)FindResource(typeof(ComboBox));
        var template = common.Setters.OfType<Setter>().Single(item => item.Property == Control.TemplateProperty).Value;
        if (card.QuickNodeBox.Template != template ||
            card.QuickNodeBox.Background is not SolidColorBrush background ||
            background.Color != ((SolidColorBrush)FindResource("FieldBrush")).Color)
            throw new InvalidOperationException("节点选择没有使用统一主题模板。");
        if (card.NodeLatencyPanel.ToolTip as string != "这是电脑到你选的节点的延迟，不代表实际游戏延迟")
            throw new InvalidOperationException("节点延迟说明错误。");
    }
}

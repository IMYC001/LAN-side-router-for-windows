using Microsoft.Win32;
using System.Collections.ObjectModel;
using System.ComponentModel;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Security.Cryptography;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;

namespace Accelerator.App;

public partial class MainWindow : Window
{
    private NetworkSnapshot? snapshot;
    private bool busy;
    private ManualGatewayPlan? manualPlan;
    private readonly Dictionary<string, ManualGatewayPlan> manualPlans = [];
    private GatewaySession? gatewaySession;
    private readonly DispatcherTimer gatewayTimer = new() { Interval = TimeSpan.FromMilliseconds(500) };
    private readonly Dictionary<string, CancellationTokenSource> deviceLatencyCancellations = [];
    private string sessionMode = "";
    private string lastPhase = "";
    private bool stopping;
    private bool closeAfterStop;
    private long retryAfterTick;
    private const int MaxSubscriptionBytes = SubscriptionDownloader.MaxBytes;
    private bool SessionCoolingDown => Environment.TickCount64 < retryAfterTick;
    private readonly bool smokeTest = Environment.GetCommandLineArgs().Contains("--smoke-test");
    private readonly bool restartCheck = Environment.GetCommandLineArgs().Contains("--profile-restart-check");
    private readonly Dictionary<string, IndependentCore> independentCores = [];
    private IndependentCore? independentCore;
    private string subscriptionContent = "";
    private readonly ObservableCollection<SubscriptionProfile> subscriptions = [];
    private readonly ObservableCollection<DeviceProfile> devices = [];
    private readonly HashSet<string> activeDeviceIds = [];
    public ObservableCollection<string> NodeOptions { get; } = [];
    private SubscriptionProfile? activeSubscription;
    private bool synchronizingSubscriptions;
    private bool profileReady;
    private bool synchronizingNodes;
    private bool restoringProfile;
    private string savedNode = "";
    private string preferredAdapter = "";
    private string? lastSessionDirectory;
    private TrayIcon? tray;
    private bool Independent => OutletBox.SelectedIndex == 1;
    private bool Continuous => true;
    internal DeviceProfile PrimaryDevice => devices.First();
    private int IncludedDeviceCount => devices.Count(d => d.IncludedInAcceleration);
    private string EffectiveProxy => GetEffectiveProxy(PrimaryDevice);
    private List<DeviceProfile> ActiveDevices => devices.Where(device => activeDeviceIds.Contains(device.Id)).ToList();

    public MainWindow()
    {
        InitializeComponent();
        LoadAboutIcon();
        DataContext = this;
        LanguageBox.SelectedValue = UiText.Instance.Language;
        ApplyTheme("Dark");
        selectedLatencyTimer.Tick += (_, _) => RefreshSelectedLatency();
        SubscriptionsList.ItemsSource = subscriptions;
        if (devices.Count == 0) devices.Add(new DeviceProfile());
        PrimaryCard.DataContext = PrimaryDevice;
        DeviceManagerList.ItemsSource = devices;
        AdditionalDeviceCards.ItemsSource = devices.Skip(1).ToList();
        gatewayTimer.Tick += GatewayTick;
        Closing += Window_Closing;
        Closed += (_, _) =>
        {
            SystemEvents.PowerModeChanged -= PowerMode_Changed;
            EmergencyStop();
        };
        UpdateControls();
    }

    private void UpdateControls()
    {
        bool idle = !busy && gatewaySession is null && !SessionCoolingDown;
        foreach (var button in new[] { RefreshButton, ProbeButton, NATButton, FetchButton, ImportButton,
            SelfTestButton, ExportButton })
            button.IsEnabled = idle;
        AddDeviceButton.IsEnabled = idle && devices.Count < 8;
        LatencyButton.IsEnabled = latencyCancellation is not null || (idle && latencyRows.Count > 0);
        PresentGatewayAction(gatewaySession is not null, busy, stopping, SessionCoolingDown);
        foreach (Control control in new Control[] { AdapterBox, DevicesGrid, DeviceManagerList,
            ProxyBox, IPv6Check, SubscriptionUrlBox, OutletBox, NodeBox, NodesList, SubscriptionsList })
            control.IsEnabled = idle;
        ApplySubscriptionSurfaceTheme();
    }

    private void PresentGatewayAction(bool running, bool processing, bool ending, bool coolingDown)
    {
        foreach (var device in devices)
        {
            bool deviceRunning = activeDeviceIds.Contains(device.Id);
            bool deviceEnding = device.ActionEnding || (ending && deviceRunning);
            device.CanToggleAcceleration = device.IncludedInAcceleration && !ending && !coolingDown && !processing &&
                !device.ActionPreparing && !deviceEnding;
            device.ActionText = deviceEnding ? "正在停止…" : device.ActionPreparing ? "准备中…" :
                deviceRunning ? "停止加速" : "开始加速";
            device.ActionIcon = deviceRunning || deviceEnding ? "\uE71A" : "\uE768";
            device.ActionRunning = deviceRunning;
            device.CanSelectNode = !processing && !coolingDown && !deviceRunning && !device.ActionPreparing && !deviceEnding;
            device.UsesIndependentOutlet = Independent;
            device.OutletLabel = Independent ? "独立订阅" : "Clash 共存";
            device.NotifyRuntime();
        }
    }

    private void ToggleGateway_Click(object sender, RoutedEventArgs e)
    {
        _ = ToggleDeviceGatewayAsync(PrimaryDevice);
    }

    internal async void ToggleDeviceGateway_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: DeviceProfile device })
            await ToggleDeviceGatewayAsync(device);
    }

    internal void ProxySettings_Click(object sender, RoutedEventArgs e) => MainTabs.SelectedIndex = 3;

    private void RefreshDeviceViews()
    {
        if (devices.Count == 0) devices.Add(new DeviceProfile());
        PrimaryCard.DataContext = PrimaryDevice;
        foreach (var device in devices)
        {
            device.ParameterToggleIcon = device.ParametersExpanded ? "\uE70E" : "\uE70D";
            device.NotifyRuntime();
        }
        var additional = devices.Skip(1).ToList();
        if (AdditionalDeviceCards.ItemsSource is not List<DeviceProfile> current || !current.SequenceEqual(additional))
            AdditionalDeviceCards.ItemsSource = additional;
        RefreshDeviceLatencies();
        PresentGatewayAction(gatewaySession is not null, busy, stopping, SessionCoolingDown);
    }

    private void RefreshDeviceLatencies()
    {
        foreach (var device in devices)
        {
            var row = latencyRows.FirstOrDefault(item => item.Name == device.SelectedNode);
            device.NodeLatency = !Independent ? "--" : row?.Display.EndsWith(" ms") == true
                ? row.Display[..^3]
                : row?.Pending == true ? "…" : "--";
            device.UsesIndependentOutlet = Independent;
            device.OutletLabel = Independent ? "独立订阅" : "Clash 共存";
            device.ParameterToggleIcon = device.ParametersExpanded ? "\uE70E" : "\uE70D";
            device.NotifyRuntime();
        }
    }

    private void TogglePrimaryParameters_Click(object sender, RoutedEventArgs e)
    {
        PrimaryDevice.ParametersExpanded = !PrimaryDevice.ParametersExpanded;
        RefreshDeviceViews();
        SaveProfile();
    }

    internal void ToggleDeviceParameters_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: DeviceProfile device })
        {
            device.ParametersExpanded = !device.ParametersExpanded;
            device.ParameterToggleIcon = device.ParametersExpanded ? "\uE70E" : "\uE70D";
            device.NotifyRuntime();
            SaveProfile();
        }
    }

    internal void CopyDeviceParameters_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: DeviceProfile device }) return;
        if (!device.ParametersReady)
        {
            UiText.Set(StatusText, "此设备网关就绪后可复制网络参数");
            return;
        }
        CopyParameters(device.DeviceIP, device.Netmask, device.Gateway, device.DNS);
    }

    private void CopyParameters_Click(object sender, RoutedEventArgs e)
    {
        if (!PrimaryDevice.ParametersReady)
        {
            UiText.Set(StatusText, "开始加速后可复制网络参数");
            return;
        }
        try
        {
            CopyParameters(PrimaryDevice.DeviceIP, PrimaryDevice.Netmask, PrimaryDevice.Gateway, PrimaryDevice.DNS);
        }
        catch (System.Runtime.InteropServices.ExternalException)
        {
            UiText.Set(StatusText, "剪贴板暂时被占用，请稍后再试");
        }
    }

    private void CopyParameters(string ip, string mask, string gateway, string dns)
    {
        try
        {
            if (string.IsNullOrEmpty(gateway))
            {
                UiText.Set(StatusText, "开始加速后可复制网络参数");
                return;
            }
            Clipboard.SetText($"{UiText.Get("IPv4 地址")}：{ip}\n{UiText.Get("子网掩码")}：{mask}\n{UiText.Get("默认网关")}：{gateway}\n{UiText.Get("首选 DNS")}：{dns}");
            UiText.Set(StatusText, "四项网络参数已复制");
        }
        catch (System.Runtime.InteropServices.ExternalException)
        {
            UiText.Set(StatusText, "剪贴板暂时被占用，请稍后再试");
        }
    }

    private object CurrentPlanRequest(DeviceProfile? device = null, string? proxy = null)
    {
        if (AdapterBox.SelectedItem is not AdapterInfo adapter)
            throw new InvalidOperationException("尚未发现已连接的物理网卡。");
        var slot = device is null ? 0 : devices.IndexOf(device);
        if (slot < 0) throw new InvalidOperationException("设备配置不存在。");
        return new
        {
            adapterIndex = adapter.Index,
            slot,
            autoDiscover = true,
            proxy = proxy ?? GetEffectiveProxy(device ?? PrimaryDevice),
            ipv6Absent = IPv6Check.IsChecked == true,
            ipv6UnverifiedAccepted = false,
        };
    }

    private void InvalidateGateway()
    {
        if (PrimaryCard is null || gatewaySession is not null) return;
        manualPlan = null;
        manualPlans.Clear();
        foreach (var device in devices) ClearDeviceRuntime(device);
        if (SafetyText is not null) UiText.Set(SafetyText, "未启动 · 设备已修改时请恢复原来的 IP 和 DNS 设置");
        RefreshDeviceViews();
        PresentGatewayAction(gatewaySession is not null, busy, stopping, SessionCoolingDown);
    }

    private void GatewayInput_Changed(object sender, RoutedEventArgs e) => InvalidateGateway();

    private async Task PrepareManualPlanAsync(IReadOnlyList<DeviceProfile> targetDevices)
    {
        if (targetDevices.Count == 0) throw new InvalidOperationException("请至少添加一台设备到加速。");
        await EnsureCoresAsync(targetDevices);
        foreach (var device in targetDevices)
        {
            var data = await EngineClient.CallAsync(new
            {
                command = "manual-plan",
                plan = CurrentPlanRequest(device, GetEffectiveProxy(device)),
            });
            var plan = data.Deserialize<ManualGatewayPlan>(EngineClient.JsonOptions)
                ?? throw new InvalidOperationException($"{device.Name} 未收到有效网络参数。");
            manualPlans[device.Id] = plan;
            device.DeviceIP = plan.DeviceIP;
            device.Netmask = plan.Netmask;
            device.Gateway = plan.Gateway;
            device.DNS = plan.DNS;
            device.ParametersReady = false;
            device.State = plan.Prepared ? "正在启动网关" : "参数准备未完成";
            device.PreparationIssue = string.Join("；", plan.Checks.Where(check => check.Status == "block").Select(check => check.Detail));
            device.ParameterNote = "正在准备，暂勿填写设备网络参数。";
            device.NotifyRuntime();
        }
        manualPlan = manualPlans.GetValueOrDefault(PrimaryDevice.Id);
        RefreshDeviceViews();
        if (targetDevices.Any(device => !manualPlans[device.Id].Prepared))
            throw new InvalidOperationException(string.Join("\n", targetDevices
                .Where(device => device.PreparationIssue.Length > 0)
                .Select(device => $"{device.Name}：{device.PreparationIssue}")));
        MainTabs.SelectedIndex = 0;
        Log("手动参数预览完成，未打开网卡，网络配置修改次数为 0。");
    }

    private async void StartGateway_Click(object sender, RoutedEventArgs e)
    {
        await ToggleDeviceGatewayAsync(PrimaryDevice);
    }

    private async Task ToggleDeviceGatewayAsync(DeviceProfile device)
    {
        if (busy || stopping || SessionCoolingDown || !device.IncludedInAcceleration) return;
        if (activeDeviceIds.Contains(device.Id))
        {
            if (activeDeviceIds.Count == 1)
            {
                StopGateway_Click(this, new RoutedEventArgs());
                return;
            }
            busy = true;
            device.ActionEnding = true;
            UpdateControls();
            var session = gatewaySession!;
            try
            {
                await session.UpdateDevicesAsync(ActiveDevices.Where(item => item != device).Select(DeviceRequest).ToList());
                if (gatewaySession != session) return;
                activeDeviceIds.Remove(device.Id);
                manualPlans.Remove(device.Id);
                manualPlan = manualPlans.GetValueOrDefault(PrimaryDevice.Id);
                DisposeCore(device.Id);
                ClearDeviceRuntime(device, "加速已停止 · 请恢复此设备原来的 IP 和 DNS 设置");
                UiText.Set(StatusText, $"{device.Name} 已停止，其他设备继续加速");
            }
            catch (Exception ex)
            {
                Log(ex.Message);
                UiText.Set(StatusText, $"{device.Name} 未能完成停止：{ex.Message}");
            }
            finally
            {
                device.ActionEnding = false;
                busy = false;
                UpdateControls();
            }
            return;
        }
        await StartDeviceGatewayAsync(device);
    }

    private async Task StartDeviceGatewayAsync(DeviceProfile device)
    {
        if (busy || stopping || SessionCoolingDown) return;
        if (Independent && (subscriptionContent.Length == 0 || string.IsNullOrEmpty(device.SelectedNode)))
        {
            MainTabs.SelectedIndex = 2;
            UiText.Set(StatusText, "请先导入订阅，并为每台加入加速的设备选择节点");
            return;
        }
        if (IPv6Check.IsChecked != true)
        {
            MainTabs.SelectedIndex = 3;
            UiText.Set(StatusText, "请确认主机没有可用公网 IPv6，本版仅加速 IPv4");
            return;
        }
        if (!Independent) ShowClashWarning();

        busy = true;
        device.ActionPreparing = true;
        device.PreparationIssue = "";
        device.State = "正在准备";
        UpdateControls();
        UiText.Set(StatusText, gatewaySession is null ? "启动网关…" : "正在更新加速设备…");
        var previousSession = gatewaySession;
        try
        {
            await CancelLatencyAsync();
            // Rebinding the adapter is only safe before the shared session starts.
            if (previousSession is null) await ReadNetworkAsync();
            device.ActionPreparing = true;
            UpdateControls();
            await PrepareManualPlanAsync([device]);
            if (previousSession is null)
            {
                activeDeviceIds.Add(device.Id);
                ResetDeviceTraffic(device);
                StartSession("gateway", [device]);
            }
            else
            {
                if (gatewaySession != previousSession || stopping)
                    throw new IOException("加速会话已停止，请重新开始。");
                await previousSession.UpdateDevicesAsync(ActiveDevices.Append(device).Select(DeviceRequest).ToList());
                if (gatewaySession != previousSession)
                    throw new IOException("加速会话已停止，请重新开始。");
                ResetDeviceTraffic(device);
                activeDeviceIds.Add(device.Id);
                ApplyLatestSessionState();
            }
            UiText.Set(StatusText, $"{device.Name} 加速准备中");
        }
        catch (Exception ex)
        {
            activeDeviceIds.Remove(device.Id);
            manualPlans.Remove(device.Id);
            manualPlan = manualPlans.GetValueOrDefault(PrimaryDevice.Id);
            DisposeCore(device.Id);
            ClearDeviceRuntime(device);
            var message = ex is HttpRequestException http ? "网络请求失败：" + SubscriptionDownloader.Describe(http) : ex.Message;
            device.State = "启动未完成";
            device.PreparationIssue = message;
            device.NotifyRuntime();
            UiText.Set(StatusText, $"{device.Name} 启动未完成");
            Log(message);
            MessageBox.Show(this, UiText.Get(message), UiText.Get("操作未完成"), MessageBoxButton.OK, MessageBoxImage.Information);
        }
        finally
        {
            device.ActionPreparing = false;
            busy = false;
            SaveProfile();
            UpdateControls();
        }
    }

    private object DeviceRequest(DeviceProfile device)
    {
        var adapter = AdapterBox.SelectedItem as AdapterInfo
            ?? throw new InvalidOperationException("请选择物理网卡。");
        return new
        {
            id = device.Id,
            plan = CurrentPlanRequest(device, GetEffectiveProxy(device)),
            expected = new
            {
                adapterGUID = adapter.GUID, adapterIP = adapter.IPv4, adapterMAC = adapter.MAC,
                adapterPrefix = adapter.Prefix, router = adapter.Gateway,
                deviceIP = manualPlans[device.Id].DeviceIP, gateway = manualPlans[device.Id].Gateway,
            },
        };
    }

    private void StartSession(string mode, IReadOnlyList<DeviceProfile> targetDevices)
    {
        var adapter = AdapterBox.SelectedItem as AdapterInfo
            ?? throw new InvalidOperationException("请选择物理网卡。");
        if (targetDevices.Count == 0) throw new InvalidOperationException("请至少添加一台设备到加速。");
        object Expected(DeviceProfile device) => new
        {
            adapterGUID = adapter.GUID,
            adapterIP = adapter.IPv4,
            adapterMAC = adapter.MAC,
            adapterPrefix = adapter.Prefix,
            router = adapter.Gateway,
            deviceIP = mode == "gateway" ? manualPlans[device.Id].DeviceIP : "",
            gateway = mode == "gateway" ? manualPlans[device.Id].Gateway : "",
        };
        var sessionPrimary = targetDevices[0];
        var primaryPlan = CurrentPlanRequest(sessionPrimary, GetEffectiveProxy(sessionPrimary));
        var primaryExpected = Expected(sessionPrimary);
        gatewaySession = GatewaySession.StartMany(primaryPlan, mode, primaryExpected,
            targetDevices.Select(DeviceRequest).ToList(), continuous: mode == "gateway" && Continuous);
        if (mode == "gateway") lastSessionDirectory = gatewaySession.DirectoryPath;
        sessionMode = mode;
        stopping = false;
        lastPhase = "";
        UiText.Set(DetectedDeviceText, "设备 · 等待接入");
        foreach (var device in targetDevices)
        {
            device.State = "正在准备，等待设备接入";
            device.Identity = "尚无设备接入";
            device.NotifyRuntime();
        }
        UiText.Set(GatewaySpeedText, "捕获丢包 · 等待开始");
        UiText.Set(GatewayTrafficText, "UDP 会话 · 等待开始");
        UiText.Set(GatewayDiagnosticsText, mode == "passive" ? "" : "测速诊断 · 等待网关就绪");
        UiText.Set(SafetyText, mode == "passive" ? "只读检查 · 设备设置保持原样" : "网关准备中 · 暂勿填写设备");
        MainTabs.SelectedIndex = 0;
        gatewayTimer.Start();
        UpdateControls();
        Log(mode == "passive" ? "连接检查已启动，最长 20 秒。" :
            Continuous ? "持续网关已启动；电脑路由与地址未修改。" : "限时网关已启动，最长 180 秒；电脑路由与地址未修改。");
    }

    private static string FormatBytes(ulong value)
    {
        string[] units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"];
        double amount = value;
        int unit = 0;
        while (amount >= 1024 && unit < units.Length - 1) { amount /= 1024; unit++; }
        return unit == 0 ? $"{value} B" : $"{amount:F1} {units[unit]}";
    }

    private static void ResetDeviceTraffic(DeviceProfile device)
    {
        device.DownloadSpeed = device.UploadSpeed = "0.0";
        device.DownloadTotal = device.UploadTotal = "累计 0 B";
        device.NotifyRuntime();
    }

    private static void ClearDeviceRuntime(DeviceProfile device, string? note = null)
    {
        device.ParametersReady = false;
        device.DeviceIP = device.Netmask = device.Gateway = device.DNS = "";
        device.State = device.IncludedInAcceleration ? "等待开始加速" : "未加入加速";
        device.Identity = "尚无设备接入";
        device.DownloadSpeed = device.UploadSpeed = "0.0";
        device.ParameterNote = note ?? "开始加速后生成网络参数。";
        device.PreparationIssue = "";
        device.ActionPreparing = device.ActionEnding = false;
        device.NotifyRuntime();
    }

    private void UpdateDeviceTraffic(DeviceProfile device, GatewayDeviceStatus state, bool inactive)
    {
        device.DownloadSpeed = $"{(inactive ? 0 : state.DownloadBytesPerSecond * 8 / 1e6):F1}";
        device.UploadSpeed = $"{(inactive ? 0 : state.UploadBytesPerSecond * 8 / 1e6):F1}";
        device.DownloadTotal = $"累计 {FormatBytes(state.Stats.Downloaded)}";
        device.UploadTotal = $"累计 {FormatBytes(state.Stats.Uploaded)}";
    }

    private static string FormatElapsed(double seconds)
    {
        var elapsed = TimeSpan.FromSeconds(Math.Max(0, seconds));
        return $"{(long)elapsed.TotalHours:00}:{elapsed.Minutes:00}:{elapsed.Seconds:00}";
    }

    private void UpdateAllDevicePresentation(GatewayStatus state)
    {
        foreach (var result in state.Devices)
        {
            var device = devices.FirstOrDefault(item => item.Id == result.Id);
            if (device is null || !activeDeviceIds.Contains(device.Id)) continue;
            bool inactive = state.Stopped || stopping || device.ActionEnding;
            UpdateDeviceTraffic(device, result, inactive);
            if (state.Stopped)
            {
                ClearDeviceRuntime(device, "加速已结束，请恢复此设备原来的 IP 和 DNS 设置。");
                continue;
            }
            device.ParametersReady = !inactive && manualPlans.ContainsKey(device.Id) &&
                result.Phase is "waiting" or "identified" or "connected";
            device.ParameterNote = inactive ? "正在停止，请恢复此设备原来的 IP 和 DNS 设置。" :
                device.ParametersReady ? "网关已就绪，可在此设备填写以上参数。停止后恢复原设置。" :
                "正在准备，暂勿填写设备网络参数。";
            var connected = !inactive && result.Phase is "identified" or "connected" &&
                !string.IsNullOrWhiteSpace(result.DeviceMAC);
            device.State = connected
                ? result.Phase == "connected" ? "已连接 · 加速中" : "已连接 · 正在建立转发"
                : inactive ? "正在停止" : result.Phase == "failed" ? "连接失败" : "等待设备连接";
            device.Identity = connected
                ? $"{result.DeviceIP} · {result.DeviceMAC}"
                : "尚无设备接入";
            device.NotifyRuntime();
        }
        UiText.Set(DetectedDeviceText, string.Join("；", ActiveDevices
            .Where(device => device.Identity != "尚无设备接入")
            .Select(device => $"{device.Name} · {device.Identity}")));
        if (DetectedDeviceText.Text.Length == 0) UiText.Set(DetectedDeviceText, "设备 · 未连接");
    }

    private void ApplyLatestSessionState()
    {
        if (gatewaySession?.LatestStatus is { } state) UpdateAllDevicePresentation(state);
    }

    private void GatewayTick(object? sender, EventArgs e)
    {
        if (gatewaySession is null)
        {
            if (!SessionCoolingDown)
            {
                gatewayTimer.Stop();
                DisposeCore();
                UiText.Set(StatusText, "等待结束 · 可以重新开始加速");
                UpdateControls();
                if (closeAfterStop) Close();
            }
            return;
        }
        try
        {
            if (sessionMode == "gateway" && Independent &&
                ActiveDevices
                    .Any(device => !independentCores.TryGetValue(device.Id, out var core) || !core.Alive) && !stopping)
            {
                gatewaySession.RequestStop();
                stopping = true;
                MarkActiveDevicesStopping();
                UpdateControls();
                Log("独立节点核心已退出，正在停止网关。请恢复设备原网络设置。");
            }
            var state = gatewaySession?.Poll();
            if (state is null) return;
            UpdateAllDevicePresentation(state);
            var phase = state.Phase;
            var activeGateway = sessionMode == "gateway" && !state.Stopped && !stopping &&
                phase is "waiting" or "identified" or "connected";
            UiText.Set(SafetyText, activeGateway
                ? "网关已就绪 · 停止后恢复设备原来的 IP 和 DNS 设置"
                : sessionMode == "passive" ? "只读检查 · 设备设置保持原样" : "网关准备或已停止 · 设备已修改时请恢复原设置");
            UiText.Set(GatewayTrafficText, sessionMode == "passive"
                ? state.IdentityMethod == "windows-arp" ? "主机身份 · Windows ARP 核对完成" :
                    $"目标帧 {state.Received} · ARP {state.ARP} · IPv4 {state.IPv4}"
                : $"UDP {state.Stats.UDPStarted} 流 / {state.Stats.UDPAssociations} 代理会话");
            UiText.Set(GatewaySpeedText, sessionMode == "passive" ? "" :
                (state.Capture.Available ? $"捕获丢包 {state.Capture.Dropped:N0}" : "捕获丢包未知"));
            UiText.Set(GatewayDiagnosticsText, sessionMode == "passive" ? "" :
                $"TCP 重传 {state.Stats.TCPRetransmits:N0} · 超时 {state.Stats.TCPTimeouts:N0} · " +
                $"待发送 {state.PendingFrames + state.Stats.PendingPackets:N0} 包 · " +
                $"最长发包等待 {state.Transmit.LongestMillis:F1} ms" +
                (state.TraceSaved ? " · 诊断已记录" : ""));
            if (phase != lastPhase)
            {
                Log(state.Message);
                lastPhase = phase;
            }
            if (!state.Stopped) return;
            gatewayTimer.Stop();
            bool restore = sessionMode == "gateway";
            gatewaySession!.Dispose();
            gatewaySession = null;
            stopping = false;
            UiText.Set(StatusText, restore ? "加速已结束 · 请恢复设备原来的 IP 和 DNS 设置" : "只读检查已结束");
            if (restore)
            {
                foreach (var device in ActiveDevices)
                    ClearDeviceRuntime(device, "加速已结束，请恢复此设备原来的 IP 和 DNS 设置。");
                activeDeviceIds.Clear();
                DisposeCore();
                manualPlan = null;
                manualPlans.Clear();
                RestoreFromTray();
                if (!closeAfterStop) MessageBox.Show(this,
                    UiText.Get("加速已结束。如果已填写参数，请恢复设备原来的 IP 与 DNS 设置：原先自动获取则改回自动，原先自定义则填回原值。"),
                    UiText.Get("恢复设备网络设置"), MessageBoxButton.OK, MessageBoxImage.Information);
            }
            RefreshDeviceViews();
            UpdateControls();
            if (closeAfterStop) Close();
        }
        catch (Exception)
        {
            LoseSession();
        }
    }

    private void LoseSession()
    {
        // Stop renewing the lease even when writing the stop file fails.
        gatewayTimer.Stop();
        try { gatewaySession?.Dispose(); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { }
        gatewaySession = null;
        stopping = false;
        activeDeviceIds.Clear();
        // Wait beyond the helper's eight-second lease before allowing a retry.
        retryAfterTick = Environment.TickCount64 + 9000;
        InvalidateGateway();
        UiText.Set(StatusText, "状态连接失败 · 测试将在心跳超时后停止");
        Log("状态连接失败。辅助进程将因心跳过期停止；若已修改设备，请恢复原来的 IP 和 DNS 设置。");
        gatewayTimer.Start();
        UpdateControls();
        MessageBox.Show(this, UiText.Get("状态连接失败。若已修改设备，请恢复原来的 IP 和 DNS 设置。停止最长需等待 8 秒。"),
            UiText.Get("测试停止"), MessageBoxButton.OK, MessageBoxImage.Information);
    }

    private void StopGateway_Click(object sender, RoutedEventArgs e)
    {
        if (gatewaySession is null) return;
        try
        {
            gatewaySession?.RequestStop();
            stopping = true;
            MarkActiveDevicesStopping();
            UiText.Set(SafetyText, "正在停止 · 请恢复设备原来的 IP 和 DNS 设置");
            foreach (var device in ActiveDevices)
            {
                device.State = "正在停止";
                device.NotifyRuntime();
            }
            UpdateControls();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            LoseSession();
        }
    }

    private void MarkActiveDevicesStopping()
    {
        foreach (var device in ActiveDevices)
        {
            device.ActionEnding = true;
            device.ParametersReady = false;
            device.State = "正在停止";
            device.Identity = "尚无设备接入";
            device.DownloadSpeed = device.UploadSpeed = "0.0";
            device.ParameterNote = "正在停止，请恢复此设备原来的 IP 和 DNS 设置。";
            device.NotifyRuntime();
        }
    }

    internal void EmergencyStop()
    {
        latencyCancellation?.Cancel();
        selectedLatencyCancellation?.Cancel();
        selectedLatencyTimer.Stop();
        closingLatency = true;
        gatewayTimer.Stop();
        try { gatewaySession?.Dispose(); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { }
        DisposeCore();
        tray?.Dispose();
        tray = null;
    }

    private async void Window_Closing(object? sender, CancelEventArgs e)
    {
        if (busy)
        {
            e.Cancel = true;
            UiText.Set(StatusText, "请等待当前操作结束后再退出");
            return;
        }
        if (gatewaySession is null)
        {
            if (SessionCoolingDown) { e.Cancel = true; closeAfterStop = true; return; }
            closingLatency = true;
            selectedLatencyTimer.Stop();
            if (!latencyTask.IsCompleted || !selectedLatencyTask.IsCompleted)
            {
                e.Cancel = true;
                await CancelLatencyAsync();
                Close();
                return;
            }
            SaveProfile();
            return;
        }
        e.Cancel = true;
        if (closeAfterStop) return;
        if (MessageBox.Show(this,
            UiText.Get(sessionMode == "gateway" ? "关闭将停止网关。若已修改设备，请恢复原来的 IP 和 DNS 设置。\n停止后关闭？" : "停止只读检查并关闭？"),
            UiText.Get("停止测试"), MessageBoxButton.OKCancel, MessageBoxImage.Information) != MessageBoxResult.OK) return;
        closeAfterStop = true;
        StopGateway_Click(this, new RoutedEventArgs());
    }

    private async void Window_Loaded(object sender, RoutedEventArgs e)
    {
        CenterCompactWindow();
        if (smokeTest)
        {
            await RenderSmokeTestAsync();
            Application.Current.Shutdown();
            return;
        }
        if (!restartCheck)
        {
            tray = new TrayIcon(this, () => StopGateway_Click(this, new RoutedEventArgs()), Close);
            SystemEvents.PowerModeChanged += PowerMode_Changed;
        }
        try { if (!restartCheck) IndependentCore.CleanupStaleRuntime(); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        { Log("遗留的临时配置清理未完成；不会启动或终止其他进程。"); }
        AppProfile profile = new();
        var loaded = true;
        try { profile = ProfileStore.Load(); }
        catch (Exception ex) when (ex is IOException or System.Security.Cryptography.CryptographicException or InvalidOperationException or JsonException or UnauthorizedAccessException)
        {
            loaded = false;
            Log("保存的设置未能读取，原文件已保留；重新导入成功前不会覆盖它。");
        }
        restoringProfile = true;
        ApplyLanguage(profile.Language);
        ApplyTheme(profile.Theme, profile.Accent);
        foreach (var item in profile.Subscriptions) subscriptions.Add(item);
        devices.Clear();
        foreach (var device in profile.Devices) devices.Add(device);
        if (devices.Count == 0) devices.Add(new DeviceProfile());
        // Device cards are now always managed from the multi-device page and
        // are added to acceleration immediately. Migrate profiles written by
        // older builds so a stale opt-out flag cannot hide a saved device.
        foreach (var device in devices)
        {
            device.IncludedInAcceleration = true;
            if (device.State == "未加入加速")
                device.State = "等待开始加速";
        }
        var restoredSubscription = subscriptions.FirstOrDefault(s => s.Id == profile.ActiveSubscriptionId) ?? subscriptions.FirstOrDefault();
        subscriptionContent = restoredSubscription?.Content ?? "";
        savedNode = restoredSubscription?.SelectedNode ?? "";
        activeSubscription = restoredSubscription;
        RefreshSubscriptionCards();
        ProxyBox.Text = profile.Proxy;
        preferredAdapter = profile.AdapterGUID;
        OutletBox.SelectedIndex = profile.Independent ? 1 : 0;
        if (restoredSubscription is not null)
            await WorkAsync("恢复加密订阅…", async () =>
            {
                await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(restoredSubscription.Content), restoredSubscription);
            });
        restoringProfile = false;
        profileReady = loaded;
        RefreshDeviceViews();
        UpdateOutletStatus();
        if (restartCheck)
        {
            if (!loaded || !Independent || subscriptionContent != ProfileStore.RestartSubscription ||
                NodeBox.SelectedItem as string != "Restart Second" || subscriptions.Count != 2 ||
                activeSubscription?.Name != "Second" || NodesList.Items.Count != 2 || gatewaySession is not null ||
                ThemeManager.Current != "Dark" || DarkThemeOption.IsChecked != true ||
                ThemeManager.Accent != "LightSalmon" || SalmonAccentOption.IsChecked != true ||
                UiText.Instance.Language != "en-US" || LanguageBox.SelectedValue as string != "en-US")
                throw new InvalidOperationException("关闭再打开后的订阅或节点恢复失败。");
            SaveProfile();
            await File.WriteAllTextAsync(Path.Combine(ProfileStore.Root, "restart-passed"), "passed");
            Application.Current.Shutdown();
            return;
        }
        await RefreshAsync();
        foreach (var device in devices)
            RequestDeviceLatency(device);
        selectedLatencyTimer.Start();
        RefreshSelectedLatency();
        Log(subscriptionContent.Length > 0 ? "已加载保存的订阅；加速未自动启动。" : "加速未自动启动。");
    }

    private void CenterCompactWindow()
    {
        var handle = new System.Windows.Interop.WindowInteropHelper(this).Handle;
        var area = System.Windows.Forms.Screen.FromHandle(handle).WorkingArea;
        var transform = PresentationSource.FromVisual(this)?.CompositionTarget?.TransformFromDevice ?? Matrix.Identity;
        var topLeft = transform.Transform(new Point(area.Left, area.Top));
        var available = transform.Transform(new Vector(area.Width, area.Height));
        double width = Math.Max(1, available.X - 24);
        double height = Math.Max(1, available.Y - 24);
        MinWidth = Math.Min(720, width);
        MinHeight = Math.Min(500, height);
        Width = Math.Min(800, width);
        Height = Math.Min(560, height);
        Left = topLeft.X + (available.X - Width) / 2;
        Top = topLeft.Y + (available.Y - Height) / 2;
    }

    private void Log(string text)
    {
        LogBox.AppendText($"[{DateTime.Now:HH:mm:ss}] {UiText.Get(text)}{Environment.NewLine}");
        LogBox.ScrollToEnd();
    }

    private async Task WorkAsync(string description, Func<Task> work)
    {
        if (busy || gatewaySession is not null || SessionCoolingDown) return;
        busy = true;
        UpdateControls();
        UiText.Set(StatusText, description);
        try
        {
            await CancelLatencyAsync();
            await work();
            UiText.Set(StatusText, gatewaySession is null ? "完成 · 电脑网络配置未修改" : "加速准备中");
        }
        catch (OperationCanceledException)
        {
            UiText.Set(StatusText, "操作超时或已取消");
            Log("操作超时或已取消，网络配置未修改。");
        }
        catch (Exception ex)
        {
            var text = ex is HttpRequestException http ? "网络请求失败：" + SubscriptionDownloader.Describe(http) : ex.Message;
            UiText.Set(StatusText, "未完成 · 网络配置未修改");
            Log(text);
            MessageBox.Show(this, UiText.Get(text), UiText.Get("操作未完成"), MessageBoxButton.OK, MessageBoxImage.Information);
        }
        finally
        {
            busy = false;
            SaveProfile();
            UpdateControls();
            if (pendingLatency)
            {
                pendingLatency = false;
                BeginLatencyTest();
            }
            RefreshSelectedLatency();
        }
    }

    private Task RefreshAsync() => WorkAsync("读取网卡和邻居表…", ReadNetworkAsync);

    private async Task ReadNetworkAsync()
    {
        var oldIndex = (AdapterBox.SelectedItem as AdapterInfo)?.Index;
        var data = await EngineClient.CallAsync(new { command = "diagnostics" });
        snapshot = data.Deserialize<NetworkSnapshot>(EngineClient.JsonOptions)!;
        if (oldIndex is null && ProxyBox.Text == "127.0.0.1:7890" && !snapshot.Listeners.Any(l => l.Port == 7890))
        {
            var detected = snapshot.Listeners.FirstOrDefault(l => l.Address is "127.0.0.1" or "0.0.0.0");
            if (detected is not null)
                ProxyBox.Text = $"127.0.0.1:{detected.Port}";
        }
        var adapters = snapshot.Adapters.Where(a => a.Physical && a.Status == "Up" && !string.IsNullOrEmpty(a.IPv4)).ToList();
        AdapterBox.ItemsSource = adapters;
        AdapterBox.SelectedItem = adapters.FirstOrDefault(a => a.Index == oldIndex) ??
            adapters.FirstOrDefault(a => a.GUID == preferredAdapter) ?? adapters.FirstOrDefault();
        UiText.Set(ListenerText, snapshot.Listeners.Count == 0
            ? "没有发现常用代理端口。可填写 Clash 设置中的本机 SOCKS / Mixed 端口。"
            : "已监听的常用端口：" + string.Join("、", snapshot.Listeners.Select(l => $"{l.Address}:{l.Port}").Distinct()));
        UiText.Set(SafetyText, snapshot.Npcap || snapshot.WinpkFilter
            ? "未启动 · 填写前记录设备原设置，停止后恢复原设置。"
            : "未启动 · 需要 Windows Packet Filter 或 Npcap；软件不会自动安装驱动。");
        Log($"读取完成：{adapters.Count} 个已连接物理网卡，{snapshot.Neighbors.Count} 条已知 IPv4 邻居记录。没有主动扫描或修改网络。");
    }

    private async void Refresh_Click(object sender, RoutedEventArgs e) => await RefreshAsync();

    private async void SelfTest_Click(object sender, RoutedEventArgs e) => await WorkAsync("运行内存 TCP / UDP 转发验证…", async () =>
    {
        var result = await EngineClient.CallAsync(new { command = "selftest" });
        var sizes = result.GetProperty("udpSizes").Deserialize<List<int>>()!;
        Log($"离线验证通过：TCP {result.GetProperty("tcpBytes").GetInt32():N0} 字节；UDP {string.Join("、", sizes)} 字节往返。未使用真实网卡或公网。");
        MainTabs.SelectedIndex = 3;
    });

    private void Adapter_Changed(object sender, SelectionChangedEventArgs e)
    {
        latencyCancellation?.Cancel();
        foreach (var row in latencyRows) row.Set("未测试", "网卡变化后需重新检测");
        RefreshSelectedLatency(true);
        InvalidateGateway();
        if (AdapterBox.SelectedItem is not AdapterInfo a || snapshot is null) return;
        UiText.Set(AdapterDetails, $"电脑 {a.IPv4}/{a.Prefix}   ·   主路由 {a.Gateway}   ·   系统 IP 转发 {(a.Forwarding ? "已开启" : "未开启")}");
        var rows = snapshot.Neighbors.Where(n => n.Index == a.Index).Select(n => new NeighborInfo
        {
            IP = n.IP, MAC = n.MAC, Index = n.Index, State = n.State,
        }).ToList();
        DevicesGrid.ItemsSource = rows;
    }

    private async void Probe_Click(object sender, RoutedEventArgs e) => await WorkAsync("检查代理 TCP / UDP…", async () =>
    {
        await EnsureCoreAsync();
        UiText.Set(TCPResult, "TCP · 检查中");
        UiText.Set(UDPResult, "UDP · 检查中");
        var data = await EngineClient.CallAsync(new { command = "probe", proxy = EffectiveProxy });
        var tcp = data.GetProperty("tcp").GetBoolean();
        var udp = data.GetProperty("udp").GetBoolean();
        UiText.Set(TCPResult, $"TCP · {(tcp ? "通过" : "未通过")} · {data.GetProperty("tcpMs").GetInt64()} ms\n{data.GetProperty("tcpInfo").GetString()}");
        UiText.Set(UDPResult, $"UDP · {(udp ? "通过" : "未通过")} · {data.GetProperty("udpMs").GetInt64()} ms\n{data.GetProperty("udpInfo").GetString()}");
        UiText.Set(ProbeScope, data.GetProperty("scope").GetString());
        Log($"代理入口检查：TCP {(tcp ? "通过" : "未通过")}，UDP {(udp ? "通过" : "未通过")}。没有建立设备接管。");
    });

    private async void NATProbe_Click(object sender, RoutedEventArgs e) => await WorkAsync("检查代理 UDP 映射（最长 22 秒）…", async () =>
    {
        await EnsureCoreAsync();
        UiText.Set(NATResult, "UDP 映射 · 检查中");
        var data = await EngineClient.CallAsync(new { command = "nat-probe", proxy = EffectiveProxy });
        var complete = data.GetProperty("complete").GetBoolean();
        UiText.Set(NATResult, complete
            ? $"UDP 映射 · 同一会话重复 {(data.GetProperty("stableOnRepeat").GetBoolean() ? "稳定" : "变化")} · " +
              $"跨目标 {(data.GetProperty("sameAcrossDestinations").GetBoolean() ? "一致" : "不同")} · " +
              $"跨会话 {(data.GetProperty("sameAcrossAssociations").GetBoolean() ? "一致" : "不同")}\n"
            : "UDP 映射 · 未完成\n");
        UiText.Set(NATResult, (string)NATResult.GetValue(UiText.SourceProperty) + data.GetProperty("note").GetString());
        Log(NATResult.Text.Replace('\n', ' '));
        Log(data.GetProperty("scope").GetString() ?? "");
    });

    private async Task ParseSubscriptionAsync(byte[] bytes, SubscriptionProfile? item = null, bool activate = true)
    {
        if (bytes.Length > MaxSubscriptionBytes)
            throw new InvalidOperationException("订阅超过 8 MiB。");
        var content = new UTF8Encoding(false, true).GetString(bytes);
        content = content.TrimStart('\uFEFF');
        var data = await EngineClient.CallAsync(new { command = "subscription", subscription = content });
        item ??= new SubscriptionProfile();
        var index = subscriptions.ToList().FindIndex(s => s.Id == item.Id);
        if ((index < 0 && subscriptions.Count >= 32) ||
            subscriptions.Where(s => s.Id != item.Id).Sum(s => (long)Encoding.UTF8.GetByteCount(s.Content)) + bytes.Length > 24 * 1024 * 1024)
            throw new InvalidOperationException("最多保存 32 份订阅，总内容不超过 24 MiB。请删除不再使用的订阅。");
        item.Content = content;
        synchronizingSubscriptions = true;
        if (index >= 0) subscriptions[index] = item;
        else subscriptions.Add(item);
        synchronizingSubscriptions = false;
        if (!activate)
        {
            RefreshSubscriptionCards();
            if (!restoringProfile) profileReady = true;
            SaveProfile();
            return;
        }
        DisposeCore();
        activeSubscription = item;
        subscriptionContent = content;
        UiText.Set(SubscriptionResult, $"{item.Name} · {data.GetProperty("nodes").GetInt32()} 个节点");
        var selected = item.SelectedNode;
        var names = data.GetProperty("names").Deserialize<List<string>>();
        synchronizingNodes = true;
        NodeOptions.Clear();
        foreach (var name in names ?? []) NodeOptions.Add(name);
        NodeBox.ItemsSource = NodeOptions;
        ResetLatencyRows(names ?? []);
        // The selected node belongs to the subscription. A device selection is preferred only
        // when it is available in the newly activated subscription.
        var preferredNode = PrimaryDevice.SelectedNode;
        NodeBox.SelectedItem = NodeOptions.Contains(preferredNode) ? preferredNode : selected;
        if (NodeBox.SelectedItem is null && NodeBox.Items.Count > 0) NodeBox.SelectedIndex = 0;
        SyncNodeList();
        synchronizingNodes = false;
        savedNode = NodeBox.SelectedItem as string ?? "";
        item.SelectedNode = savedNode;
        PrimaryDevice.SelectedNode = savedNode;
        foreach (var device in devices.Where(d => !NodeOptions.Contains(d.SelectedNode)))
            device.SelectedNode = savedNode;
        RefreshDeviceLatencies();
        RefreshSubscriptionCards();
        InvalidateGateway();
        UpdateOutletStatus();
        RefreshSelectedLatency(true);
        if (!restoringProfile) profileReady = true;
        SaveProfile();
        Log("订阅解析完成。地址和节点凭据将由当前 Windows 账户加密保存，不写入日志。");
    }

    private string GetEffectiveProxy(DeviceProfile device)
    {
        if (!Independent) return ProxyBox.Text.Trim();
        if (independentCores.TryGetValue(device.Id, out var core) && core.Alive)
            return core.Proxy;
        if (device == PrimaryDevice && independentCore is { Alive: true })
            return independentCore.Proxy;
        throw new InvalidOperationException($"{device.Name} 的独立节点尚未准备，请点击开始加速或检查 TCP / UDP。");
    }

    private async Task EnsureCoreAsync(DeviceProfile? target = null)
    {
        if (!Independent) return;
        var device = target ?? PrimaryDevice;
        if (AdapterBox.SelectedItem is not AdapterInfo adapter)
            throw new InvalidOperationException("请选择已连接的物理网卡。");
        var node = string.IsNullOrWhiteSpace(device.SelectedNode) ? NodeBox.SelectedItem as string : device.SelectedNode;
        if (string.IsNullOrWhiteSpace(node) || subscriptionContent.Length == 0)
            throw new InvalidOperationException($"请先导入订阅，并为“{device.Name}”选择独立节点。");
        if (independentCores.TryGetValue(device.Id, out var existing) && existing.Alive &&
            existing.Node == node && existing.AdapterGUID == adapter.GUID)
            return;
        DisposeCore(device.Id);
        var core = await IndependentCore.StartAsync(subscriptionContent, node, adapter);
        try { await core.VerifyIsolationAsync(); }
        catch { core.Dispose(); throw; }
        independentCores[device.Id] = core;
        if (device == PrimaryDevice) independentCore = core;
        UpdateOutletStatus();
        Log("独立节点核心已准备，未创建 TUN 或修改电脑网络设置。");
    }

    private async Task EnsureCoresAsync(IReadOnlyList<DeviceProfile> targetDevices)
    {
        if (!Independent) return;
        var started = new List<string>();
        try
        {
            foreach (var device in targetDevices)
            {
                var hadCore = independentCores.ContainsKey(device.Id);
                await EnsureCoreAsync(device);
                if (!hadCore) started.Add(device.Id);
            }
        }
        catch
        {
            foreach (var id in started) DisposeCore(id);
            throw;
        }
    }

    private void DisposeCore(string id)
    {
        if (!independentCores.Remove(id, out var core)) return;
        core.Dispose();
        if (independentCore == core) independentCore = null;
    }

    private void DisposeCore()
    {
        foreach (var core in independentCores.Values) core.Dispose();
        independentCores.Clear();
        independentCore = null;
        if (OutletStatus is not null) UpdateOutletStatus();
    }

    private void UpdateOutletStatus()
    {
        if (OutletBox is null || NodeBox is null || ExistingProxyPanel is null || OutletStatus is null) return;
        // NodeBox is an internal synchronization source. Node choices belong to
        // the device cards and the home card, so never render this duplicate.
        NodeBox.Visibility = Visibility.Collapsed;
        ExistingProxyPanel.Visibility = Independent ? Visibility.Collapsed : Visibility.Visible;
        if (ClashWarning is not null) ClashWarning.Visibility = Independent ? Visibility.Collapsed : Visibility.Visible;
        foreach (var device in devices)
        {
            device.UsesIndependentOutlet = Independent;
            device.OutletLabel = Independent ? "独立订阅" : "Clash 共存";
            device.NotifyRuntime();
        }
        RefreshDeviceLatencies();
        UiText.Set(OutletStatus, !Independent ? "使用现有代理入口" :
            independentCores.TryGetValue(PrimaryDevice.Id, out var core) && core.Alive ? "独立节点已准备" :
            NodeBox.Items.Count == 0 ? "请先导入订阅" : "独立节点待准备");
    }

    private void Outlet_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (OutletStatus is null) return;
        DisposeCore();
        InvalidateGateway();
        UpdateOutletStatus();
        RefreshSelectedLatency(true);
        if (profileReady && !restoringProfile && !smokeTest && !Independent) ShowClashWarning();
        SaveProfile();
    }

    private void ShowClashWarning() => MessageBox.Show(this,
        UiText.Get("请先在 Clash 中切换为“全局”模式。\n规则模式可能让部分设备流量直连，导致加速失败。"),
        UiText.Get("Clash 共存"), MessageBoxButton.OK, MessageBoxImage.Information);

    private void Node_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (synchronizingNodes || NodesList is null) return;
        if (devices.Count == 0 || activeDeviceIds.Contains(PrimaryDevice.Id)) return;
        synchronizingNodes = true;
        SyncNodeList();
        savedNode = NodeBox.SelectedItem as string ?? savedNode;
        if (activeSubscription is not null) activeSubscription.SelectedNode = savedNode;
        PrimaryDevice.SelectedNode = savedNode;
        synchronizingNodes = false;
        RefreshDeviceLatencies();
        DisposeCore(PrimaryDevice.Id);
        manualPlans.Remove(PrimaryDevice.Id);
        manualPlan = null;
        ClearDeviceRuntime(PrimaryDevice);
        SaveProfile();
        RefreshSelectedLatency(true);
    }

    private void SubscriptionNode_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (synchronizingNodes || NodeBox is null) return;
        NodeBox.SelectedItem = (NodesList.SelectedItem as NodeLatencyRow)?.Name;
    }

    private void QuickNode_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (synchronizingNodes || NodeBox is null) return;
        NodeBox.SelectedItem = PrimaryCard.QuickNodeBox.SelectedItem;
    }

    private void AddDevice_Click(object sender, RoutedEventArgs e)
    {
        if (busy || gatewaySession is not null || devices.Count >= 8) return;
        var next = 2;
        while (devices.Any(d => d.Name == $"设备 {next}" || d.Name == $"Device {next}")) next++;
        devices.Add(new DeviceProfile
        {
            Name = UiText.Get($"设备 {next}"),
            SelectedNode = PrimaryDevice.SelectedNode,
            IncludedInAcceleration = true,
            ParametersExpanded = true,
            State = "等待开始加速",
        });
        RequestDeviceLatency(devices[^1]);
        RefreshDeviceViews();
        SaveProfile();
    }

    private void DeleteDevice_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: DeviceProfile device } || busy || gatewaySession is not null) return;
        if (devices.Count == 1)
        {
            UiText.Set(StatusText, "至少保留一台设备");
            return;
        }
        devices.Remove(device);
        RefreshDeviceViews();
        SaveProfile();
    }

    private void DeviceName_LostFocus(object sender, RoutedEventArgs e)
    {
        if (restoringProfile || sender is not TextBox textBox) return;

        // Commit after IME composition finishes. Refreshing the cards while a
        // Chinese composition is active resets the text box and caret.
        textBox.GetBindingExpression(TextBox.TextProperty)?.UpdateSource();
        SaveProfile();
    }

    internal void DeviceNode_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (restoringProfile || synchronizingNodes || sender is not ComboBox { DataContext: DeviceProfile device } combo) return;
        if (activeDeviceIds.Contains(device.Id)) return;
        if (combo.SelectedItem is not string node) return;
        device.SelectedNode = node;
        DisposeCore(device.Id);
        manualPlans.Remove(device.Id);
        ClearDeviceRuntime(device);
        if (device == PrimaryDevice && NodeBox.SelectedItem as string != device.SelectedNode)
            NodeBox.SelectedItem = device.SelectedNode;
        RefreshDeviceLatencies();
        SaveProfile();
        RequestDeviceLatency(device);
    }

    private void SaveProfile()
    {
        if (!profileReady || smokeTest) return;
        try
        {
            ProfileStore.Save(new AppProfile
            {
                AdapterGUID = (AdapterBox.SelectedItem as AdapterInfo)?.GUID ?? preferredAdapter,
                Theme = ThemeManager.Current,
                Accent = ThemeManager.Accent,
                Language = UiText.Instance.Language,
                Proxy = ProxyBox.Text.Trim(), Independent = Independent,
                Subscriptions = subscriptions.ToList(), ActiveSubscriptionId = activeSubscription?.Id ?? "",
                Devices = devices.ToList(),
            });
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or System.Security.Cryptography.CryptographicException or InvalidOperationException)
        { Log("设置保存失败，本次已读取的订阅仍可使用；下次启动可能需要重新导入。"); }
    }

    private void Tray_Click(object sender, RoutedEventArgs e)
    {
        if (tray is null) return;
        Hide();
        tray.Notify(gatewaySession is null ? "软件已收起，加速未启动。" : "加速继续运行；可从托盘停止或打开窗口。");
    }

    internal void RestoreFromTray()
    {
        Show();
        if (WindowState == WindowState.Minimized) WindowState = WindowState.Normal;
        Activate();
    }

    private void PowerMode_Changed(object sender, PowerModeChangedEventArgs e)
    {
        if (e.Mode != PowerModes.Suspend || gatewaySession is null) return;
        try { gatewaySession.RequestStop(); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { }
        Dispatcher.BeginInvoke(() =>
        {
            stopping = true;
            MarkActiveDevicesStopping();
            Log("电脑进入休眠，已请求停止网关；唤醒后需恢复设备原网络设置。");
            UpdateControls();
        });
    }

    private void Export_Click(object sender, RoutedEventArgs e)
    {
        if (busy || gatewaySession is not null || SessionCoolingDown) return;
        var dialog = new SaveFileDialog
        {
            Filter = UiText.Get("诊断包 (*.zip)|*.zip"), FileName = $"Accelerator-diagnostics-{DateTime.Now:yyyyMMdd-HHmmss}.zip",
        };
        if (dialog.ShowDialog(this) != true) return;
        try
        {
            DiagnosticsExport.Write(dialog.FileName, lastSessionDirectory, snapshot);
            Log("诊断包已导出，包含设备地址和运行计数，不包含订阅、节点凭据或原始数据包。");
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
        {
            MessageBox.Show(this, UiText.Get("诊断导出失败，请选择可写入的位置。"), UiText.Get("导出诊断"), MessageBoxButton.OK, MessageBoxImage.Information);
        }
    }

    private async void Import_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new OpenFileDialog { Filter = UiText.Get("Clash 配置 (*.yaml;*.yml)|*.yaml;*.yml|所有文件|*.*"), CheckFileExists = true };
        if (dialog.ShowDialog(this) != true) return;
        await WorkAsync("解析本地订阅…", async () =>
        {
            if (new FileInfo(dialog.FileName).Length > MaxSubscriptionBytes)
                throw new InvalidOperationException("订阅超过 8 MiB。");
            await ParseSubscriptionAsync(await File.ReadAllBytesAsync(dialog.FileName),
                new SubscriptionProfile { Name = Path.GetFileName(dialog.FileName) });
            RequestImportedLatency();
        });
    }

    private async void Fetch_Click(object sender, RoutedEventArgs e) => await WorkAsync("读取 HTTPS 订阅…", async () =>
    {
        var url = SubscriptionUrlBox.Text.Trim();
        var existing = subscriptions.FirstOrDefault(s => s.Url == url);
        await FetchSubscriptionAsync(url, existing);
        SubscriptionUrlBox.Clear();
    });

    private async Task FetchSubscriptionAsync(string url, SubscriptionProfile? existing = null)
    {
        var downloaded = await SubscriptionDownloader.FetchAsync(url, ProxyBox.Text.Trim());
        var item = downloaded.Profile;
        if (existing is not null)
        {
            item.Id = existing.Id;
            item.Name = existing.Name;
            item.SelectedNode = existing.SelectedNode;
        }
        // Parse first; an unsuccessful update must leave the cached record usable.
        var activate = existing is null || existing == activeSubscription;
        await ParseSubscriptionAsync(downloaded.Content, item, activate);
        if (activate) RequestImportedLatency();
        Log("订阅读取成功 · " + downloaded.Route);
    }

    private void RefreshSubscriptionCards()
    {
        synchronizingSubscriptions = true;
        SubscriptionsList.Items.Refresh();
        SubscriptionsList.SelectedItem = activeSubscription;
        synchronizingSubscriptions = false;
    }

    private async void Subscription_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (synchronizingSubscriptions || SubscriptionsList.SelectedItem is not SubscriptionProfile item || item == activeSubscription) return;
        if (busy || gatewaySession is not null) { RefreshSubscriptionCards(); return; }
        await WorkAsync("切换订阅…", async () =>
        {
            try { await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(item.Content), item); }
            finally { RefreshSubscriptionCards(); }
        });
    }

    private async void UpdateSubscription_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: SubscriptionProfile item }) return;
        e.Handled = true;
        if (item.Url.Length == 0)
        {
            UiText.Set(StatusText, "本地订阅请通过“导入文件”添加新版本");
            return;
        }
        await WorkAsync("更新订阅…", () => FetchSubscriptionAsync(item.Url, item));
    }

    private async void DeleteSubscription_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: SubscriptionProfile item }) return;
        e.Handled = true;
        await WorkAsync("删除订阅…", () => DeleteSubscriptionAsync(item));
    }

    private async Task DeleteSubscriptionAsync(SubscriptionProfile item)
    {
        if (!subscriptions.Contains(item)) return;
        if (item == activeSubscription)
        {
            var next = subscriptions.FirstOrDefault(s => s != item);
            if (next is not null) await ParseSubscriptionAsync(Encoding.UTF8.GetBytes(next.Content), next);
            else
            {
                DisposeCore();
                activeSubscription = null;
                subscriptionContent = savedNode = "";
                synchronizingNodes = true;
                NodeBox.ItemsSource = NodesList.ItemsSource = null;
                NodeOptions.Clear();
                ResetLatencyRows([]);
                synchronizingNodes = false;
                UiText.Set(SubscriptionResult, "尚未导入订阅");
                InvalidateGateway();
                UpdateOutletStatus();
            }
        }
        subscriptions.Remove(item);
        RefreshSubscriptionCards();
        SaveProfile();
    }

    private void RenameSubscription_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: SubscriptionProfile item } || busy || gatewaySession is not null) return;
        e.Handled = true;
        var input = new TextBox { Text = item.Name, Margin = new Thickness(0, 0, 0, 16), MaxLength = 100 };
        var save = new Button { Content = UiText.Get("保存"), IsDefault = true, HorizontalAlignment = HorizontalAlignment.Right, Margin = new Thickness(0) };
        var panel = new StackPanel { Margin = new Thickness(20) };
        panel.Children.Add(new TextBlock { Text = UiText.Get("订阅名称"), Margin = new Thickness(0, 0, 0, 8) });
        panel.Children.Add(input);
        panel.Children.Add(save);
        var dialog = new Window
        {
            Title = UiText.Get("重命名订阅"), Owner = this, Width = 400, SizeToContent = SizeToContent.Height,
            ResizeMode = ResizeMode.NoResize, WindowStartupLocation = WindowStartupLocation.CenterOwner, Content = panel,
        };
        save.Click += (_, _) => { if (!string.IsNullOrWhiteSpace(input.Text)) dialog.DialogResult = true; };
        dialog.Loaded += (_, _) => { input.Focus(); input.SelectAll(); };
        if (dialog.ShowDialog() != true) return;
        item.Name = input.Text.Trim();
        RefreshSubscriptionCards();
        if (activeSubscription == item) UiText.Set(SubscriptionResult, item.Name + $" · {NodesList.Items.Count} 个节点");
        SaveProfile();
    }

}

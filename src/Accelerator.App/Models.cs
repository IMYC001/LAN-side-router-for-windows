using System.ComponentModel;
using System.Runtime.CompilerServices;
using System.Text.Json.Serialization;

namespace Accelerator.App;

internal sealed class DeviceProfile : INotifyPropertyChanged
{
    private string name = UiText.Get("设备 1");
    private string selectedNode = "";
    private bool includedInAcceleration = true;
    private bool parametersExpanded = true;

    public string Id { get; set; } = Guid.NewGuid().ToString("N");
    public string Name
    {
        get => name;
        set => Set(ref name, string.IsNullOrWhiteSpace(value) ? "未命名设备" : value.Trim());
    }
    public string SelectedNode
    {
        get => selectedNode;
        set => Set(ref selectedNode, value ?? "");
    }
    public bool IncludedInAcceleration
    {
        get => includedInAcceleration;
        set => Set(ref includedInAcceleration, value);
    }
    public bool ParametersExpanded
    {
        get => parametersExpanded;
        set => Set(ref parametersExpanded, value);
    }

    [JsonIgnore] public string State { get; set; } = "等待开始加速";
    [JsonIgnore] public string Identity { get; set; } = "尚无设备接入";
    [JsonIgnore] public string DeviceIP { get; set; } = "";
    [JsonIgnore] public string Netmask { get; set; } = "";
    [JsonIgnore] public string Gateway { get; set; } = "";
    [JsonIgnore] public string DNS { get; set; } = "";
    [JsonIgnore] public string DownloadSpeed { get; set; } = "0.0";
    [JsonIgnore] public string UploadSpeed { get; set; } = "0.0";
    [JsonIgnore] public string DownloadTotal { get; set; } = "累计 0 B";
    [JsonIgnore] public string UploadTotal { get; set; } = "累计 0 B";
    [JsonIgnore] public string NodeLatency { get; set; } = "--";
    [JsonIgnore] public string OutletLabel { get; set; } = "独立订阅";
    [JsonIgnore] public string ActionText { get; set; } = "开始加速";
    [JsonIgnore] public string ActionIcon { get; set; } = "\uE768";
    [JsonIgnore] public bool CanToggleAcceleration { get; set; } = true;
    [JsonIgnore] public bool UsesIndependentOutlet { get; set; } = true;
    [JsonIgnore] public string ParameterToggleIcon { get; set; } = "\uE70E";
    [JsonIgnore] public bool ActionRunning { get; set; }
    [JsonIgnore] public bool ActionEnding { get; set; }
    [JsonIgnore] public bool ActionPreparing { get; set; }
    [JsonIgnore] public bool ParametersReady { get; set; }
    [JsonIgnore] public bool CanSelectNode { get; set; } = true;
    [JsonIgnore] public string ParameterNote { get; set; } = "开始加速后生成网络参数。";
    [JsonIgnore] public string PreparationIssue { get; set; } = "";

    public event PropertyChangedEventHandler? PropertyChanged;

    private void Set<T>(ref T field, T value, [CallerMemberName] string? property = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value)) return;
        field = value;
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(property));
    }

    internal void NotifyRuntime() => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(null));
}

internal sealed class AdapterInfo
{
    public string Name { get; set; } = "";
    public string Description { get; set; } = "";
    public int Index { get; set; }
    public string GUID { get; set; } = "";
    public string MAC { get; set; } = "";
    public bool Physical { get; set; }
    public string Status { get; set; } = "";
    public string IPv4 { get; set; } = "";
    public int Prefix { get; set; }
    public string Gateway { get; set; } = "";
    public bool Forwarding { get; set; }
    public string Display => $"{Name} · {IPv4}/{Prefix} · {Description}";
}

internal sealed class ManualGatewayPlan
{
    public AdapterInfo Adapter { get; set; } = new();
    public string DeviceMAC { get; set; } = "";
    public string OriginalIP { get; set; } = "";
    public string DeviceIP { get; set; } = "";
    public string Netmask { get; set; } = "";
    public string Gateway { get; set; } = "";
    public string DNS { get; set; } = "";
    public string Proxy { get; set; } = "";
    public string Description { get; set; } = "";
    public bool Prepared { get; set; }
    public List<PlanCheck> Checks { get; set; } = [];
}

internal sealed class GatewayStatus
{
    public string Phase { get; set; } = "";
    public string Message { get; set; } = "";
    public string DeviceIP { get; set; } = "";
    public string DeviceMAC { get; set; } = "";
    public ulong Received { get; set; }
    public ulong Sent { get; set; }
    public ulong ARP { get; set; }
    public ulong IPv4 { get; set; }
    public ulong Forwarded { get; set; }
    public int SecondsLeft { get; set; }
    public bool PassivePassed { get; set; }
    public string IdentityMethod { get; set; } = "";
    public bool Stopped { get; set; }
    public bool Continuous { get; set; }
    public int Elapsed { get; set; }
    public GatewayStats Stats { get; set; } = new();
    public CaptureStats Capture { get; set; } = new();
    public TransmitStats Transmit { get; set; } = new();
    public int PendingFrames { get; set; }
    public bool TraceSaved { get; set; }
    public double UploadBytesPerSecond { get; set; }
    public double DownloadBytesPerSecond { get; set; }
    public List<GatewayDeviceStatus> Devices { get; set; } = [];
    public long ControlRevision { get; set; }
    public string ControlError { get; set; } = "";
}

internal sealed class GatewayDeviceStatus
{
    public string Id { get; set; } = "";
    public string Phase { get; set; } = "";
    public string Message { get; set; } = "";
    public string DeviceIP { get; set; } = "";
    public string DeviceMAC { get; set; } = "";
    public ulong Received { get; set; }
    public ulong Sent { get; set; }
    public ulong ARP { get; set; }
    public ulong IPv4 { get; set; }
    public ulong Forwarded { get; set; }
    public GatewayStats Stats { get; set; } = new();
    public double UploadBytesPerSecond { get; set; }
    public double DownloadBytesPerSecond { get; set; }
}

internal sealed class TransmitStats
{
    public ulong Batches { get; set; }
    public ulong Frames { get; set; }
    public double TotalMillis { get; set; }
    public double LongestMillis { get; set; }
}

internal sealed class CaptureStats
{
    public bool Available { get; set; }
    public uint Received { get; set; }
    public uint Dropped { get; set; }
}

internal sealed class GatewayStats
{
    public ulong TCPStarted { get; set; }
    public ulong UDPStarted { get; set; }
    public ulong UDPAssociations { get; set; }
    public ulong Failed { get; set; }
    public ulong Uploaded { get; set; }
    public ulong Downloaded { get; set; }
    public ulong TCPRetransmits { get; set; }
    public ulong TCPTimeouts { get; set; }
    public int PendingPackets { get; set; }
}

internal sealed class NeighborInfo
{
    public string IP { get; set; } = "";
    public string MAC { get; set; } = "";
    public int Index { get; set; }
    public string State { get; set; } = "";
}

internal sealed class NetworkSnapshot
{
    public List<AdapterInfo> Adapters { get; set; } = [];
    public List<NeighborInfo> Neighbors { get; set; } = [];
    public List<ListenerInfo> Listeners { get; set; } = [];
    public bool Npcap { get; set; }
    public bool WinpkFilter { get; set; }
    public bool Admin { get; set; }
}

internal sealed class ListenerInfo
{
    public string Address { get; set; } = "";
    public int Port { get; set; }
}

internal sealed class PlanCheck
{
    public string Name { get; set; } = "";
    public string Status { get; set; } = "";
    public string Detail { get; set; } = "";
    public string Label => Status switch { "pass" => "通过", "block" => "未满足", _ => "待实测" };
}

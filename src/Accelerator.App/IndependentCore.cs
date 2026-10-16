using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text.Json;

namespace Accelerator.App;

internal sealed class IndependentCore : IDisposable
{
    private readonly Process process;
    private readonly ProcessJob job;
    private readonly string directory;
    private readonly HttpClient client;
    private bool disposed;
    public string Proxy { get; }
    public string Node { get; }
    public string AdapterGUID { get; }
    private readonly string adapterName;
    public bool Alive => !disposed && !process.HasExited;
    internal int ProcessId => process.Id;

    private IndependentCore(Process process, ProcessJob job, string directory, HttpClient client,
        string proxy, string node, string adapterGUID, string adapterName)
    {
        this.process = process;
        this.job = job;
        this.directory = directory;
        this.client = client;
        Proxy = proxy;
        Node = node;
        AdapterGUID = adapterGUID;
        this.adapterName = adapterName;
    }

    private static TcpListener ReservePort()
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Server.ExclusiveAddressUse = true;
        listener.Start();
        return listener;
    }

    public static async Task<IndependentCore> StartAsync(string subscription, string node, AdapterInfo adapter,
        CancellationToken cancellation = default)
    {
        cancellation.ThrowIfCancellationRequested();
        var executable = Path.Combine(AppContext.BaseDirectory, "mihomo.exe");
        if (!File.Exists(executable))
            throw new InvalidOperationException("独立节点核心缺失；请从完整发布目录运行。");
        if (!adapter.Physical || adapter.Status != "Up" || string.IsNullOrEmpty(adapter.Name))
            throw new InvalidOperationException("独立节点需要已连接的物理网卡。");
        var directory = Path.Combine(ProfileStore.Root, "runtime", Guid.NewGuid().ToString("N"));
        ProfileStore.MakePrivate(directory);
        using var socks = ReservePort();
        using var controller = ReservePort();
        var socksPort = ((IPEndPoint)socks.LocalEndpoint).Port;
        var controllerPort = ((IPEndPoint)controller.LocalEndpoint).Port;
        var secret = Convert.ToHexString(RandomNumberGenerator.GetBytes(32));
        Process? process = null;
        ProcessJob? job = null;
        HttpClient? client = null;
        try
        {
            var data = await EngineClient.CallAsync(new
            {
                command = "prepare-outbound", subscription,
                outbound = new { node, @interface = adapter.Name, socksPort, controllerPort, secret },
            }, cancellation);
            var configPath = Path.Combine(directory, "config.yaml");
            await File.WriteAllTextAsync(configPath, data.GetProperty("config").GetString(), cancellation);
            var start = new ProcessStartInfo(executable)
            {
                UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = directory,
                RedirectStandardOutput = true, RedirectStandardError = true,
            };
            start.ArgumentList.Add("-d");
            start.ArgumentList.Add(directory);
            start.ArgumentList.Add("-f");
            start.ArgumentList.Add(configPath);
            job = new ProcessJob();
            socks.Stop();
            controller.Stop();
            process = Process.Start(start) ?? throw new InvalidOperationException("无法启动独立节点核心。");
            job.Add(process);
            // Core diagnostics may include endpoints or credentials. Drain and discard.
            _ = DrainAsync(process.StandardOutput);
            _ = DrainAsync(process.StandardError);
            client = new HttpClient(new HttpClientHandler { UseProxy = false })
            {
                BaseAddress = new Uri($"http://127.0.0.1:{controllerPort}/"),
                Timeout = TimeSpan.FromSeconds(8),
            };
            client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", secret);
            var ready = false;
            var deadline = Stopwatch.StartNew();
            while (deadline.Elapsed < TimeSpan.FromSeconds(12) && !process.HasExited)
            {
                cancellation.ThrowIfCancellationRequested();
                try
                {
                    using var probe = CancellationTokenSource.CreateLinkedTokenSource(cancellation);
                    probe.CancelAfter(TimeSpan.FromSeconds(2));
                    using var response = await client.GetAsync("version", probe.Token);
                    ready = response.IsSuccessStatusCode;
                    if (ready) break;
                }
                catch (HttpRequestException) { }
                catch (TaskCanceledException) { }
                await Task.Delay(150, cancellation);
            }
            if (!ready) throw new InvalidOperationException("独立节点核心启动失败；节点参数、协议或端口可能不受支持。");
            return new IndependentCore(process, job, directory, client,
                $"127.0.0.1:{socksPort}", node, adapter.GUID, adapter.Name);
        }
        catch
        {
            job?.Dispose();
            if (process is not null)
            {
                if (!process.HasExited) process.Kill(entireProcessTree: true);
                process.WaitForExit(3000);
                process.Dispose();
            }
            client?.Dispose();
            DeleteRuntime(directory);
            throw;
        }
    }

    private static async Task DrainAsync(StreamReader reader)
    {
        try
        {
            var buffer = new char[4096];
            while (await reader.ReadAsync(buffer) > 0) { }
        }
        catch (Exception ex) when (ex is IOException or ObjectDisposedException) { }
    }

    public async Task VerifyIsolationAsync(CancellationToken cancellation = default)
    {
        using var response = await client.GetAsync("configs", cancellation);
        response.EnsureSuccessStatusCode();
        using var data = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        var root = data.RootElement;
        if (root.GetProperty("allow-lan").GetBoolean() ||
            root.GetProperty("bind-address").GetString() != "127.0.0.1" ||
            root.GetProperty("tun").GetProperty("enable").GetBoolean() ||
            root.GetProperty("socks-port").GetInt32() != int.Parse(Proxy.Split(':')[1]) ||
            root.GetProperty("interface-name").GetString() != adapterName)
            throw new InvalidOperationException("独立节点的回环监听或 TUN 隔离检查未通过。");
    }

    public Task<int> MeasureDelayAsync(CancellationToken cancellation) => ReadDelayAsync(client, cancellation);

    internal static async Task<int> ReadDelayAsync(HttpClient controller, CancellationToken cancellation)
    {
        using var response = await controller.GetAsync(
            "proxies/PS5-EXIT/delay?timeout=5000&expected=204&url=" +
            Uri.EscapeDataString("https://www.gstatic.com/generate_204"), cancellation);
        if (response.StatusCode == HttpStatusCode.GatewayTimeout) throw new TimeoutException();
        response.EnsureSuccessStatusCode();
        using var data = JsonDocument.Parse(await response.Content.ReadAsStringAsync(cancellation));
        if (!data.RootElement.TryGetProperty("delay", out var value) || !value.TryGetInt32(out int delay) ||
            delay <= 0 || delay > 65535)
            throw new JsonException("Invalid delay response.");
        return delay;
    }

    public void Dispose()
    {
        if (disposed) return;
        disposed = true;
        job.Dispose();
        if (!process.HasExited) process.WaitForExit(3000);
        var exited = process.HasExited;
        client.Dispose();
        process.Dispose();
        if (exited) DeleteRuntime(directory);
    }

    private static void DeleteRuntime(string directory)
    {
        var root = Path.GetFullPath(Path.Combine(ProfileStore.Root, "runtime")) + Path.DirectorySeparatorChar;
        var target = Path.GetFullPath(directory);
        if (!target.StartsWith(root, StringComparison.OrdinalIgnoreCase) ||
            !Guid.TryParseExact(Path.GetFileName(target), "N", out _))
            return;
        for (var attempt = 0; attempt < 6; attempt++)
        {
            try
            {
                if (!Directory.Exists(target)) return;
                if ((File.GetAttributes(root.TrimEnd(Path.DirectorySeparatorChar)) & FileAttributes.ReparsePoint) != 0 ||
                    (File.GetAttributes(target) & FileAttributes.ReparsePoint) != 0) return;
                Directory.Delete(target, true);
                return;
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                // Windows can retain a short-lived handle after core termination.
                if (attempt < 5) Thread.Sleep(100 * (attempt + 1));
            }
        }
    }

    public static void CleanupStaleRuntime()
    {
        // The app mutex prevents another instance of this product from starting.
        // Leave all files alone if another standalone Mihomo is running.
        var processes = Process.GetProcessesByName("mihomo");
        foreach (var process in processes) process.Dispose();
        if (processes.Length != 0) return;
        var root = Path.Combine(ProfileStore.Root, "runtime");
        if (!Directory.Exists(root) || (File.GetAttributes(root) & FileAttributes.ReparsePoint) != 0) return;
        foreach (var directory in Directory.EnumerateDirectories(root))
            DeleteRuntime(directory);
    }

    internal static async Task VerifyOfflineAsync()
    {
        const string subscription = "proxies: [{name: offline, type: anytls, server: 203.0.113.1, port: 443, password: OFFLINE-SECRET, udp: true}]\n";
        var core = await StartAsync(subscription, "offline", new AdapterInfo
        {
            Name = "Loopback Pseudo-Interface 1", GUID = "{offline}", Physical = true, Status = "Up",
        });
        var directory = core.directory;
        var pid = core.ProcessId;
        try { await core.VerifyIsolationAsync(); }
        finally { core.Dispose(); }
        if (Directory.Exists(directory))
            throw new InvalidOperationException("独立核心临时配置未清理。");
        try
        {
            using var process = Process.GetProcessById(pid);
            if (!process.HasExited) throw new InvalidOperationException("独立核心未随任务关闭。");
        }
        catch (ArgumentException) { }
    }
}

using System.Diagnostics;
using System.IO;
using System.Text.Json;

namespace Accelerator.App;

internal sealed class GatewaySession : IDisposable
{
    private readonly Process process;
    private readonly string directory;
    private bool disposed;
    private long lastPoll = Environment.TickCount64;
    private readonly long startedAt = Environment.TickCount64;
    private long lastStatusAt = Environment.TickCount64;
    private bool receivedStatus;
    private long controlRevision;
    private GatewayStatus? latestStatus;
    public string DirectoryPath => directory;
    public GatewayStatus? LatestStatus => latestStatus;

    private GatewaySession(Process process, string directory)
    {
        this.process = process;
        this.directory = directory;
    }

    public static GatewaySession Start(object plan, string mode, object expected, bool continuous = false)
    {
        return StartInternal(plan, mode, expected, null, continuous);
    }

    public static GatewaySession StartMany(object primaryPlan, string mode, object primaryExpected,
        IReadOnlyList<object> devices, bool continuous = false)
    {
        if (devices.Count is < 1 or > 8)
            throw new InvalidOperationException("设备会话需要 1 到 8 台设备。");
        return StartInternal(primaryPlan, mode, primaryExpected, devices, continuous);
    }

    private static GatewaySession StartInternal(object plan, string mode, object expected,
        IReadOnlyList<object>? devices, bool continuous)
    {
        var directory = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "PS5Accelerator", "sessions", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        var request = Path.Combine(directory, "request.json");
        File.WriteAllText(request, JsonSerializer.Serialize(new
        {
            mode,
            plan,
            expected,
            devices,
            confirmed = true,
            continuous = mode == "gateway" && continuous,
            seconds = mode == "passive" ? 20 : continuous ? 0 : 180,
        }));
        File.WriteAllText(Path.Combine(directory, "lease"), "");
        // No credentials or subscription content are passed to the elevated helper.
        var process = Process.Start(new ProcessStartInfo(
            Path.Combine(AppContext.BaseDirectory, "accelerator-engine.exe"),
            $"worker \"{request}\"")
        {
            UseShellExecute = true,
            Verb = "runas",
            WindowStyle = ProcessWindowStyle.Hidden,
        }) ?? throw new InvalidOperationException("未能启动管理员辅助进程。");
        var session = new GatewaySession(process, directory);
        try
        {
            File.SetLastWriteTimeUtc(Path.Combine(directory, "lease"), DateTime.UtcNow);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Keep the process handle so polling reports the failure and the UI
            // waits for lease expiry before permitting another attempt.
            try { session.RequestStop(); }
            catch (Exception stopError) when (stopError is IOException or UnauthorizedAccessException) { }
        }
        return session;
    }

    public GatewayStatus? Poll()
    {
        if (disposed) return null;
        var now = Environment.TickCount64;
        if (now - lastPoll > 8000)
        {
            RequestStop();
            throw new IOException("软件长时间暂停，已请求停止网关。");
        }
        lastPoll = now;
        if ((!receivedStatus && now - startedAt > 45000) ||
            (receivedStatus && now - lastStatusAt > 10000))
        {
            RequestStop();
            throw new IOException("辅助进程没有返回有效的新状态，已请求停止网关。");
        }
        File.SetLastWriteTimeUtc(Path.Combine(directory, "lease"), DateTime.UtcNow);
        var path = Path.Combine(directory, "status.json");
        try
        {
            var (status, writtenAt) = ReadStatusSnapshot(path);
            if (status is not null && DateTime.UtcNow - writtenAt <= TimeSpan.FromSeconds(8) &&
                writtenAt - DateTime.UtcNow <= TimeSpan.FromSeconds(5))
            {
                lastStatusAt = Environment.TickCount64;
                receivedStatus = true;
            }
            if (status is not null && process.HasExited && !status.Stopped)
            {
                status.Stopped = true;
                status.Phase = "failed";
                status.Message = "辅助进程异常退出；若已修改设备，请恢复原来的 IP 和 DNS 设置。";
            }
            latestStatus = status;
            return status;
        }
        catch (FileNotFoundException)
        {
            return latestStatus = process.HasExited
                ? new GatewayStatus { Phase = "failed", Message = "辅助进程已退出，没有可用结果。", Stopped = true }
                : null;
        }
        catch (IOException)
        {
            return null;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    public async Task UpdateDevicesAsync(IReadOnlyList<object> devices)
    {
        if (disposed || devices.Count is < 1 or > 8)
            throw new InvalidOperationException("加速会话不可用。");
        var revision = ++controlRevision;
        var temp = Path.Combine(directory, "control-" + revision + ".tmp");
        try
        {
            await File.WriteAllTextAsync(temp, JsonSerializer.Serialize(new { revision, devices }));
            File.Move(temp, Path.Combine(directory, "control.json"), overwrite: true);
        }
        finally { if (File.Exists(temp)) File.Delete(temp); }
        var deadline = Environment.TickCount64 + 40000;
        while (!disposed && Environment.TickCount64 < deadline)
        {
            // The dispatcher timer continues polling and renewing the lease.
            if (latestStatus is { } status && status.ControlRevision >= revision)
            {
                if (status.ControlError.Length > 0) throw new InvalidOperationException(status.ControlError);
                if (status.Stopped) throw new IOException("加速会话已停止。");
                return;
            }
            if (latestStatus?.Stopped == true) throw new IOException("加速会话已停止。");
            await Task.Delay(100);
        }
        RequestStop();
        throw new IOException("设备变更确认超时，已请求安全停止；请恢复设备原网络设置。");
    }

    private static (GatewayStatus? Status, DateTime WrittenAt) ReadStatusSnapshot(string path)
    {
        using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
        var status = JsonSerializer.Deserialize<GatewayStatus>(stream, EngineClient.JsonOptions);
        // Read metadata from the same delete-sharing handle as the JSON.
        // Opening the path again can block replacement or refer to a newer file.
        return (status, File.GetLastWriteTimeUtc(stream.SafeFileHandle));
    }

    internal static void VerifyOffline()
    {
        var directory = Path.Combine(Path.GetTempPath(), "Accelerator-status-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        try
        {
            var path = Path.Combine(directory, "status.json");
            var temp = Path.Combine(directory, "status.tmp");
            File.WriteAllText(path, "{\"phase\":\"connected\"}");
            var past = DateTime.UtcNow.AddMinutes(-1);
            File.SetLastWriteTimeUtc(path, past);
            using (var oldSnapshot = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete))
            {
                File.WriteAllText(temp, "{\"phase\":\"stopped\",\"stopped\":true}");
                File.Replace(temp, path, null);
                if (JsonSerializer.Deserialize<GatewayStatus>(oldSnapshot, EngineClient.JsonOptions)?.Phase != "connected" ||
                    Math.Abs((File.GetLastWriteTimeUtc(oldSnapshot.SafeFileHandle) - past).TotalSeconds) > 1)
                    throw new InvalidOperationException("状态内容与时间戳没有来自同一个快照。");
            }
            var (state, writtenAt) = ReadStatusSnapshot(path);
            if (state is not { Stopped: true, Phase: "stopped" } || DateTime.UtcNow - writtenAt > TimeSpan.FromSeconds(8))
                throw new InvalidOperationException("原子替换后的停止状态读取失败。");
        }
        finally { Directory.Delete(directory, true); }
    }

    internal static async Task VerifyControlOfflineAsync()
    {
        var directory = Path.Combine(Path.GetTempPath(), "Accelerator-control-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        File.WriteAllText(Path.Combine(directory, "lease"), "");
        try
        {
            // A handle to this process is only observed/disposed, never terminated.
            using var session = new GatewaySession(Process.GetCurrentProcess(), directory);
            for (int revision = 1; revision <= 3; revision++)
            {
                var pending = session.UpdateDevicesAsync([new { id = "retained" }, new { id = "added-" + revision }]);
                var path = Path.Combine(directory, "control.json");
                var deadline = Environment.TickCount64 + 3000;
                while (!File.Exists(path) || JsonDocument.Parse(await File.ReadAllTextAsync(path))
                    .RootElement.GetProperty("revision").GetInt64() != revision)
                {
                    if (Environment.TickCount64 > deadline) throw new IOException("设备变更请求没有写入。");
                    await Task.Delay(10);
                }
                using (var control = JsonDocument.Parse(await File.ReadAllTextAsync(path)))
                {
                    if (control.RootElement.GetProperty("devices")[0].GetProperty("id").GetString() != "retained")
                        throw new InvalidOperationException("设备变更丢失保留设备。");
                }
                if (pending.IsCompleted) throw new InvalidOperationException("未确认的设备变更提前成功。");
                var state = new GatewayStatus { Phase = "connected", ControlRevision = revision,
                    ControlError = revision == 2 ? "offline rejection" : "" };
                await File.WriteAllTextAsync(Path.Combine(directory, "status.json"),
                    JsonSerializer.Serialize(state, EngineClient.JsonOptions));
                session.Poll();
                try
                {
                    await pending;
                    if (revision == 2) throw new IOException("拒绝变更未报告错误。");
                }
                catch (InvalidOperationException ex) when (revision == 2 && ex.Message == "offline rejection") { }
                if (File.Exists(Path.Combine(directory, "stop")))
                    throw new InvalidOperationException("正常变更或单台准备失败停止了整个会话。");
            }
        }
        finally { Directory.Delete(directory, true); }
    }

    public void RequestStop()
    {
        if (!disposed)
            File.WriteAllText(Path.Combine(directory, "stop"), "");
    }

    public void Dispose()
    {
        if (disposed) return;
        try { RequestStop(); }
        finally
        {
            disposed = true;
            process.Dispose();
        }
        // The helper observes the stop file or lease expiration and closes its
        // own handles. The GUI never kills a privileged process mid-write.
    }
}

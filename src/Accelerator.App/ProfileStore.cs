using System.IO;
using System.Diagnostics;
using System.Security.AccessControl;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Text.Json;

namespace Accelerator.App;

internal sealed class AppProfile
{
    public int Schema { get; set; } = 4;
    public string Theme { get; set; } = "Dark";
    public string Accent { get; set; } = "Green";
    public string Language { get; set; } = "zh-CN";
    public string AdapterGUID { get; set; } = "";
    public string Proxy { get; set; } = "127.0.0.1:7890";
    public bool Independent { get; set; } = true;
    public string SelectedNode { get; set; } = "";
    public string Subscription { get; set; } = "";
    public string SubscriptionUrl { get; set; } = "";
    public List<SubscriptionProfile> Subscriptions { get; set; } = [];
    public string ActiveSubscriptionId { get; set; } = "";
    public List<DeviceProfile> Devices { get; set; } = [new()];
}

internal static class ProfileStore
{
    public static string Root
    {
        get
        {
            var args = Environment.GetCommandLineArgs();
            var check = Array.IndexOf(args, "--profile-restart-check");
            if (check >= 0)
            {
                if (check + 1 >= args.Length)
                    throw new InvalidOperationException("缺少重启验证目录。");
                var root = Path.GetFullPath(args[check + 1]);
                var temporary = Path.TrimEndingDirectorySeparator(Path.GetFullPath(Path.GetTempPath())) + Path.DirectorySeparatorChar;
                if (!root.StartsWith(temporary, StringComparison.OrdinalIgnoreCase) ||
                    !Path.GetFileName(root).StartsWith("PS5-restart-test-", StringComparison.Ordinal))
                    throw new InvalidOperationException("无效重启验证目录。");
                return root;
            }
            return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "PS5Accelerator");
        }
    }
    internal const string RestartSubscription =
        "proxies: [{name: Restart First, type: ss, server: 203.0.113.1, port: 443, cipher: aes-128-gcm, password: OFFLINE, udp: true}," +
        "{name: Restart Second, type: ss, server: 203.0.113.2, port: 443, cipher: aes-128-gcm, password: OFFLINE, udp: true}]\n";
    private static string Pathname => Path.Combine(Root, "profile.protected");
    private static readonly byte[] Entropy = "PS5Accelerator.Profile.v1"u8.ToArray();

    internal static void MakePrivate(string path)
    {
        Directory.CreateDirectory(path);
        var identity = WindowsIdentity.GetCurrent().User
            ?? throw new InvalidOperationException("无法确认当前 Windows 用户。");
        var security = new DirectorySecurity();
        security.SetOwner(identity);
        security.SetAccessRuleProtection(true, false);
        foreach (var sid in new[] { identity, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null) })
            security.AddAccessRule(new FileSystemAccessRule(sid, FileSystemRights.FullControl,
                InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit,
                PropagationFlags.None, AccessControlType.Allow));
        new DirectoryInfo(path).SetAccessControl(security);
    }

    public static AppProfile Load() => LoadRecoverable(Pathname);

    private static AppProfile LoadRecoverable(string path)
    {
        if (!File.Exists(path) && File.Exists(path + ".bak")) return Load(path + ".bak");
        try { return Load(path); }
        catch (Exception ex) when (ex is IOException or CryptographicException or JsonException or InvalidOperationException)
        {
            if (!File.Exists(path + ".bak")) throw;
            return Load(path + ".bak");
        }
    }

    private static AppProfile Load(string path)
    {
        if (!File.Exists(path)) return new();
        if (new FileInfo(path).Length > 32 * 1024 * 1024)
            throw new InvalidOperationException("保存的设置文件过大。");
        var data = ProtectedData.Unprotect(File.ReadAllBytes(path), Entropy, DataProtectionScope.CurrentUser);
        try
        {
            var profile = JsonSerializer.Deserialize<AppProfile>(data)
                ?? throw new InvalidOperationException("保存的设置无效。");
            Validate(profile);
            if (profile.Schema == 1) profile.Independent = true;
            if (profile.Schema < 3 && profile.Subscription.Length > 0)
            {
                var item = new SubscriptionProfile
                {
                    Content = profile.Subscription, Url = profile.SubscriptionUrl, SelectedNode = profile.SelectedNode,
                    Name = Uri.TryCreate(profile.SubscriptionUrl, UriKind.Absolute, out var uri) ? uri.Host : "Clash YAML",
                    UpdatedAt = default,
                };
                profile.Subscriptions = [item];
                profile.ActiveSubscriptionId = item.Id;
            }
            if (profile.Schema < 4)
            {
                var first = new DeviceProfile { Name = "设备 1", SelectedNode = profile.SelectedNode };
                profile.Devices = [first];
            }
            profile.Schema = 4;
            return profile;
        }
        finally { CryptographicOperations.ZeroMemory(data); }
    }

    private static void Validate(AppProfile profile)
    {
        if (profile.Schema is not (1 or 2 or 3 or 4) || profile.Subscription is null ||
            Encoding.UTF8.GetByteCount(profile.Subscription) > 8 * 1024 * 1024 ||
            profile.AdapterGUID is null || profile.Proxy is null ||
            profile.SelectedNode is null || profile.SubscriptionUrl is null ||
            profile.Subscriptions is null || profile.Subscriptions.Count > 32 ||
            profile.Subscriptions.Any(s => s is null || string.IsNullOrEmpty(s.Id) || s.Content is null ||
                Encoding.UTF8.GetByteCount(s.Content) > 8 * 1024 * 1024 || s.Name is null ||
                s.Url is null || s.SelectedNode is null) ||
            profile.Subscriptions.Select(s => s.Id).Distinct().Count() != profile.Subscriptions.Count ||
            profile.Subscriptions.Sum(s => (long)Encoding.UTF8.GetByteCount(s.Content)) > 24 * 1024 * 1024 ||
            profile.ActiveSubscriptionId is null || profile.Theme is not ("Light" or "Dark") ||
            !ThemeManager.Accents.Contains(profile.Accent) || profile.Language is not ("zh-CN" or "en-US") ||
            profile.Devices is null || profile.Devices.Count is < 1 or > 8 ||
            profile.Devices.Any(d => d is null || string.IsNullOrEmpty(d.Id) || d.Name is null ||
                d.Name.Length > 64 || d.SelectedNode is null) ||
            profile.Devices.Select(d => d.Id).Distinct().Count() != profile.Devices.Count)
            throw new InvalidOperationException("保存的设置版本、字段或订阅大小不受支持。");
    }

    public static void Save(AppProfile profile) => Save(Pathname, profile);

    private static void Save(string path, AppProfile profile)
    {
        Validate(profile);
        MakePrivate(Path.GetDirectoryName(path)!);
        var data = JsonSerializer.SerializeToUtf8Bytes(profile);
        try
        {
            if (data.Length > 32 * 1024 * 1024)
                throw new InvalidOperationException("订阅保存内容过大，请删除不再使用的订阅。");
            var encrypted = ProtectedData.Protect(data, Entropy, DataProtectionScope.CurrentUser);
            var temporary = path + ".tmp";
            File.WriteAllBytes(temporary, encrypted);
            if (File.Exists(path))
            {
                var validPrimary = true;
                try { Load(path); }
                catch (Exception ex) when (ex is IOException or CryptographicException or JsonException or InvalidOperationException)
                { validPrimary = false; }
                // Do not overwrite the last recoverable backup with a corrupt primary.
                File.Replace(temporary, path, validPrimary ? path + ".bak" : null);
            }
            else File.Move(temporary, path);
        }
        finally { CryptographicOperations.ZeroMemory(data); }
    }

    internal static void VerifyOffline()
    {
        const string secret = "OFFLINE-SUBSCRIPTION-SECRET";
        var directory = Path.Combine(Path.GetTempPath(), "PS5-profile-test-" + Guid.NewGuid().ToString("N"));
        var path = Path.Combine(directory, "profile.protected");
        try
        {
            if (new AppProfile().Theme != "Dark")
                throw new InvalidOperationException("首次启动主题必须为深色。");
            var profile = new AppProfile { Subscription = secret, SelectedNode = "offline", Independent = true, Theme = "Light", Accent = "LightSalmon", Language = "en-US" };
            Save(path, profile);
            Save(path, profile);
            var encrypted = File.ReadAllBytes(path);
            if (encrypted.AsSpan().IndexOf(Encoding.UTF8.GetBytes(secret)) >= 0)
                throw new InvalidOperationException("保存的订阅包含明文。");
            var restored = Load(path);
            if (restored.Subscription != secret || restored.SelectedNode != "offline" || !restored.Independent ||
                restored.Theme != "Light" || restored.Accent != "LightSalmon" || restored.Language != "en-US")
                throw new InvalidOperationException("设置加密保存与恢复验证失败。");
            encrypted[encrypted.Length / 2] ^= 0xFF;
            File.WriteAllBytes(path, encrypted);
            var rejected = false;
            try { Load(path); }
            catch (CryptographicException) { rejected = true; }
            if (!rejected) throw new InvalidOperationException("损坏的加密设置被接受。");
            if (LoadRecoverable(path).Subscription != secret)
                throw new InvalidOperationException("损坏主文件后的备份恢复失败。");
            Save(path, profile);
            if (Load(path + ".bak").Subscription != secret)
                throw new InvalidOperationException("保存时覆盖了可恢复的备份。");
            File.Delete(path);
            if (LoadRecoverable(path).Subscription != secret)
                throw new InvalidOperationException("缺失主文件后的备份恢复失败。");
            Save(path, new AppProfile { Schema = 1, Subscription = secret, Independent = false });
            var migrated = Load(path);
            if (migrated.Schema != 4 || !migrated.Independent || migrated.Subscription != secret ||
                migrated.Subscriptions.Count != 1 || migrated.Subscriptions[0].Content != secret)
                throw new InvalidOperationException("旧版设置迁移失败。");
            var first = new SubscriptionProfile { Content = secret, Name = "One", SelectedNode = "A" };
            var second = new SubscriptionProfile { Content = secret + "-two", Name = "Two", SelectedNode = "B", Total = 200000000000 };
            Save(path, new AppProfile { Subscriptions = [first, second], ActiveSubscriptionId = second.Id });
            var multiple = Load(path);
            if (multiple.Subscriptions.Count != 2 || multiple.ActiveSubscriptionId != second.Id ||
                multiple.Subscriptions[1].SelectedNode != "B" || multiple.Subscriptions[1].Total != second.Total)
                throw new InvalidOperationException("多订阅保存恢复失败。");
            Save(path, new AppProfile
            {
                Devices =
                [
                    new DeviceProfile { Name = "设备 1", SelectedNode = "A", IncludedInAcceleration = true },
                    new DeviceProfile { Name = "客厅主机", SelectedNode = "B", IncludedInAcceleration = false, ParametersExpanded = false },
                ],
            });
            var devices = Load(path);
            if (devices.Devices.Count != 2 || devices.Devices[1].Name != "客厅主机" ||
                devices.Devices[1].ParametersExpanded || devices.Devices[1].IncludedInAcceleration)
                throw new InvalidOperationException("多设备设置保存恢复失败。");
        }
        finally
        {
            File.Delete(path);
            File.Delete(path + ".tmp");
            File.Delete(path + ".bak");
            if (Directory.Exists(directory)) Directory.Delete(directory);
        }
    }

    internal static async Task VerifyRestartAsync()
    {
        var directory = Path.Combine(Path.GetTempPath(), "PS5-restart-test-" + Guid.NewGuid().ToString("N"));
        var path = Path.Combine(directory, "profile.protected");
        var first = new SubscriptionProfile { Name = "First", Content = RestartSubscription, SelectedNode = "Restart First" };
        var second = new SubscriptionProfile { Name = "Second", Content = RestartSubscription, SelectedNode = "Restart Second" };
        Save(path, new AppProfile { Subscriptions = [first, second], ActiveSubscriptionId = second.Id, Theme = "Dark", Accent = "LightSalmon", Language = "en-US" });
        try
        {
            for (var run = 0; run < 2; run++)
            {
                File.Delete(Path.Combine(directory, "restart-passed"));
                var start = new ProcessStartInfo(Path.Combine(AppContext.BaseDirectory, "Accelerator.exe"))
                {
                    UseShellExecute = false, CreateNoWindow = true, WindowStyle = ProcessWindowStyle.Hidden,
                };
                start.ArgumentList.Add("--profile-restart-check");
                start.ArgumentList.Add(directory);
                using var process = Process.Start(start) ?? throw new InvalidOperationException("无法启动重启验证。");
                using var deadline = new CancellationTokenSource(TimeSpan.FromSeconds(15));
                try { await process.WaitForExitAsync(deadline.Token); }
                finally { if (!process.HasExited) process.Kill(entireProcessTree: true); }
                if (process.ExitCode != 0 || !File.Exists(Path.Combine(directory, "restart-passed")))
                    throw new InvalidOperationException("桌面重启恢复验证未通过。");
                var restored = Load(path);
                if (restored.Subscriptions.Count != 2 || restored.ActiveSubscriptionId != second.Id ||
                    restored.Subscriptions[1].Content != RestartSubscription || restored.Subscriptions[1].SelectedNode != "Restart Second" ||
                    restored.Theme != "Dark" || restored.Accent != "LightSalmon" || restored.Language != "en-US")
                    throw new InvalidOperationException("关闭窗口时订阅被覆盖。");
            }
        }
        finally
        {
            foreach (var file in new[] { "profile.protected", "profile.protected.bak", "profile.protected.tmp", "restart-passed", "restart-failure.txt" })
                File.Delete(Path.Combine(directory, file));
            Directory.Delete(directory);
        }
    }
}

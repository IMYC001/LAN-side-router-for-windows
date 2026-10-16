using System.Diagnostics;
using System.IO;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Security.Principal;
using System.Text.Json;
using System.Text.RegularExpressions;
using Microsoft.Win32;

namespace Accelerator.App;

internal static class DriverInstaller
{
    internal const string PackageName = "Windows.Packet.Filter.3.6.2.1.x64.msi";
    internal const string PackageHash = "9C388C0B7F189F7FA98720BAE2CAECF7D64F30910838B80B438ECF8956B8502C";
    private const string InfHash = "71DBE4B33F3654F3E37D27F507D1CD6ADE70FB9C1A327435155C6C71DD137654";
    private const string DriverHash = "8623A9C34531CF73DE4286AD518B84AF03A331C78959769A1CA38723E3258984";
    private const string CertificateThumbprint = "FDB7CFFE05417C221025E753BC3999E44D10C22A";
    private const string ProductCode = "{4EC4289E-7F4B-424C-8EA9-B7AC9850FFBE}";
    private const string RegistryPath = @"SOFTWARE\LANDeviceAccelerator\Driver";
    private const string ServicePath = @"SYSTEM\CurrentControlSet\Services\ndisrd";
    private const string ProductPath = @"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\" + ProductCode;

    internal sealed class Record
    {
        public string Owner { get; set; } = "";
        public string Phase { get; set; } = "installing";
        public bool CertificateWasPresent { get; set; }
        public bool DriverFileWasPresent { get; set; }
        public string[] BaselineInfs { get; set; } = [];
        public string[] AddedInfs { get; set; } = [];
    }

    private static bool ServicePresent()
    {
        using var key = Registry.LocalMachine.OpenSubKey(ServicePath);
        return key is not null;
    }

    private static string? InstalledVersion()
    {
        using var key = Registry.LocalMachine.OpenSubKey(ProductPath);
        return key?.GetValue("DisplayVersion") as string;
    }

    private static Record? ReadRecord()
    {
        using var key = Registry.LocalMachine.OpenSubKey(RegistryPath);
        return key?.GetValue("State") is string json ? JsonSerializer.Deserialize<Record>(json) : null;
    }

    private static void SaveRecord(Record record)
    {
        using var key = Registry.LocalMachine.CreateSubKey(RegistryPath);
        key.SetValue("State", JsonSerializer.Serialize(record));
    }

    private static string InfDirectory => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "INF");
    private static string DriverFile => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), "drivers", "ndisrd.sys");

    private static bool MatchesInf(string name)
    {
        if (!Regex.IsMatch(name, @"^oem[0-9]+\.inf$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant))
            throw new IOException("驱动清理记录含无效文件名。");
        var path = Path.Combine(InfDirectory, name);
        if (!File.Exists(path)) return false;
        using var file = File.OpenRead(path);
        return Convert.ToHexString(SHA256.HashData(file)) == InfHash;
    }

    private static string[] FindInfs() =>
        Directory.EnumerateFiles(InfDirectory, "oem*.inf").Select(Path.GetFileName)
            .Where(name => name is not null && MatchesInf(name)).Select(name => name!).ToArray();

    private static bool CertificatePresent()
    {
        using var store = new X509Store(StoreName.TrustedPublisher, StoreLocation.LocalMachine);
        store.Open(OpenFlags.ReadOnly);
        return store.Certificates.Find(X509FindType.FindByThumbprint, CertificateThumbprint, false).Count > 0;
    }

    private static int RunSystem(string program, params string[] arguments)
    {
        var start = new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), program))
        {
            UseShellExecute = false, CreateNoWindow = true,
        };
        foreach (var argument in arguments) start.ArgumentList.Add(argument);
        using var process = Process.Start(start) ?? throw new IOException("无法启动 Windows 驱动安装组件。");
        process.WaitForExit();
        return process.ExitCode;
    }

    private static void ValidateOwner(string owner)
    {
        if (!Guid.TryParseExact(owner, "D", out _))
            throw new IOException("无效的安装所有权标识。");
        using var identity = WindowsIdentity.GetCurrent();
        if (!new WindowsPrincipal(identity).IsInRole(WindowsBuiltInRole.Administrator))
            throw new IOException("安装或卸载驱动需要管理员权限。");
        if (InstallerMaintenance.CheckIdle() != 0)
            throw new IOException("请先停止加速并退出软件，再处理驱动。");
    }

    private static void ValidateRemoval(Record record, string owner, string? version, bool servicePresent)
    {
        if (record.Owner != owner)
            throw new IOException("该驱动属于另一份安装，已拒绝卸载。");
        if (version is not null && version != "3.6.2.1")
            throw new IOException("驱动已被其他程序升级，不能按旧记录卸载。");
        if (servicePresent && version is null)
            throw new IOException("驱动存在但安装记录已变化，已停止自动清理。");
        if (record.AddedInfs.Intersect(record.BaselineInfs, StringComparer.OrdinalIgnoreCase).Any())
            throw new IOException("驱动清理记录与安装前记录冲突。");
        foreach (var name in record.AddedInfs.Concat(record.BaselineInfs))
            if (!Regex.IsMatch(name, @"^oem[0-9]+\.inf$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant))
                throw new IOException("驱动清理记录含无效文件名。");
    }

    public static int Install(string owner)
    {
        ValidateOwner(owner);
        var previous = ReadRecord();
        if (previous is not null && previous.Owner != owner)
            throw new IOException("电脑已有另一份软件的驱动所有权记录，已拒绝覆盖。");
        if (ServicePresent() || InstalledVersion() is not null)
        {
            if (previous is { Phase: "installed" } && previous.Owner == owner) return 0;
            throw new IOException("驱动已由其他程序安装，请重新运行安装器以复用它。");
        }
        var package = Path.Combine(AppContext.BaseDirectory, "drivers", PackageName);
        using var source = new FileStream(package, FileMode.Open, FileAccess.Read, FileShare.Read);
        if (Convert.ToHexString(SHA256.HashData(source)) != PackageHash)
            throw new IOException("驱动包校验失败，未执行安装。");
        var record = previous ?? new Record
        {
            Owner = owner, CertificateWasPresent = CertificatePresent(), BaselineInfs = FindInfs(),
            DriverFileWasPresent = File.Exists(DriverFile),
        };
        record.Phase = "installing";
        SaveRecord(record);
        var exit = RunSystem("msiexec.exe", "/i", package, "/qn", "/norestart");
        record.AddedInfs = FindInfs().Except(record.BaselineInfs, StringComparer.OrdinalIgnoreCase).ToArray();
        var success = exit is 0 or 3010;
        record.Phase = success ? "installed" : "installFailed";
        SaveRecord(record);
        if (!success) throw new IOException($"驱动安装未完成（Windows 代码 {exit}），软件卸载入口仍可用于清理。");
        if (exit == 0 && (!ServicePresent() || InstalledVersion() != "3.6.2.1"))
            throw new IOException("Windows 返回安装成功，但驱动注册未确认，请重启后检查。");
        return exit;
    }

    public static int Remove(string owner)
    {
        ValidateOwner(owner);
        var record = ReadRecord();
        if (record is null)
        {
            if (ServicePresent()) throw new IOException("驱动所有权记录缺失，不能自动删除共享驱动。");
            return 0;
        }
        ValidateRemoval(record, owner, InstalledVersion(), ServicePresent());
        // Recover an interrupted install without including pre-existing INF packages.
        if (record.Phase is "installing" or "installFailed")
            record.AddedInfs = FindInfs().Except(record.BaselineInfs, StringComparer.OrdinalIgnoreCase).ToArray();
        foreach (var name in record.AddedInfs)
            if (File.Exists(Path.Combine(InfDirectory, name)) && !MatchesInf(name))
                throw new IOException("驱动包已被其他程序替换，已停止清理。");
        SaveRecord(record);
        var reboot = false;
        if (InstalledVersion() is not null)
        {
            var exit = RunSystem("msiexec.exe", "/x", ProductCode, "/qn", "/norestart");
            if (exit is not (0 or 3010)) throw new IOException($"驱动卸载未完成（Windows 代码 {exit}），请保留软件后重试。");
            reboot = exit == 3010;
        }
        if (ServicePresent())
            throw new IOException("驱动仍在使用或等待重启，请重启后再次卸载软件。");
        foreach (var name in record.AddedInfs)
        {
            if (!MatchesInf(name)) continue;
            // No /force or /uninstall: never detach a package from another device.
            var exit = RunSystem("pnputil.exe", "/delete-driver", name);
            if (exit is not (0 or 3010) || File.Exists(Path.Combine(InfDirectory, name)))
                throw new IOException("Windows 尚未删除本软件添加的驱动包，请重启后再次卸载软件。");
            reboot |= exit == 3010;
        }
        if (!record.DriverFileWasPresent && File.Exists(DriverFile))
        {
            if ((File.GetAttributes(DriverFile) & FileAttributes.ReparsePoint) != 0)
                throw new IOException("系统驱动文件为链接，已停止清理。");
            using (var driver = File.OpenRead(DriverFile))
                if (Convert.ToHexString(SHA256.HashData(driver)) != DriverHash)
                    throw new IOException("系统驱动文件已变化，不能按旧记录删除。");
            File.Delete(DriverFile);
        }
        if (!record.CertificateWasPresent)
        {
            using var store = new X509Store(StoreName.TrustedPublisher, StoreLocation.LocalMachine);
            store.Open(OpenFlags.ReadWrite);
            foreach (var certificate in store.Certificates.Find(X509FindType.FindByThumbprint, CertificateThumbprint, false))
                store.Remove(certificate);
            if (CertificatePresent()) throw new IOException("本软件添加的发布者证书尚未清理，请重试。");
        }
        DeleteRecord();
        return reboot ? 3010 : 0;
    }

    public static int Release(string owner)
    {
        ValidateOwner(owner);
        var record = ReadRecord();
        if (record is not null && record.Owner != owner)
            throw new IOException("驱动所有权记录属于另一份安装。");
        DeleteRecord();
        return 0;
    }

    private static void DeleteRecord()
    {
        Registry.LocalMachine.DeleteSubKeyTree(RegistryPath, false);
        using (var key = Registry.LocalMachine.OpenSubKey(@"SOFTWARE\LANDeviceAccelerator", true))
            if (key is not null && key.SubKeyCount == 0 && key.ValueCount == 0)
            {
                key.Close();
                Registry.LocalMachine.DeleteSubKey(@"SOFTWARE\LANDeviceAccelerator", false);
            }
    }

    internal static void VerifyOffline()
    {
        var owner = Guid.NewGuid().ToString("D");
        var record = new Record { Owner = owner, BaselineInfs = ["oem2.inf"], AddedInfs = ["oem3.inf"] };
        ValidateRemoval(record, owner, "3.6.2.1", true);
        ValidateRemoval(record, owner, null, false);
        void MustRefuse(Action action)
        {
            try { action(); }
            catch (IOException) { return; }
            throw new InvalidOperationException("驱动卸载保护验证失败。");
        }
        MustRefuse(() => ValidateRemoval(record, Guid.NewGuid().ToString("D"), "3.6.2.1", true));
        MustRefuse(() => ValidateRemoval(record, owner, "3.7.0", true));
        MustRefuse(() => ValidateRemoval(record, owner, null, true));
        record.AddedInfs = ["oem2.inf"];
        MustRefuse(() => ValidateRemoval(record, owner, null, false));
        record.AddedInfs = [@"..\unrelated.inf"];
        MustRefuse(() => ValidateRemoval(record, owner, null, false));
    }
}

using System.Diagnostics;
using System.IO;
using System.Buffers.Binary;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;

namespace Accelerator.App;

internal static class InstallerMaintenance
{
    internal const int Busy = 10;
    internal static bool ReparseTestPassed { get; private set; }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern SafeFileHandle CreateFileW(string path, uint access, uint share, nint security,
        uint disposition, uint flags, nint template);
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DeviceIoControl(SafeFileHandle file, uint code, byte[] input, uint length,
        nint output, uint outputLength, out uint returned, nint overlapped);

    private static void CreateFixtureJunction(string path, string target)
    {
        Directory.CreateDirectory(path);
        var substitute = Encoding.Unicode.GetBytes(@"\??\" + Path.GetFullPath(target));
        var display = Encoding.Unicode.GetBytes(Path.GetFullPath(target));
        var data = new byte[16 + substitute.Length + 2 + display.Length + 2];
        BinaryPrimitives.WriteUInt32LittleEndian(data, 0xA0000003);
        BinaryPrimitives.WriteUInt16LittleEndian(data.AsSpan(4), checked((ushort)(data.Length - 8)));
        BinaryPrimitives.WriteUInt16LittleEndian(data.AsSpan(10), checked((ushort)substitute.Length));
        BinaryPrimitives.WriteUInt16LittleEndian(data.AsSpan(12), checked((ushort)(substitute.Length + 2)));
        BinaryPrimitives.WriteUInt16LittleEndian(data.AsSpan(14), checked((ushort)display.Length));
        substitute.CopyTo(data, 16);
        display.CopyTo(data, 16 + substitute.Length + 2);
        using var file = CreateFileW(path, 0x40000000, 7, 0, 3, 0x02200000, 0);
        if (file.IsInvalid || !DeviceIoControl(file, 0x000900A4, data, (uint)data.Length, 0, 0, out _, 0))
            throw new Win32Exception(Marshal.GetLastWin32Error());
    }

    public static int CheckIdle()
    {
        foreach (var name in new[] { "Accelerator", "accelerator-engine" })
        {
            foreach (var process in Process.GetProcessesByName(name))
            {
                using (process)
                    if (process.Id != Environment.ProcessId && !process.HasExited)
                        return Busy;
            }
        }
        return 0;
    }

    public static int CleanData()
    {
        if (CheckIdle() != 0) return Busy;
        DeleteTree(ProfileStore.Root,
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "PS5Accelerator");
        return 0;
    }

    private static void RejectLink(string path)
    {
        if ((File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0)
            throw new IOException("设置目录包含链接，已停止清理以保护其他文件。");
    }

    private static void DeleteTree(string target, string parent, string leaf)
    {
        target = Path.TrimEndingDirectorySeparator(Path.GetFullPath(target));
        parent = Path.TrimEndingDirectorySeparator(Path.GetFullPath(parent));
        if (!string.Equals(target, Path.Combine(parent, leaf), StringComparison.OrdinalIgnoreCase))
            throw new IOException("设置清理目录不符合预期。");
        for (var ancestor = new DirectoryInfo(parent); ancestor is not null; ancestor = ancestor.Parent)
            RejectLink(ancestor.FullName);
        try { RejectLink(target); }
        catch (FileNotFoundException) { return; }
        catch (DirectoryNotFoundException) { return; }
        // Preflight the entire tree before deleting anything; never follow links.
        var paths = new List<string>();
        void Scan(string path)
        {
            RejectLink(path);
            if (Directory.Exists(path))
                foreach (var child in Directory.EnumerateFileSystemEntries(path))
                    Scan(child);
            paths.Add(path);
        }
        Scan(target);
        foreach (var path in paths)
        {
            RejectLink(path);
            if (Directory.Exists(path)) Directory.Delete(path, false);
            else File.Delete(path);
        }
    }

    internal static void VerifyOffline()
    {
        using (var check = Process.Start(new ProcessStartInfo(Path.Combine(AppContext.BaseDirectory, "Accelerator.exe"),
            "--installer-check") { UseShellExecute = false, CreateNoWindow = true }))
        {
            if (check is null || !check.WaitForExit(10000) || check.ExitCode != Busy)
                throw new InvalidOperationException("安装维护没有拒绝运行中的软件。");
        }
        var parent = Path.Combine(Path.GetTempPath(), "Accelerator-uninstall-test-" + Guid.NewGuid().ToString("N"));
        var target = Path.Combine(parent, "PS5Accelerator");
        Directory.CreateDirectory(Path.Combine(target, "runtime", "fixture"));
        Directory.CreateDirectory(Path.Combine(target, "sessions", "fixture"));
        File.WriteAllText(Path.Combine(target, "profile.protected"), "OFFLINE");
        File.WriteAllText(Path.Combine(target, "profile.protected.bak"), "OFFLINE");
        File.WriteAllText(Path.Combine(target, "runtime", "fixture", "config.yaml"), "OFFLINE");
        var unrelated = Path.Combine(parent, "unrelated.txt");
        File.WriteAllText(unrelated, "KEEP");
        try
        {
            var refused = false;
            try { DeleteTree(parent, parent, "PS5Accelerator"); }
            catch (IOException) { refused = true; }
            if (!refused || !Directory.Exists(target))
                throw new InvalidOperationException("卸载清理目录限制验证失败。");
            var link = Path.Combine(target, "outside-link");
            CreateFixtureJunction(link, parent);
            refused = false;
            try { DeleteTree(target, parent, "PS5Accelerator"); }
            catch (IOException) { refused = true; }
            if (!refused || !File.Exists(unrelated) || !File.Exists(Path.Combine(target, "profile.protected")))
                throw new InvalidOperationException("卸载清理没有拒绝目录链接。");
            Directory.Delete(link, false);
            ReparseTestPassed = true;
            // Locked files must be reported; do not schedule credentials for reboot deletion.
            using (var held = File.Open(Path.Combine(target, "profile.protected"), FileMode.Open, FileAccess.Read, FileShare.None))
            {
                refused = false;
                try { DeleteTree(target, parent, "PS5Accelerator"); }
                catch (IOException) { refused = true; }
                if (!refused) throw new InvalidOperationException("卸载清理忽略了文件占用。");
            }
            DeleteTree(target, parent, "PS5Accelerator");
            if (Directory.Exists(target) || File.ReadAllText(unrelated) != "KEEP")
                throw new InvalidOperationException("卸载数据清理验证失败。");
        }
        finally
        {
            var link = Path.Combine(target, "outside-link");
            if (Directory.Exists(link)) Directory.Delete(link, false);
            if (Directory.Exists(target)) DeleteTree(target, parent, "PS5Accelerator");
            File.Delete(unrelated);
            Directory.Delete(parent, false);
        }
    }
}

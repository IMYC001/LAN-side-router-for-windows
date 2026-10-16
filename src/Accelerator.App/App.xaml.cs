using System.IO;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Windows;

namespace Accelerator.App;

public partial class App : Application
{
    private Mutex? instance;
    protected override void OnStartup(StartupEventArgs e)
    {
        if (e.Args.Length > 0 && e.Args[0].StartsWith("--installer-", StringComparison.Ordinal))
        {
            try
            {
                var result = e.Args switch
                {
                    ["--installer-check"] => InstallerMaintenance.CheckIdle(),
                    ["--installer-clean-data"] => InstallerMaintenance.CleanData(),
                    ["--installer-install-driver", var owner] => DriverInstaller.Install(owner),
                    ["--installer-remove-driver", var owner] => DriverInstaller.Remove(owner),
                    ["--installer-release-driver", var owner] => DriverInstaller.Release(owner),
                    _ => throw new InvalidOperationException("无效的安装维护命令。"),
                };
                Shutdown(result);
            }
            catch (Exception ex)
            {
                MessageBox.Show(UiText.Get(ex.Message), UiText.ProductTitle);
                Shutdown(20);
            }
            return;
        }
        if (!e.Args.Contains("--smoke-test") && !e.Args.Contains("--profile-restart-check") &&
            Mutex.TryOpenExisting(@"Local\LANDeviceAccelerator-Setup", out var maintenance))
        {
            maintenance.Dispose();
            MessageBox.Show(UiText.Get("软件正在安装或卸载，请完成后再打开。"), UiText.ProductTitle);
            Shutdown();
            return;
        }
        if (!e.Args.Contains("--smoke-test") && !e.Args.Contains("--profile-restart-check"))
        {
            var sid = WindowsIdentity.GetCurrent().User?.Value ?? Environment.UserName;
            var suffix = Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(sid)))[..24];
            instance = new Mutex(true, @"Local\PS5Accelerator-" + suffix, out var created);
            if (!created)
            {
                instance.Dispose();
                instance = null;
                MessageBox.Show(UiText.Get("软件已在运行，请从任务栏或托盘打开。"), UiText.ProductTitle);
                Shutdown();
                return;
            }
        }
        DispatcherUnhandledException += (_, args) =>
        {
            if (MainWindow is Accelerator.App.MainWindow window) window.EmergencyStop();
            args.Handled = true;
            if (e.Args.Contains("--profile-restart-check"))
                File.WriteAllText(Path.Combine(ProfileStore.Root, "restart-failure.txt"), args.Exception.ToString());
            else if (e.Args.Contains("--smoke-test"))
            {
                var directory = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "..", "..", "test-results"));
                Directory.CreateDirectory(directory);
                File.WriteAllText(Path.Combine(directory, "smoke-failure.txt"), args.Exception.ToString());
            }
            else MessageBox.Show(UiText.Get("软件发生异常，加速已请求停止。如已修改设备，请恢复原来的 IP 和 DNS 设置。"), UiText.ProductTitle);
            Shutdown(1);
        };
        base.OnStartup(e);
    }

    protected override void OnExit(ExitEventArgs e)
    {
        if (MainWindow is Accelerator.App.MainWindow window) window.EmergencyStop();
        if (instance is not null)
        {
            instance.ReleaseMutex();
            instance.Dispose();
        }
        base.OnExit(e);
    }
}

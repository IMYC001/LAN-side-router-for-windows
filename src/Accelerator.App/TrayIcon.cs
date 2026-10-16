namespace Accelerator.App;

internal sealed class TrayIcon : IDisposable
{
    private readonly System.Windows.Forms.NotifyIcon icon;
    private readonly System.Drawing.Icon artwork;
    private bool disposed;

    public TrayIcon(MainWindow window, Action stop, Action exit)
    {
        using var stream = typeof(TrayIcon).Assembly.GetManifestResourceStream("Accelerator.Icon")
            ?? throw new InvalidOperationException("软件图标缺失。");
        artwork = new System.Drawing.Icon(stream, 32, 32);
        var menu = new System.Windows.Forms.ContextMenuStrip();
        menu.Items.Add("打开窗口", null, (_, _) => window.Dispatcher.Invoke(window.RestoreFromTray));
        menu.Items.Add("停止加速", null, (_, _) => window.Dispatcher.Invoke(stop));
        menu.Items.Add(new System.Windows.Forms.ToolStripSeparator());
        menu.Items.Add("退出", null, (_, _) => window.Dispatcher.Invoke(exit));
        icon = new System.Windows.Forms.NotifyIcon
        {
            Icon = artwork,
            Text = "局域网设备加速",
            ContextMenuStrip = menu,
            Visible = true,
        };
        icon.DoubleClick += (_, _) => window.Dispatcher.Invoke(window.RestoreFromTray);
        RefreshLanguage();
    }

    public void RefreshLanguage()
    {
        icon.Text = UiText.ProductTitle;
        foreach (System.Windows.Forms.ToolStripItem item in icon.ContextMenuStrip!.Items)
        {
            if (item is System.Windows.Forms.ToolStripSeparator) continue;
            item.Tag ??= item.Text;
            item.Text = UiText.Get(item.Tag as string ?? item.Text ?? "");
        }
    }

    public void Notify(string text) => icon.ShowBalloonTip(3000, UiText.ProductTitle, UiText.Get(text),
        System.Windows.Forms.ToolTipIcon.Info);

    public void Dispose()
    {
        if (disposed) return;
        disposed = true;
        icon.Visible = false;
        icon.ContextMenuStrip?.Dispose();
        icon.Dispose();
        artwork.Dispose();
    }
}

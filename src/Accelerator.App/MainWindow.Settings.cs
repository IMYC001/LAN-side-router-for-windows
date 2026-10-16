using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;

namespace Accelerator.App;

public partial class MainWindow
{
    private bool changingTheme;
    private CancellationTokenSource? latencyCancellation;
    private Task latencyTask = Task.CompletedTask;
    private bool pendingLatency;
    private List<NodeLatencyRow> latencyRows = [];
    private readonly System.Windows.Threading.DispatcherTimer selectedLatencyTimer = new() { Interval = TimeSpan.FromSeconds(30) };
    private CancellationTokenSource? selectedLatencyCancellation;
    private Task selectedLatencyTask = Task.CompletedTask;
    private bool closingLatency;

    private void ApplyTheme(string theme, string? accent = null)
    {
        changingTheme = true;
        ThemeManager.Apply(theme, this, accent);
        LightThemeOption.IsChecked = ThemeManager.Current == "Light";
        DarkThemeOption.IsChecked = ThemeManager.Current == "Dark";
        foreach (RadioButton option in AccentOptions.Children)
            option.IsChecked = option.Tag as string == ThemeManager.Accent;
        ApplySubscriptionSurfaceTheme();
        changingTheme = false;
        PresentGatewayAction(gatewaySession is not null, busy, stopping, SessionCoolingDown);
    }

    private void ApplySubscriptionSurfaceTheme()
    {
        // The subscription tree can be realized while a session locks controls.
        // Reapply its dynamic resources so WPF never falls back to a system white surface.
        SubscriptionsList.SetResourceReference(Control.BackgroundProperty, "WindowBrush");
        NodesList.SetResourceReference(Control.BackgroundProperty, "WindowBrush");
        ApplyListSurfaceTheme(SubscriptionsList);
        ApplyListSurfaceTheme(NodesList);
        SubscriptionsList.Items.Refresh();
        NodesList.Items.Refresh();
        SubscriptionsList.InvalidateVisual();
        NodesList.InvalidateVisual();
    }

    private void MainTabs_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (e.Source != MainTabs || MainTabs.SelectedIndex != 2) return;
        _ = Dispatcher.BeginInvoke(() =>
        {
            if (MainTabs.SelectedIndex == 2) ApplySubscriptionSurfaceTheme();
        }, System.Windows.Threading.DispatcherPriority.ContextIdle);
    }

    private async void RequestDeviceLatency(DeviceProfile device)
    {
        if (deviceLatencyCancellations.Remove(device.Id, out var previous))
        {
            previous.Cancel();
            previous.Dispose();
        }
        if (smokeTest || restartCheck || !Independent || subscriptionContent.Length == 0 ||
            string.IsNullOrWhiteSpace(device.SelectedNode) ||
            AdapterBox.SelectedItem is not AdapterInfo { Physical: true, Status: "Up" } adapter)
        {
            RefreshDeviceLatencies();
            return;
        }

        var row = latencyRows.FirstOrDefault(item => item.Name == device.SelectedNode);
        if (row is null) return;

        var cancellation = new CancellationTokenSource();
        deviceLatencyCancellations[device.Id] = cancellation;
        row.Set("测试中", "正在测量电脑到节点服务器的延迟", true);
        RefreshDeviceLatencies();
        try
        {
            var delay = await NodeLatency.MeasureAsync(subscriptionContent, row.Name, adapter, cancellation.Token);
            if (cancellation.IsCancellationRequested || !latencyRows.Contains(row) ||
                device.SelectedNode != row.Name || AdapterBox.SelectedItem != adapter || !Independent)
                return;
            row.Set($"{delay} ms", $"电脑到节点 {delay} ms；不代表实际游戏延迟");
        }
        catch (OperationCanceledException) { }
        catch (Exception)
        {
            if (!cancellation.IsCancellationRequested && latencyRows.Contains(row) &&
                device.SelectedNode == row.Name && AdapterBox.SelectedItem == adapter && Independent)
                row.Set("不可测试", "未收到节点直连响应；不代表游戏不可用");
        }
        finally
        {
            if (deviceLatencyCancellations.TryGetValue(device.Id, out var current) &&
                ReferenceEquals(current, cancellation))
                deviceLatencyCancellations.Remove(device.Id);
            cancellation.Dispose();
            RefreshDeviceLatencies();
        }
    }

    private void ApplyListSurfaceTheme(ListBox list)
    {
        list.ApplyTemplate();
        foreach (var viewer in FindVisualChildren<ScrollViewer>(list))
        {
            viewer.SetResourceReference(Control.BackgroundProperty, "WindowBrush");
            viewer.SetResourceReference(Control.ForegroundProperty, "TextBrush");
            viewer.InvalidateVisual();
        }
    }

    private static IEnumerable<T> FindVisualChildren<T>(DependencyObject root) where T : DependencyObject
    {
        for (var index = 0; index < VisualTreeHelper.GetChildrenCount(root); index++)
        {
            var child = VisualTreeHelper.GetChild(root, index);
            if (child is T match) yield return match;
            foreach (var nested in FindVisualChildren<T>(child)) yield return nested;
        }
    }

    private void Accent_Changed(object sender, RoutedEventArgs e)
    {
        if (changingTheme || sender is not RadioButton { Tag: string accent } || DarkThemeOption is null) return;
        ApplyTheme(ThemeManager.Current, accent);
        SaveProfile();
    }

    private void RefreshSelectedLatency(bool reset = false)
    {
        if (devices.Count == 0) return;
        if (reset)
        {
            selectedLatencyCancellation?.Cancel();
        }
        if (!Independent)
        {
            RefreshDeviceLatencies();
            return;
        }
        var selected = latencyRows.FirstOrDefault(r => r.Name == NodeBox.SelectedItem as string);
        if (devices.Count > 0)
        {
            RefreshDeviceLatencies();
        }
        if (smokeTest || restartCheck || closingLatency || restoringProfile || busy ||
            latencyCancellation is not null || selectedLatencyCancellation is not null ||
            selected is null || AdapterBox.SelectedItem is not AdapterInfo { Physical: true, Status: "Up" } adapter) return;
        var cancellation = new CancellationTokenSource();
        selectedLatencyCancellation = cancellation;
        selectedLatencyTask = MeasureSelectedLatencyAsync(selected, adapter, cancellation);
    }

    private async Task MeasureSelectedLatencyAsync(NodeLatencyRow row, AdapterInfo adapter, CancellationTokenSource cancellation,
        Func<CancellationToken, Task<int>>? measure = null)
    {
        try
        {
            int delay = await (measure?.Invoke(cancellation.Token) ??
                NodeLatency.MeasureAsync(subscriptionContent, row.Name, adapter, cancellation.Token));
            if (cancellation.IsCancellationRequested || !latencyRows.Contains(row) ||
                NodeBox.SelectedItem as string != row.Name || AdapterBox.SelectedItem != adapter || !Independent) return;
            row.Set($"{delay} ms", $"电脑到节点 {delay} ms；不代表实际游戏延迟");
            RefreshDeviceLatencies();
        }
        catch (OperationCanceledException) { }
        catch (Exception)
        {
            if (!cancellation.IsCancellationRequested && latencyRows.Contains(row) &&
                NodeBox.SelectedItem as string == row.Name && AdapterBox.SelectedItem == adapter && Independent)
            {
                row.Set("不可测试", "未收到节点直连响应；不代表游戏不可用");
                RefreshDeviceLatencies();
            }
        }
        finally
        {
            bool canceled = cancellation.IsCancellationRequested;
            if (selectedLatencyCancellation == cancellation) selectedLatencyCancellation = null;
            cancellation.Dispose();
            if (canceled && !closingLatency && !smokeTest && !restartCheck)
                _ = Dispatcher.BeginInvoke(() => RefreshSelectedLatency());
        }
    }

    private void Theme_Changed(object sender, RoutedEventArgs e)
    {
        if (changingTheme || sender is not RadioButton { Tag: string theme } || DarkThemeOption is null) return;
        ApplyTheme(theme);
        SaveProfile();
    }

    private void ResetLatencyRows(IEnumerable<string> names)
    {
        selectedLatencyCancellation?.Cancel();
        latencyRows = names.Select(n => new NodeLatencyRow(n)).ToList();
        NodesList.ItemsSource = latencyRows;
        LatencyNotice.Visibility = Visibility.Collapsed;
        RefreshDeviceLatencies();
    }

    private void SyncNodeList() => NodesList.SelectedItem =
        latencyRows.FirstOrDefault(r => r.Name == NodeBox.SelectedItem as string);

    private void RequestImportedLatency()
    {
        if (smokeTest || restartCheck) return;
        pendingLatency = true;
        UiText.Set(LatencyNotice, "订阅已导入，即将测试延迟；请选择节点");
        LatencyNotice.Visibility = Visibility.Visible;
    }

    private async void Latency_Click(object sender, RoutedEventArgs e)
    {
        if (latencyCancellation is not null) { await CancelLatencyAsync(); return; }
        BeginLatencyTest();
    }

    private void BeginLatencyTest(Func<int, CancellationToken, Task<int>>? measure = null)
    {
        if (busy || gatewaySession is not null || SessionCoolingDown || latencyCancellation is not null || latencyRows.Count == 0) return;
        LatencyNotice.Visibility = Visibility.Visible;
        if (AdapterBox.SelectedItem is not AdapterInfo adapter || !adapter.Physical || adapter.Status != "Up")
        {
            UiText.Set(LatencyNotice, "请选择已连接的局域网网卡后重试；请选择节点");
            return;
        }
        var cancellation = new CancellationTokenSource();
        selectedLatencyCancellation?.Cancel();
        latencyCancellation = cancellation;
        var rows = latencyRows.ToArray();
        var content = subscriptionContent;
        foreach (var row in rows) row.Set("等待", "等待测试", true);
        UiText.Set(LatencyNotice, $"正在测试 0 / {rows.Length}；请选择节点");
        UiText.Set(LatencyButtonText, "取消测试");
        UpdateControls();
        latencyTask = RunLatencyTestAsync(rows, content, adapter, cancellation, measure);
    }

    private async Task RunLatencyTestAsync(NodeLatencyRow[] rows, string content, AdapterInfo adapter,
        CancellationTokenSource cancellation, Func<int, CancellationToken, Task<int>>? measure = null)
    {
        int completed = 0;
        try
        {
            await selectedLatencyTask;
            await NodeLatency.RunAsync(rows.Length,
                measure ?? ((index, token) => NodeLatency.MeasureAsync(content, rows[index].Name, adapter, token)),
                result => Dispatcher.Invoke(() =>
                {
                    if (latencyCancellation != cancellation || cancellation.IsCancellationRequested) return;
                    rows[result.Index].Set(result.Display, result.Detail, result.Pending);
                    RefreshDeviceLatencies();
                    if (!result.Pending) completed++;
                    UiText.Set(LatencyNotice, $"正在测试 {completed} / {rows.Length}；请选择节点");
                }), cancellation.Token);
            UiText.Set(LatencyNotice, $"测试完成 · {rows.Count(r => r.Display.EndsWith(" ms"))} / {rows.Length} 可达，请选择节点");
        }
        catch (OperationCanceledException)
        {
            UiText.Set(LatencyNotice, "测试已取消，已完成结果保留；请选择节点");
        }
        catch (Exception)
        {
            UiText.Set(LatencyNotice, "测试未完成，可重试；请选择节点");
        }
        finally
        {
            foreach (var row in rows.Where(r => r.Pending)) row.Set("未测试", "测试未完成，可重试");
            RefreshDeviceLatencies();
            latencyCancellation = null;
            cancellation.Dispose();
            UiText.Set(LatencyButtonText, "测延迟");
            UpdateControls();
        }
    }

    private async Task CancelLatencyAsync()
    {
        latencyCancellation?.Cancel();
        selectedLatencyCancellation?.Cancel();
        await Task.WhenAll(latencyTask, selectedLatencyTask);
    }

    private async Task VerifyLatencyPresentationAsync(SubscriptionProfile first, SubscriptionProfile second)
    {
        BeginLatencyTest((index, _) => index == 0 ? Task.FromResult(42) : Task.FromException<int>(new TimeoutException()));
        await latencyTask;
        if (latencyRows[0].Display != "42 ms" || latencyRows[1].Display != "超时" ||
            latencyCancellation is not null || LatencyButtonText.Text != "测延迟" ||
            !LatencyNotice.Text.Contains("请选择节点") || PrimaryDevice.NodeLatency != "42")
            throw new InvalidOperationException("节点延迟结果或非弹窗提醒错误。");
        var started = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        BeginLatencyTest(async (_, token) =>
        {
            started.TrySetResult();
            await Task.Delay(Timeout.Infinite, token);
            return 999;
        });
        await started.Task.WaitAsync(TimeSpan.FromSeconds(5));
        await WorkAsync("离线切换验证", () => ParseSubscriptionAsync(System.Text.Encoding.UTF8.GetBytes(second.Content), second));
        if (!latencyTask.IsCompleted || latencyCancellation is not null ||
            latencyRows.Any(r => r.Display != "未测试") || activeSubscription != second)
            throw new InvalidOperationException("取消测试后旧节点结果污染了新订阅。");
        await ParseSubscriptionAsync(System.Text.Encoding.UTF8.GetBytes(first.Content), first);
        var selected = latencyRows[0];
        var adapter = (AdapterInfo)AdapterBox.SelectedItem;
        using var source = new CancellationTokenSource();
        await MeasureSelectedLatencyAsync(selected, adapter, source, _ => Task.FromResult(38));
        if (PrimaryDevice.NodeLatency != "38" || selected.Display != "38 ms")
            throw new InvalidOperationException("首页延迟与所选节点不同步。");
        var delayed = new TaskCompletionSource<int>(TaskCreationOptions.RunContinuationsAsynchronously);
        using var stale = new CancellationTokenSource();
        var previous = MeasureSelectedLatencyAsync(selected, adapter, stale, _ => delayed.Task);
        PrimaryCard.QuickNodeBox.SelectedIndex = 1;
        delayed.SetResult(999);
        await previous;
        if (PrimaryDevice.NodeLatency == "999")
            throw new InvalidOperationException("旧节点测试覆盖了新节点延迟。");
        OutletBox.SelectedIndex = 0;
        if (PrimaryDevice.NodeLatency != "--")
            throw new InvalidOperationException("Clash 共存模式不应显示独立节点延迟。");
        OutletBox.SelectedIndex = 1;
        PrimaryCard.QuickNodeBox.SelectedIndex = 0;
    }
}

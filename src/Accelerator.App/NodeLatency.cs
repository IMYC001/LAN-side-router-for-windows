using System.ComponentModel;
namespace Accelerator.App;

internal sealed class NodeLatencyRow(string name) : INotifyPropertyChanged
{
    public string Name { get; } = name;
    public string Display { get; private set; } = "未测试";
    public string Detail { get; private set; } = "电脑到节点服务器的延迟";
    public bool Pending { get; private set; }
    public event PropertyChangedEventHandler? PropertyChanged;
    public void Set(string display, string detail, bool pending = false)
    {
        Display = display;
        Detail = detail;
        Pending = pending;
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(null));
    }
}

internal record LatencyResult(int Index, string Display, string Detail, bool Pending = false);

internal static class NodeLatency
{
    public static async Task<int> MeasureAsync(string subscription, string node, AdapterInfo adapter, CancellationToken token)
    {
        var result = await EngineClient.CallAsync(new
        {
            command = "node-latency", subscription,
            latency = new { node, index = adapter.Index, source = adapter.IPv4 },
        }, token);
        return result.GetProperty("delay").GetInt32();
    }

    // Bound direct probes do not start proxy cores or change routes.
    internal static async Task RunAsync(int count, Func<int, CancellationToken, Task<int>> measure,
        Action<LatencyResult> report, CancellationToken cancellation)
    {
        await Parallel.ForEachAsync(Enumerable.Range(0, count),
            new ParallelOptions { MaxDegreeOfParallelism = 6, CancellationToken = cancellation },
            async (index, token) =>
            {
                report(new(index, "测试中", "正在测量电脑到节点服务器的延迟", true));
                using var deadline = CancellationTokenSource.CreateLinkedTokenSource(token);
                deadline.CancelAfter(TimeSpan.FromSeconds(8));
                LatencyResult result;
                try
                {
                    int delay = await measure(index, deadline.Token);
                    result = new(index, $"{delay} ms", $"电脑到节点 {delay} ms；不代表实际游戏延迟");
                }
                catch (OperationCanceledException) when (token.IsCancellationRequested) { throw; }
                catch (Exception ex)
                {
                    // Never surface core output, subscription credentials or server addresses.
                    result = ex switch
                    {
                        OperationCanceledException or TimeoutException => new(index, "超时", "测试超时，可稍后重试"),
                        InvalidOperationException => new(index, "不可测试", "节点未响应直连检测，或不支持该检测；不代表游戏不可用"),
                        _ => new(index, "未连通", "未收到节点响应；这不一定代表游戏不可用"),
                    };
                }
                token.ThrowIfCancellationRequested();
                report(result);
            });
    }

    internal static async Task VerifyOfflineAsync()
    {
        int active = 0, peak = 0;
        var results = new System.Collections.Concurrent.ConcurrentBag<LatencyResult>();
        await RunAsync(4, async (index, token) =>
        {
            int current = Interlocked.Increment(ref active);
            Interlocked.Exchange(ref peak, Math.Max(peak, current));
            try
            {
                await Task.Delay(10, token);
                if (index == 1) throw new TimeoutException();
                if (index == 2) throw new InvalidOperationException("PRIVATE-SECRET");
                return 35 + index;
            }
            finally { Interlocked.Decrement(ref active); }
        }, results.Add, CancellationToken.None);
        if (peak > 6 || results.Count(r => !r.Pending) != 4 ||
            !results.Any(r => r.Index == 1 && r.Display == "超时") ||
            results.Any(r => r.Detail.Contains("PRIVATE-SECRET")))
            throw new InvalidOperationException("节点延迟并发或失败隔离验证失败。");
        using var cancel = new CancellationTokenSource();
        var pending = RunAsync(20, async (_, token) =>
        {
            cancel.Cancel();
            await Task.Delay(1000, token);
            return 1;
        }, _ => { }, cancel.Token);
        try { await pending; throw new InvalidOperationException("节点测试未取消。"); }
        catch (OperationCanceledException) when (cancel.IsCancellationRequested) { }
    }
}

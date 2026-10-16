using System.Globalization;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text.Json.Serialization;

namespace Accelerator.App;

internal sealed class SubscriptionProfile
{
    public string Id { get; set; } = Guid.NewGuid().ToString("N");
    public string Name { get; set; } = "Clash YAML";
    public string Url { get; set; } = "";
    public string Content { get; set; } = "";
    public string SelectedNode { get; set; } = "";
    public DateTimeOffset UpdatedAt { get; set; } = DateTimeOffset.UtcNow;
    public long? Upload { get; set; }
    public long? Download { get; set; }
    public long? Total { get; set; }
    public long? Expire { get; set; }
    [JsonIgnore] public string Source => Uri.TryCreate(Url, UriKind.Absolute, out var uri) ? uri.Host : "本地文件";
    [JsonIgnore] public string Usage => Upload.HasValue && Download.HasValue
        ? $"{Bytes((double)Upload.Value + Download.Value)} / {(Total is > 0 ? Bytes(Total.Value) : "不限量或未提供")}"
        : "用量未提供";
    [JsonIgnore] public double UsagePercent => Total is > 0 && Upload.HasValue && Download.HasValue
        ? Math.Clamp(((double)Upload.Value + Download.Value) / Total.Value * 100, 0, 100) : 0;
    [JsonIgnore] public string Expiry => Expire is > 0 && Expire <= 253402300799
        ? DateTimeOffset.FromUnixTimeSeconds(Expire.Value).ToLocalTime().ToString("yyyy-MM-dd") : "到期时间未提供";
    [JsonIgnore] public string Updated => UpdatedAt == default ? "更新时间未知" :
        DateTimeOffset.UtcNow - UpdatedAt < TimeSpan.FromMinutes(1) ? "刚刚更新" :
        DateTimeOffset.UtcNow - UpdatedAt < TimeSpan.FromHours(1) ? $"{Math.Max(1, (int)(DateTimeOffset.UtcNow - UpdatedAt).TotalMinutes)} 分钟前" :
        DateTimeOffset.UtcNow - UpdatedAt < TimeSpan.FromDays(1) ? $"{(int)(DateTimeOffset.UtcNow - UpdatedAt).TotalHours} 小时前" :
        UpdatedAt.ToLocalTime().ToString("MM-dd HH:mm");
    [JsonIgnore] public string UsageDetails => "已用 / 总额度；按 1024 进制换算。用量来自服务商上次更新的记录，可能延迟。" +
        (UpdatedAt == default ? "" : $"\n记录时间：{UpdatedAt.ToLocalTime():yyyy-MM-dd HH:mm:ss}") +
        "\n点击卡片的更新按钮重新获取；本次加速流量不等于整个订阅的计费用量。";

    private static string Bytes(double value) => value >= 1073741824 ? $"{value / 1073741824:F1} GB" :
        value >= 1048576 ? $"{value / 1048576:F1} MB" :
        value >= 1024 ? $"{value / 1024:F1} KB" : $"{value:F0} B";

    public static SubscriptionProfile FromResponse(string url, HttpResponseHeaders headers, HttpContentHeaders content)
    {
        var result = new SubscriptionProfile { Url = url, Name = new Uri(url).Host };
        var disposition = content.ContentDisposition;
        var filename = disposition?.FileNameStar ?? disposition?.FileName;
        if (!string.IsNullOrWhiteSpace(filename)) result.Name = CleanName(filename);
        if (headers.TryGetValues("Subscription-Userinfo", out var values))
            foreach (var field in string.Join(";", values).Split(';'))
            {
                var parts = field.Split('=', 2, StringSplitOptions.TrimEntries);
                if (parts.Length != 2 || !long.TryParse(parts[1], NumberStyles.None, CultureInfo.InvariantCulture, out var value)) continue;
                switch (parts[0].ToLowerInvariant())
                {
                    case "upload": result.Upload = value; break;
                    case "download": result.Download = value; break;
                    case "total": result.Total = value; break;
                    case "expire": result.Expire = value; break;
                }
            }
        return result;
    }

    private static string CleanName(string name)
    {
        name = name.Trim('"').Replace('\\', '/');
        name = name[(name.LastIndexOf('/') + 1)..];
        name = new string(name.Where(c => !char.IsControl(c)).Take(100).ToArray());
        return string.IsNullOrWhiteSpace(name) ? "Clash YAML" : name;
    }

    internal static void VerifyOffline()
    {
        using var response = new HttpResponseMessage();
        response.Headers.TryAddWithoutValidation("Subscription-Userinfo", "upload=1073741824; download=2147483648; total=10737418240; expire=1792108800");
        response.Content = new StringContent("");
        response.Content.Headers.ContentDisposition = new ContentDispositionHeaderValue("attachment") { FileNameStar = "Game.yaml" };
        var result = FromResponse("https://example.com/sub?token=SECRET", response.Headers, response.Content.Headers);
        if (result.Name != "Game.yaml" || result.Source != "example.com" || result.UsagePercent != 30 ||
            result.Usage != "3.0 GB / 10.0 GB" || result.Expiry == "到期时间未提供")
            throw new InvalidOperationException("订阅卡片信息解析失败。");
        if (new SubscriptionProfile().Usage != "用量未提供")
            throw new InvalidOperationException("缺失的订阅用量不应伪造。");
        result.Upload = 7367111877;
        result.Download = 93546470584;
        result.Total = 214748364800;
        if (result.Usage != "94.0 GB / 200.0 GB" || !result.UsageDetails.Contains("记录时间"))
            throw new InvalidOperationException("订阅二进制容量或记录时间显示错误。");
        result.Upload = 0;
        result.Download = 512;
        result.Total = null;
        if (result.Usage != "512 B / 不限量或未提供")
            throw new InvalidOperationException("小额用量或未提供配额显示错误。");
    }
}

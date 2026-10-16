using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;
using System.ComponentModel;
using System.Text.Json;

namespace Accelerator.App;

internal static class SubscriptionDownloader
{
    internal const int MaxBytes = 8 * 1024 * 1024;
    private sealed record Route(string Name, bool UseProxy, Uri? Proxy = null);
    private sealed class CompatibilityReply
    {
        public byte[] Content { get; set; } = [];
        public string UserInfo { get; set; } = "";
        public string Disposition { get; set; } = "";
        public string Failure { get; set; } = "";
        public int Status { get; set; }
    }

    private sealed class TlsDiagnostic
    {
        public bool Rejected { get; private set; }
        public string Detail { get; private set; } = "";

        public bool Validate(SslPolicyErrors errors, X509ChainStatusFlags flags)
        {
            if (errors == SslPolicyErrors.None) return true;
            Rejected = true;
            Detail = CertificateFailure(errors, flags);
            return false;
        }
    }

    internal sealed record Result(byte[] Content, SubscriptionProfile Profile, string Route);

    public static Task<Result> FetchAsync(string address, string localProxy) =>
        FetchAsync(address, Routes(localProxy), CreateHandler, TimeSpan.FromSeconds(15), FetchCompatibleAsync);

    private static List<Route> Routes(string localProxy)
    {
        var routes = new List<Route> { new("系统连接", true), new("直接连接", false) };
        if (Uri.TryCreate("socks5://" + localProxy, UriKind.Absolute, out var proxy) &&
            IPAddress.TryParse(proxy.Host.Trim('[', ']'), out var ip) && IPAddress.IsLoopback(ip) &&
            proxy.Port is > 0 and <= 65535 && proxy.AbsolutePath == "/" &&
            proxy.UserInfo.Length == 0 && proxy.Query.Length == 0 && proxy.Fragment.Length == 0)
            routes.Add(new("本机 Clash 代理", true, proxy));
        return routes;
    }

    private static HttpClientHandler CreateHandler(Route route, TlsDiagnostic diagnostic) => new()
    {
        UseProxy = route.UseProxy,
        Proxy = route.Proxy is null ? null : new WebProxy(route.Proxy),
        AllowAutoRedirect = false,
        UseCookies = false,
        AutomaticDecompression = DecompressionMethods.GZip | DecompressionMethods.Deflate | DecompressionMethods.Brotli,
        ServerCertificateCustomValidationCallback = (_, _, chain, errors) => diagnostic.Validate(errors,
            chain?.ChainStatus.Aggregate(X509ChainStatusFlags.NoError, (flags, status) => flags | status.Status)
                ?? X509ChainStatusFlags.NoError),
    };

    private static async Task<Result> FetchAsync(string address, IReadOnlyList<Route> routes,
        Func<Route, TlsDiagnostic, HttpMessageHandler> handlerFactory, TimeSpan attemptTimeout,
        Func<string, Route, CancellationToken, Task<CompatibilityReply>>? compatible = null)
    {
        if (!Uri.TryCreate(address, UriKind.Absolute, out var uri) || uri.Scheme != "https" ||
            uri.UserInfo.Length != 0 || uri.Host.Length == 0)
            throw new InvalidOperationException("请输入有效的 HTTPS 订阅地址。");
        var failures = new List<string>();
        foreach (var route in routes)
        {
            var diagnostic = new TlsDiagnostic();
            using var deadline = new CancellationTokenSource(attemptTimeout);
            try
            {
                using var client = new HttpClient(handlerFactory(route, diagnostic)) { Timeout = Timeout.InfiniteTimeSpan };
                client.DefaultRequestHeaders.UserAgent.ParseAdd("ClashMeta/Accelerator-Preflight");
                using var response = await client.GetAsync(uri, HttpCompletionOption.ResponseHeadersRead, deadline.Token);
                if ((int)response.StatusCode is >= 300 and < 400)
                    throw new InvalidOperationException("订阅返回重定向；请使用服务商提供的最终 HTTPS 地址。");
                if (response.StatusCode != HttpStatusCode.OK)
                    throw new InvalidOperationException($"订阅读取失败，HTTP 状态 {(int)response.StatusCode}。" +
                        (response.StatusCode == HttpStatusCode.Forbidden ? "服务商拒绝了请求，请核对链接是否有效及订阅访问限制。" : ""));
                if (response.Content.Headers.ContentLength > MaxBytes)
                    throw new InvalidOperationException("订阅超过 8 MiB。");
                await using var stream = await response.Content.ReadAsStreamAsync(deadline.Token);
                using var memory = new MemoryStream();
                var buffer = new byte[8192];
                int read;
                while ((read = await stream.ReadAsync(buffer, deadline.Token)) > 0)
                {
                    if (memory.Length + read > MaxBytes)
                        throw new InvalidOperationException("订阅超过 8 MiB。");
                    await memory.WriteAsync(buffer.AsMemory(0, read), deadline.Token);
                }
                return new Result(memory.ToArray(),
                    SubscriptionProfile.FromResponse(address, response.Headers, response.Content.Headers), route.Name);
            }
            catch (HttpRequestException ex)
            {
                failures.Add(route.Name + "：" + (diagnostic.Rejected ? diagnostic.Detail : Describe(ex)));
                if (diagnostic.Rejected) break;
                if (ex.HttpRequestError == HttpRequestError.SecureConnectionError && compatible is not null)
                {
                    try
                    {
                        var reply = await compatible(address, route, deadline.Token);
                        if (reply.Failure.Length == 0)
                        {
                            if (reply.Content.Length > MaxBytes)
                            {
                                failures.Add(route.Name + "（兼容 TLS）：订阅超过 8 MiB");
                                break;
                            }
                            using var metadata = new HttpResponseMessage();
                            metadata.Headers.TryAddWithoutValidation("Subscription-Userinfo", reply.UserInfo);
                            metadata.Content = new ByteArrayContent([]);
                            metadata.Content.Headers.TryAddWithoutValidation("Content-Disposition", reply.Disposition);
                            return new Result(reply.Content, SubscriptionProfile.FromResponse(address,
                                metadata.Headers, metadata.Content.Headers), route.Name + " · 兼容 TLS");
                        }
                        failures.Add(route.Name + "（兼容 TLS）：" + DescribeCompatibility(reply));
                        if (reply.Failure.StartsWith("certificate-", StringComparison.Ordinal) ||
                            reply.Failure is "http" or "size" or "address" or "encoding") break;
                    }
                    catch (OperationCanceledException)
                    {
                        failures.Add(route.Name + "（兼容 TLS）：连接或下载超时");
                    }
                    catch (Exception failure) when (failure is InvalidOperationException or IOException or JsonException or HttpRequestException)
                    {
                        failures.Add(route.Name + "（兼容 TLS）：下载引擎不可用，请确认安装完整");
                    }
                }
            }
            catch (OperationCanceledException)
            {
                failures.Add(route.Name + "：连接或下载超时");
            }
            catch (IOException)
            {
                failures.Add(route.Name + "：下载过程中连接中断");
            }
        }
        throw new InvalidOperationException("订阅读取失败。\n" + string.Join("\n", failures) +
            "\n可导入已下载的 YAML 文件；仍失败时请提供此提示的完整截图。");
    }

    private static async Task<CompatibilityReply> FetchCompatibleAsync(string address, Route route, CancellationToken token)
    {
        Uri? proxy = route.Proxy;
        if (route.UseProxy && proxy is null)
        {
            var systemProxy = HttpClient.DefaultProxy;
            var target = new Uri(address);
            if (!systemProxy.IsBypassed(target)) proxy = systemProxy.GetProxy(target);
            if (proxy == target) proxy = null;
        }
        var data = await EngineClient.CallAsync(new
        {
            command = "fetch-subscription", fetch = new { url = address, proxy = proxy?.AbsoluteUri ?? "" },
        }, token);
        return data.Deserialize<CompatibilityReply>(EngineClient.JsonOptions)
            ?? new CompatibilityReply { Failure = "engine" };
    }

    private static string DescribeCompatibility(CompatibilityReply reply) => reply.Failure switch
    {
        "certificate-trust" => "证书链不受这台 Windows 信任（可能缺少根或中间证书）",
        "certificate-name" => "证书与订阅域名不符",
        "certificate-time" => "证书已过期或尚未生效",
        "certificate-invalid" => "证书校验失败",
        "timeout" => "连接或下载超时",
        "dns" => "DNS 无法解析订阅域名",
        "http" => reply.Status is >= 300 and < 400 ? "订阅返回重定向，请使用最终 HTTPS 地址" :
            $"HTTP 状态 {reply.Status}",
        "size" => "订阅超过 8 MiB",
        "encoding" => "服务器返回了未协商的压缩格式",
        "proxy" => "代理地址不支持兼容连接",
        "address" => "请输入有效的 HTTPS 订阅地址",
        _ => "连接或 TLS 握手失败",
    };

    private static string CertificateFailure(SslPolicyErrors errors, X509ChainStatusFlags flags)
    {
        var reason = (errors & SslPolicyErrors.RemoteCertificateNameMismatch) != 0 ? "证书与订阅域名不符" :
            (errors & SslPolicyErrors.RemoteCertificateNotAvailable) != 0 ? "服务器未提供证书" :
            (flags & X509ChainStatusFlags.Revoked) != 0 ? "证书已被吊销" :
            (flags & X509ChainStatusFlags.NotTimeValid) != 0 ? "证书已过期或尚未生效" :
            (flags & X509ChainStatusFlags.UntrustedRoot) != 0 ? "证书根不受这台 Windows 信任" :
            (flags & X509ChainStatusFlags.PartialChain) != 0 ? "证书链不完整（缺少根或中间证书）" :
            "证书校验失败";
        return reason + $"（{errors}; {flags}）";
    }

    internal static string Describe(HttpRequestException error) => error.HttpRequestError switch
    {
        HttpRequestError.NameResolutionError => "DNS 无法解析订阅域名",
        HttpRequestError.ProxyTunnelError => "系统代理或代理隧道连接失败",
        HttpRequestError.SecureConnectionError => "TLS 握手失败" + NativeCode(error),
        HttpRequestError.ConnectionError => "无法连接订阅服务器或代理",
        HttpRequestError.HttpProtocolError or HttpRequestError.InvalidResponse => "服务器或代理返回了无效响应",
        HttpRequestError.ResponseEnded => "下载尚未完成，服务器已断开连接",
        _ => "网络请求失败（" + error.HttpRequestError + "）",
    };

    private static string NativeCode(Exception error)
    {
        for (Exception? current = error; current is not null; current = current.InnerException)
            if (current is Win32Exception native) return $"（0x{unchecked((uint)native.NativeErrorCode):X8}）";
        return "";
    }

    private sealed class FixtureHandler(Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> send)
        : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken token) =>
            send(request, token);
    }

    private sealed class UnknownLengthStream(byte[] content) : MemoryStream(content)
    {
        public override bool CanSeek => false;
        public override long Length => throw new NotSupportedException();
    }

    internal static async Task VerifyOfflineAsync()
    {
        const string url = "https://example.invalid/sub?token=OFFLINE-SECRET";
        var routes = Routes("127.0.0.1:7897");
        if (routes.Count != 3 || Routes("203.0.113.1:7897").Count != 2 ||
            Routes("127.0.0.1:7897@203.0.113.1").Count != 2)
            throw new InvalidOperationException("订阅备用代理没有限制为回环地址。");
        using (var handler = CreateHandler(routes[2], new TlsDiagnostic()))
            if (handler.AllowAutoRedirect || handler.ServerCertificateCustomValidationCallback is null ||
                !handler.UseProxy || handler.Proxy?.GetProxy(new Uri(url))?.Scheme != "socks5" ||
                handler.AutomaticDecompression != (DecompressionMethods.GZip | DecompressionMethods.Deflate | DecompressionMethods.Brotli))
                throw new InvalidOperationException("订阅 HTTPS、压缩或代理保护配置不正确。");
        int attempts = 0;
        var result = await FetchAsync(url, routes, (route, _) => new FixtureHandler((request, token) =>
        {
            attempts++;
            if (request.RequestUri?.AbsoluteUri != url || !request.Headers.UserAgent.ToString().Contains("ClashMeta"))
                throw new InvalidOperationException("订阅请求地址或客户端标识已变化。");
            if (route.Name != "本机 Clash 代理")
                throw new HttpRequestException(HttpRequestError.ConnectionError, "OFFLINE-SECRET");
            var response = new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("proxies: []") };
            response.Headers.TryAddWithoutValidation("Subscription-Userinfo", "upload=1; download=2; total=10");
            return Task.FromResult(response);
        }), TimeSpan.FromSeconds(1));
        if (attempts != 3 || result.Profile.Total != 10 || result.Content.Length == 0)
            throw new InvalidOperationException("订阅备用连接或用量保留验证失败。");

        async Task MustFail(Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> send,
            string expected, int expectedAttempts, TimeSpan? timeout = null)
        {
            int count = 0;
            try
            {
                await FetchAsync(url, routes, (_, _) => { count++; return new FixtureHandler(send); }, timeout ?? TimeSpan.FromSeconds(2));
            }
            catch (InvalidOperationException ex) when (ex.Message.Contains(expected) && !ex.Message.Contains("OFFLINE-SECRET"))
            {
                if (count == expectedAttempts) return;
            }
            throw new InvalidOperationException($"订阅失败诊断或重试边界验证失败：{expected}，尝试 {count}/{expectedAttempts}。");
        }
        await MustFail((_, _) => throw new HttpRequestException(HttpRequestError.SecureConnectionError, url),
            "TLS", 3);
        await MustFail((_, _) => Task.FromResult(new HttpResponseMessage(HttpStatusCode.Forbidden)),
            "403", 1);
        await MustFail((_, _) => Task.FromResult(new HttpResponseMessage(HttpStatusCode.Redirect)),
            "重定向", 1);
        await MustFail((_, _) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("") };
            response.Content.Headers.ContentLength = MaxBytes + 1;
            return Task.FromResult(response);
        }, "8 MiB", 1);
        await MustFail((_, _) => Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StreamContent(new UnknownLengthStream(new byte[MaxBytes + 1])),
        }), "8 MiB", 1);
        await MustFail((_, _) => throw new HttpRequestException(HttpRequestError.NameResolutionError, url),
            "DNS", 3);
        await MustFail((_, _) => throw new IOException(url), "连接中断", 3);
        await MustFail(async (_, token) =>
        {
            await Task.Delay(Timeout.Infinite, token);
            return new HttpResponseMessage();
        }, "超时", 3, TimeSpan.FromMilliseconds(20));

        // Simulate a platform TLS failure while retaining strict validation in both clients.
        int platformAttempts = 0, compatibleAttempts = 0;
        var tlsResult = await FetchAsync(url, routes, (_, _) => new FixtureHandler((_, _) =>
        {
            platformAttempts++;
            throw new HttpRequestException(HttpRequestError.SecureConnectionError, url,
                new Win32Exception(unchecked((int)0x80090331)));
        }), TimeSpan.FromSeconds(2), (address, route, token) =>
        {
            compatibleAttempts++;
            if (address != url || route.Name != "系统连接" || !token.CanBeCanceled)
                throw new InvalidOperationException("兼容下载没有保留连接或时限。");
            return Task.FromResult(new CompatibilityReply
            {
                Content = System.Text.Encoding.UTF8.GetBytes("proxies: []"),
                UserInfo = "upload=1; download=2; total=10",
                Disposition = "attachment; filename=Game.yaml",
            });
        });
        if (platformAttempts != 1 || compatibleAttempts != 1 || tlsResult.Profile.Total != 10 ||
            tlsResult.Profile.Name != "Game.yaml" || !tlsResult.Route.Contains("兼容 TLS"))
            throw new InvalidOperationException("兼容 TLS 备用下载或订阅元数据验证失败。");
        foreach (var flags in new[] { X509ChainStatusFlags.UntrustedRoot, X509ChainStatusFlags.PartialChain,
            X509ChainStatusFlags.NotTimeValid, X509ChainStatusFlags.Revoked })
        {
            var d = new TlsDiagnostic();
            if (d.Validate(SslPolicyErrors.RemoteCertificateChainErrors, flags) || !d.Rejected)
                throw new InvalidOperationException("不应接受无效证书链。");
        }
        foreach (var policy in new[] { SslPolicyErrors.RemoteCertificateNameMismatch, SslPolicyErrors.RemoteCertificateNotAvailable })
            if (new TlsDiagnostic().Validate(policy, X509ChainStatusFlags.NoError))
                throw new InvalidOperationException("不应接受域名不符或缺失的证书。");
        using (var strict = CreateHandler(routes[0], new TlsDiagnostic()))
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, url);
            if (strict.ServerCertificateCustomValidationCallback!(request, null, null, SslPolicyErrors.RemoteCertificateNameMismatch) ||
                !strict.ServerCertificateCustomValidationCallback(request, null, null, SslPolicyErrors.None))
                throw new InvalidOperationException("订阅诊断回调改变了证书校验策略。");
        }
        bool certificateRefused = false;
        platformAttempts = compatibleAttempts = 0;
        try
        {
            await FetchAsync(url, routes, (_, diagnostic) => new FixtureHandler((_, _) =>
            {
                platformAttempts++;
                diagnostic.Validate(SslPolicyErrors.RemoteCertificateChainErrors, X509ChainStatusFlags.UntrustedRoot);
                throw new HttpRequestException(HttpRequestError.SecureConnectionError, url);
            }), TimeSpan.FromSeconds(1), (_, _, _) =>
            {
                compatibleAttempts++;
                return Task.FromResult(new CompatibilityReply());
            });
        }
        catch (InvalidOperationException ex) when (ex.Message.Contains("UntrustedRoot") && !ex.Message.Contains("OFFLINE-SECRET"))
        {
            certificateRefused = platformAttempts == 1 && compatibleAttempts == 0;
        }
        if (!certificateRefused) throw new InvalidOperationException("证书拒绝不应触发兼容尝试。");

        foreach (var code in new[] { "certificate-trust", "certificate-name", "certificate-time", "http", "size" })
        {
            platformAttempts = compatibleAttempts = 0;
            bool safelyRefused = false;
            try
            {
                await FetchAsync(url, routes, (_, _) => new FixtureHandler((_, _) =>
                {
                    platformAttempts++;
                    throw new HttpRequestException(HttpRequestError.SecureConnectionError, url,
                        new Win32Exception(unchecked((int)0x80090331)));
                }), TimeSpan.FromSeconds(1), (_, _, _) =>
                {
                    compatibleAttempts++;
                    return Task.FromResult(new CompatibilityReply { Failure = code, Status = 403 });
                });
            }
            catch (InvalidOperationException ex) when (!ex.Message.Contains("OFFLINE-SECRET") &&
                ex.Message.Contains("兼容 TLS") && ex.Message.Contains("0x80090331"))
            {
                safelyRefused = platformAttempts == 1 && compatibleAttempts == 1;
            }
            if (!safelyRefused) throw new InvalidOperationException("兼容下载拒绝或凭据保护验证失败。");
        }
        platformAttempts = compatibleAttempts = 0;
        bool deadlineRefused = false;
        try
        {
            await FetchAsync(url, routes, (_, _) => new FixtureHandler((_, _) =>
            {
                platformAttempts++;
                throw new HttpRequestException(HttpRequestError.SecureConnectionError, url);
            }), TimeSpan.FromMilliseconds(20), async (_, _, token) =>
            {
                compatibleAttempts++;
                await Task.Delay(Timeout.Infinite, token);
                return new CompatibilityReply();
            });
        }
        catch (InvalidOperationException ex) when (ex.Message.Contains("超时") && !ex.Message.Contains("OFFLINE-SECRET"))
        {
            deadlineRefused = platformAttempts == 3 && compatibleAttempts == 3;
        }
        if (!deadlineRefused) throw new InvalidOperationException("兼容下载必须使用同一连接时限。");
        var ipc = await EngineClient.CallAsync(new
        {
            command = "fetch-subscription", fetch = new { url = "http://example.invalid/sub?token=OFFLINE-SECRET" },
        });
        if (ipc.GetProperty("failure").GetString() != "address" || ipc.GetRawText().Contains("OFFLINE-SECRET"))
            throw new InvalidOperationException("订阅兼容下载引擎通信或地址校验失败。");
    }
}

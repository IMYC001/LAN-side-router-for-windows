using System.IO;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Accelerator.App;

internal static class PassiveProof
{
    public static DateTime Read(string directory, AdapterInfo adapter, string deviceIP, string deviceMAC)
    {
        directory = Path.GetFullPath(directory);
        var resultPath = Path.Combine(directory, "result.json");
        var requestPath = Path.Combine(directory, "request.json");
        using var result = ReadDocument(resultPath);
        using var request = ReadDocument(requestPath);
        var written = File.GetLastWriteTimeUtc(resultPath);
        Validate(result.RootElement, request.RootElement, adapter, deviceIP, deviceMAC, written, DateTime.UtcNow);
        return written;
    }

    private static JsonDocument ReadDocument(string path)
    {
        using var stream = File.OpenRead(path);
        if (stream.Length > 65536)
            throw new InvalidOperationException("连接检查记录过大。");
        return JsonDocument.Parse(stream);
    }

    private static string MAC(string value) => value.Replace('-', ':').ToLowerInvariant();

    internal static void Validate(JsonElement result, JsonElement request, AdapterInfo adapter,
        string deviceIP, string deviceMAC, DateTime written, DateTime now)
    {
        var status = result.GetProperty("status").Deserialize<GatewayStatus>(EngineClient.JsonOptions)
            ?? throw new InvalidOperationException("连接检查缺少状态。");
        var plan = request.GetProperty("plan");
        var expected = request.GetProperty("expected");
        bool Same(string key, string value) =>
            string.Equals(expected.GetProperty(key).GetString(), value, StringComparison.OrdinalIgnoreCase);
        if (now - written > TimeSpan.FromMinutes(10) || written > now ||
            !result.GetProperty("workerExited").GetBoolean() ||
            result.GetProperty("exitCode").GetInt32() != 0 ||
            !result.GetProperty("physicalAdapterUnchanged").GetBoolean() ||
            request.GetProperty("mode").GetString() != "passive" ||
            !request.GetProperty("confirmed").GetBoolean() ||
            request.GetProperty("seconds").GetInt32() is < 1 or > 20 ||
            !status.Stopped || !status.PassivePassed || status.Phase != "complete" ||
            (status.Received == 0 && status.IdentityMethod != "windows-arp") ||
            status.Sent != 0 || status.Forwarded != 0 ||
            status.Stats.TCPStarted != 0 || status.Stats.UDPStarted != 0 || status.Stats.UDPAssociations != 0 ||
            status.Stats.Uploaded != 0 || status.Stats.Downloaded != 0 || status.Stats.Failed != 0 ||
            status.DeviceIP != deviceIP || MAC(status.DeviceMAC) != MAC(deviceMAC) ||
            plan.GetProperty("deviceIP").GetString() != deviceIP ||
            MAC(plan.GetProperty("deviceMAC").GetString() ?? "") != MAC(deviceMAC) ||
            plan.GetProperty("adapterIndex").GetInt32() != adapter.Index ||
            !Same("adapterGUID", adapter.GUID) || !Same("adapterIP", adapter.IPv4) ||
            MAC(expected.GetProperty("adapterMAC").GetString() ?? "") != MAC(adapter.MAC) ||
            expected.GetProperty("adapterPrefix").GetInt32() != adapter.Prefix ||
            !Same("router", adapter.Gateway) || !adapter.Physical || adapter.Status != "Up" ||
            adapter.Forwarding)
            throw new InvalidOperationException("连接检查已过期、身份不匹配或网络已变化；未启动网关。");
    }

    internal static void VerifyOffline()
    {
        var adapter = new AdapterInfo
        {
            Index = 11, GUID = "{offline}", IPv4 = "192.168.1.20", Prefix = 24,
            MAC = "02-11-22-33-44-55", Gateway = "192.168.1.1", Physical = true, Status = "Up",
        };
        const string deviceIP = "192.168.1.35";
        const string deviceMAC = "02-11-22-33-44-66";
        var result = JsonSerializer.SerializeToNode(new
        {
            workerExited = true, exitCode = 0, physicalAdapterUnchanged = true,
            status = new GatewayStatus
            {
                Phase = "complete", Stopped = true, PassivePassed = true,
                DeviceIP = deviceIP, DeviceMAC = deviceMAC, Received = 2,
            },
        }, EngineClient.JsonOptions)!.AsObject();
        var request = JsonSerializer.SerializeToNode(new
        {
            mode = "passive", confirmed = true, seconds = 20,
            plan = new { deviceIP, deviceMAC, adapterIndex = 11 },
            expected = new
            {
                adapterGUID = adapter.GUID, adapterIP = adapter.IPv4, adapterMAC = adapter.MAC,
                adapterPrefix = adapter.Prefix, router = adapter.Gateway,
            },
        })!.AsObject();
        var now = DateTime.UtcNow;
        void Check(JsonObject record, JsonObject captured, DateTime written) =>
            Validate(JsonSerializer.SerializeToElement(record), JsonSerializer.SerializeToElement(captured),
                adapter, deviceIP, deviceMAC, written, now);
        Check(result, request, now.AddSeconds(-1));
        var arpOnly = result.DeepClone().AsObject();
        arpOnly["status"]!["received"] = 0;
        arpOnly["status"]!["identityMethod"] = "windows-arp";
        Check(arpOnly, request, now.AddSeconds(-1));
        foreach (var written in new[] { now.AddMinutes(-11), now.AddSeconds(1) })
        {
            try { Check(result, request, written); }
            catch (InvalidOperationException) { continue; }
            throw new InvalidOperationException("连接记录的时间检查失败。");
        }
        Action<JsonObject, JsonObject>[] mutations =
        [
            (r, q) => r["workerExited"] = false,
            (r, q) => r["exitCode"] = 1,
            (r, q) => r["physicalAdapterUnchanged"] = false,
            (r, q) => r["status"]!["stopped"] = false,
            (r, q) => r["status"]!["passivePassed"] = false,
            (r, q) => r["status"]!["received"] = 0,
            (r, q) => r["status"]!["sent"] = 1,
            (r, q) => r["status"]!["forwarded"] = 1,
            (r, q) => r["status"]!["deviceMAC"] = "02:11:22:33:44:77",
            (r, q) => r["status"]!["deviceIP"] = "192.168.1.36",
            (r, q) => r["status"]!["stats"]!["uploaded"] = 1,
            (r, q) => q["mode"] = "gateway",
            (r, q) => q["confirmed"] = false,
            (r, q) => q["seconds"] = 21,
            (r, q) => q["plan"]!["adapterIndex"] = 12,
            (r, q) => q["plan"]!["deviceMAC"] = "02:11:22:33:44:77",
            (r, q) => q["expected"]!["adapterGUID"] = "{changed}",
            (r, q) => q["expected"]!["adapterIP"] = "192.168.1.21",
            (r, q) => q["expected"]!["adapterMAC"] = "02:11:22:33:44:77",
            (r, q) => q["expected"]!["adapterPrefix"] = 25,
            (r, q) => q["expected"]!["router"] = "192.168.1.2",
        ];
        foreach (var mutate in mutations)
        {
            var changedResult = result.DeepClone().AsObject();
            var changedRequest = request.DeepClone().AsObject();
            mutate(changedResult, changedRequest);
            try { Check(changedResult, changedRequest, now.AddSeconds(-1)); }
            catch (InvalidOperationException) { continue; }
            throw new InvalidOperationException("失效的连接记录被接受。");
        }
    }
}

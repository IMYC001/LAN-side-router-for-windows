using System.IO;
using System.IO.Compression;
using System.Text.Json;

namespace Accelerator.App;

internal static class DiagnosticsExport
{
    private static readonly JsonSerializerOptions OutputOptions = new() { WriteIndented = true };

    public static void Write(string path, string? sessionDirectory, NetworkSnapshot? snapshot)
    {
        if (sessionDirectory is null)
        {
            var sessions = Path.Combine(ProfileStore.Root, "sessions");
            if (Directory.Exists(sessions))
                sessionDirectory = new DirectoryInfo(sessions).EnumerateDirectories()
                    .Where(d => Guid.TryParseExact(d.Name, "N", out _) && File.Exists(Path.Combine(d.FullName, "performance.jsonl")))
                    .OrderByDescending(d => d.LastWriteTimeUtc).FirstOrDefault()?.FullName;
        }
        var temporary = path + ".tmp-" + Guid.NewGuid().ToString("N");
        try
        {
            using (var file = new FileStream(temporary, FileMode.CreateNew))
            using (var zip = new ZipArchive(file, ZipArchiveMode.Create))
            {
                Add(zip, "summary.json", new
                {
                    version = "0.5.13", generated = DateTime.UtcNow,
                    scope = "IPv4 gateway diagnostics. Includes local device addresses and counters. No subscription, credentials, configuration, logs or payloads.",
                    knownIssue = "Multi-device throughput has not yet been validated on physical devices.",
                    network = snapshot,
                });
                if (sessionDirectory is not null)
                {
                    var statusPath = Path.Combine(sessionDirectory, "status.json");
                    if (File.Exists(statusPath))
                    {
                        using var status = OpenRead(statusPath);
                        if (status.Length > 65536) throw new IOException("Status exceeds diagnostic limit.");
                        Add(zip, "status.json", JsonSerializer.Deserialize<GatewayStatus>(status, EngineClient.JsonOptions));
                    }
                    var tracePath = Path.Combine(sessionDirectory, "performance.jsonl");
                    if (File.Exists(tracePath))
                    {
                        var samples = new Queue<JsonElement>();
                        using var reader = new StreamReader(OpenRead(tracePath));
                        string? line;
                        while ((line = reader.ReadLine()) is not null)
                        {
                            if (line.Length > 65536) continue;
                            try
                            {
                                using var document = JsonDocument.Parse(line);
                                var root = document.RootElement;
                                // Re-encode only the known counters, never copy raw session files.
                                var sample = JsonSerializer.SerializeToElement(new
                                {
                                    time = root.GetProperty("time").GetDateTime(),
                                    status = root.GetProperty("status").Deserialize<GatewayStatus>(EngineClient.JsonOptions),
                                });
                                samples.Enqueue(sample);
                                if (samples.Count > 7200) samples.Dequeue();
                            }
                            catch (Exception ex) when (ex is JsonException or KeyNotFoundException or FormatException or InvalidOperationException) { }
                        }
                        Add(zip, "performance.json", samples.ToArray());
                    }
                }
            }
            File.Move(temporary, path, true);
        }
        finally { if (File.Exists(temporary)) File.Delete(temporary); }
    }

    private static FileStream OpenRead(string path) => new(path, FileMode.Open, FileAccess.Read,
        FileShare.ReadWrite | FileShare.Delete);

    private static void Add(ZipArchive archive, string name, object? content)
    {
        using var entry = archive.CreateEntry(name).Open();
        JsonSerializer.Serialize(entry, content, OutputOptions);
    }

    internal static void VerifyOffline()
    {
        var directory = Path.Combine(Path.GetTempPath(), "PS5-diagnostics-test-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        var output = Path.Combine(directory, "diagnostics.zip");
        try
        {
            File.WriteAllText(Path.Combine(directory, "request.json"), "{\"secret\":\"OFFLINE-SECRET\"}");
            File.WriteAllText(Path.Combine(directory, "config.yaml"), "password: OFFLINE-SECRET");
            File.WriteAllText(Path.Combine(directory, "status.json"),
                "{\"phase\":\"stopped\",\"stopped\":true,\"secret\":\"OFFLINE-SECRET\",\"stats\":{\"uploaded\":42}}");
            File.WriteAllText(Path.Combine(directory, "performance.jsonl"),
                "{\"time\":\"2026-10-14T00:00:00Z\",\"secret\":\"OFFLINE-SECRET\",\"status\":{\"stats\":{\"downloaded\":84},\"secret\":\"OFFLINE-SECRET\"}}\n{incomplete");
            Write(output, directory, null);
            using var archive = ZipFile.OpenRead(output);
            if (archive.Entries.Count != 3) throw new InvalidOperationException("诊断文件白名单验证失败。");
            foreach (var entry in archive.Entries)
            {
                using var reader = new StreamReader(entry.Open());
                if (reader.ReadToEnd().Contains("OFFLINE-SECRET"))
                    throw new InvalidOperationException("诊断导出泄露原始配置。");
            }
        }
        finally
        {
            foreach (var name in new[] { "request.json", "config.yaml", "status.json", "performance.jsonl", "diagnostics.zip" })
                File.Delete(Path.Combine(directory, name));
            Directory.Delete(directory);
        }
    }
}

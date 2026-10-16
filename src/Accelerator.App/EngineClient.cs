using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.Json;

namespace Accelerator.App;

internal static class EngineClient
{
    public static readonly JsonSerializerOptions JsonOptions = new() { PropertyNameCaseInsensitive = true };

    public static async Task<JsonElement> CallAsync(object request, CancellationToken cancellation = default)
    {
        var path = Path.Combine(AppContext.BaseDirectory, "accelerator-engine.exe");
        if (!File.Exists(path))
            throw new InvalidOperationException("找不到转发引擎。请从完整发布目录运行。");
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellation);
        timeout.CancelAfter(TimeSpan.FromSeconds(45));
        using var process = new Process
        {
            StartInfo = new ProcessStartInfo(path)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                StandardInputEncoding = new UTF8Encoding(false),
                StandardOutputEncoding = Encoding.UTF8,
                StandardErrorEncoding = Encoding.UTF8,
            }
        };
        var started = false;
        try
        {
            process.Start();
            started = true;
            var stdout = process.StandardOutput.ReadToEndAsync(timeout.Token);
            var stderr = process.StandardError.ReadToEndAsync(timeout.Token);
            await process.StandardInput.WriteLineAsync(JsonSerializer.Serialize(request).AsMemory(), timeout.Token);
            process.StandardInput.Close();
            await process.WaitForExitAsync(timeout.Token);
            var output = await stdout;
            await stderr;
            if (process.ExitCode != 0)
                throw new InvalidOperationException("引擎退出异常。请检查发布目录是否完整。");
            using var result = JsonDocument.Parse(output);
            if (!result.RootElement.GetProperty("ok").GetBoolean())
                throw new InvalidOperationException(result.RootElement.GetProperty("error").GetString());
            return result.RootElement.GetProperty("data").Clone();
        }
        finally
        {
            if (started && !process.HasExited)
                process.Kill(entireProcessTree: true);
        }
    }
}

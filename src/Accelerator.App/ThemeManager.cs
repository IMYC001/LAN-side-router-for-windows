using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media;

namespace Accelerator.App;

internal static class ThemeManager
{
    public static string Current { get; private set; } = "Dark";
    public static string Accent { get; private set; } = "Green";
    internal static readonly string[] Accents = ["Green", "Blue", "LightSalmon", "Violet", "Rose"];

    public static void Apply(string theme, Window window, string? accent = null)
    {
        Current = theme == "Dark" ? "Dark" : "Light";
        if (accent is not null) Accent = Accents.Contains(accent) ? accent : "Green";
        bool dark = Current == "Dark";
        var colors = new (string Key, string Light, string Dark)[]
        {
            ("TextBrush", "#202829", "#EDEEF0"),
            ("MutedBrush", "#657375", "#B3B6BD"),
            ("SubtleBrush", "#798685", "#9C9FA6"),
            ("LineBrush", "#E3E8E9", "#3E4148"),
            ("WindowBrush", "#FFFFFF", "#222429"),
            ("SidebarBrush", "#F4F6F6", "#1B1D21"),
            ("FieldBrush", "#F5F7F8", "#2C2F35"),
            ("HoverBrush", "#E9EEEE", "#363940"),
            ("BlueBrush", "#2D64B3", "#8BB8F3"),
            ("ErrorBrush", "#A65022", "#F1AA83"),
        };
        foreach (var entry in colors)
            Application.Current.Resources[entry.Key] = new SolidColorBrush(
                (Color)ColorConverter.ConvertFromString(dark ? entry.Dark : entry.Light));
        string hex = Accent switch
        {
            "Blue" => dark ? "#8BB8F3" : "#2863AD",
            "LightSalmon" => "#FFA07A",
            "Violet" => dark ? "#C5A8F2" : "#7651AB",
            "Rose" => dark ? "#F298B5" : "#AF3D68",
            _ => dark ? "#62D2B4" : "#117766",
        };
        var color = (Color)ColorConverter.ConvertFromString(hex);
        Set("AccentBrush", color);
        // The salmon swatch stays LightSalmon; text uses a darker shade in light mode.
        Set("AccentTextBrush", (Color)ColorConverter.ConvertFromString(!dark && Accent == "LightSalmon" ? "#A3482A" : hex));
        Set("OnAccentBrush", (Color)ColorConverter.ConvertFromString(dark || Accent == "LightSalmon" ? "#202429" : "#FFFFFF"));
        Color background = (Color)ColorConverter.ConvertFromString(dark ? "#222429" : "#FFFFFF");
        Color Blend(double amount) => Color.FromRgb(
            (byte)(background.R + (color.R - background.R) * amount),
            (byte)(background.G + (color.G - background.G) * amount),
            (byte)(background.B + (color.B - background.B) * amount));
        Set("SoftBrush", Blend(dark ? .17 : .13));
        Set("SelectedBrush", Blend(dark ? .12 : .07));
        var handle = new WindowInteropHelper(window).Handle;
        if (handle != IntPtr.Zero)
        {
            int value = dark ? 1 : 0;
            _ = DwmSetWindowAttribute(handle, 20, ref value, sizeof(int));
        }
    }

    private static void Set(string key, Color color) =>
        Application.Current.Resources[key] = new SolidColorBrush(color);

    [DllImport("dwmapi.dll")]
    private static extern int DwmSetWindowAttribute(IntPtr window, int attribute, ref int value, int size);
}

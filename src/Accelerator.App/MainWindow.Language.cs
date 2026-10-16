using System.Diagnostics;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media.Imaging;
using System.Windows.Navigation;

namespace Accelerator.App;

public partial class MainWindow
{
    private bool changingLanguage;

    private void LoadAboutIcon()
    {
        using var stream = typeof(MainWindow).Assembly.GetManifestResourceStream("Accelerator.Icon")
            ?? throw new InvalidOperationException("软件图标缺失。");
        var decoder = new IconBitmapDecoder(stream, BitmapCreateOptions.PreservePixelFormat, BitmapCacheOption.OnLoad);
        AboutIcon.Source = decoder.Frames.OrderByDescending(frame => frame.PixelWidth).First();
    }

    private void ApplyLanguage(string language)
    {
        changingLanguage = true;
        UiText.Apply(language);
        LanguageBox.SelectedValue = UiText.Instance.Language;
        tray?.RefreshLanguage();
        // Refresh only subscription presentation. Device views and all session
        // controls retain their bindings and live state.
        RefreshSubscriptionCards();
        changingLanguage = false;
    }

    private void Language_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (changingLanguage || restoringProfile || LanguageBox.SelectedValue is not string language) return;
        ApplyLanguage(language);
        SaveProfile();
    }

    private void ProjectLink_RequestNavigate(object sender, RequestNavigateEventArgs e)
    {
        e.Handled = true;
        const string project = "https://github.com/IMYC001/LAN-side-router-for-windows";
        if (e.Uri.AbsoluteUri != project) return;
        try { Process.Start(new ProcessStartInfo(project) { UseShellExecute = true }); }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            UiText.Set(StatusText, UiText.IsEnglish ? "Could not open the browser." : "无法打开默认浏览器。");
        }
    }
}

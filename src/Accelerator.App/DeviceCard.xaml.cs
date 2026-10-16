using System.Windows;
using System.Windows.Controls;

namespace Accelerator.App;

public partial class DeviceCard : UserControl
{
    public DeviceCard() => InitializeComponent();
    private MainWindow? Owner => Window.GetWindow(this) as MainWindow;
    private void Toggle_Click(object sender, RoutedEventArgs e) => Owner?.ToggleDeviceGateway_Click(sender, e);
    private void Parameters_Click(object sender, RoutedEventArgs e) => Owner?.ToggleDeviceParameters_Click(sender, e);
    private void Copy_Click(object sender, RoutedEventArgs e) => Owner?.CopyDeviceParameters_Click(sender, e);
    private void Node_Changed(object sender, SelectionChangedEventArgs e) => Owner?.DeviceNode_Changed(sender, e);
    private void Settings_Click(object sender, RoutedEventArgs e) => Owner?.ProxySettings_Click(sender, e);
}

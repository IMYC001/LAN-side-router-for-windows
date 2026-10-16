$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName PresentationCore, WindowsBase
$assetRoot = Join-Path ([IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))) 'src\Accelerator.App\Assets'
$frames = @()
foreach ($size in @(16,20,24,32,48,64,128,256)) {
    $visual = [Windows.Media.DrawingVisual]::new()
    $context = $visual.RenderOpen()
    $context.PushTransform([Windows.Media.ScaleTransform]::new($size / 256.0, $size / 256.0))
    $background = [Windows.Media.BrushConverter]::new().ConvertFromString('#117766')
    $mint = [Windows.Media.BrushConverter]::new().ConvertFromString('#8DE2C4')
    $context.DrawRoundedRectangle($background, $null, [Windows.Rect]::new(8,8,240,240), 48,48)
    $whitePen = [Windows.Media.Pen]::new([Windows.Media.Brushes]::White,20)
    $mintPen = [Windows.Media.Pen]::new($mint,14)
    foreach ($pen in @($whitePen, $mintPen)) {
        $pen.StartLineCap = 'Round'
        $pen.EndLineCap = 'Round'
        $pen.LineJoin = 'Round'
    }
    $context.DrawGeometry($null, $whitePen, [Windows.Media.Geometry]::Parse('M56,144 L96,144 128,96 176,96'))
    $context.DrawGeometry($null, $whitePen, [Windows.Media.Geometry]::Parse('M151,69 L181,96 151,123'))
    $context.DrawGeometry($null, $mintPen, [Windows.Media.Geometry]::Parse('M104,184 L152,184 184,144 200,144'))
    $context.Pop()
    $context.Close()
    $bitmap = [Windows.Media.Imaging.RenderTargetBitmap]::new($size,$size,96,96,[Windows.Media.PixelFormats]::Pbgra32)
    $bitmap.Render($visual)
    $encoder = [Windows.Media.Imaging.PngBitmapEncoder]::new()
    $encoder.Frames.Add([Windows.Media.Imaging.BitmapFrame]::Create($bitmap))
    $stream = [IO.MemoryStream]::new()
    $encoder.Save($stream)
    $bytes = $stream.ToArray()
    $stream.Dispose()
    $frames += @{Size=$size; Bytes=$bytes}
    if ($size -eq 256) { [IO.File]::WriteAllBytes((Join-Path $assetRoot 'accelerator.png'), $bytes) }
}
$file = [IO.File]::Create((Join-Path $assetRoot 'accelerator.ico'))
$writer = [IO.BinaryWriter]::new($file)
try {
    $writer.Write([uint16]0)
    $writer.Write([uint16]1)
    $writer.Write([uint16]$frames.Count)
    $offset = 6 + 16 * $frames.Count
    foreach ($frame in $frames) {
        $dimension = if ($frame.Size -eq 256) { 0 } else { $frame.Size }
        $writer.Write([byte]$dimension)
        $writer.Write([byte]$dimension)
        $writer.Write([byte]0)
        $writer.Write([byte]0)
        $writer.Write([uint16]1)
        $writer.Write([uint16]32)
        $writer.Write([uint32]$frame.Bytes.Length)
        $writer.Write([uint32]$offset)
        $offset += $frame.Bytes.Length
    }
    foreach ($frame in $frames) { $writer.Write([byte[]]$frame.Bytes) }
} finally { $writer.Dispose() }

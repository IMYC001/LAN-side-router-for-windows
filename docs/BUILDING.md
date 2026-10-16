# 构建与验证 / Building and Validation

[简体中文首页](../README.md) · [English README](../README.en.md)

## 环境 / Environment

- Windows x64; PowerShell; Git.
- Pinned .NET SDK **10.0.401**, Go **1.27.2**, Mihomo **v1.19.32**.
- Optional setup compiler: Inno Setup **6.7.3**.
- Network access to upstream download endpoints, NuGet and Go modules.

工具下载到仓库 `.tools`，不修改系统 PATH。构建脚本不会启动真实网关或安装网络驱动。
Tools are local to `.tools`; no system PATH change. Building does not start a physical gateway or install a packet driver.

## 源码结构 / Layout

| Path | Content |
| --- | --- |
| `src/Accelerator.App` | WPF UI, localization, subscription persistence, core/session control, driver lifecycle |
| `engine/cmd/accelerator-engine` | Go executable and IPC entry point |
| `engine/internal` | Gateway, packet validation, TCP/UDP stack, SOCKS, DNS, drivers and offline fixtures |
| `installer` | Inno Setup source and setup notice |
| `scripts` | Pinned tool downloads, builds and developer diagnostics |
| `docs/images` | Sanitized offline UI screenshots |
| `docs/releases` | Bilingual release notes |

## 应用 / Application

```powershell
git clone https://github.com/IMYC001/LAN-side-router-for-windows.git
cd LAN-side-router-for-windows
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Get-Tools.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Get-Mihomo.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Build.ps1 -OutputName Accelerator-0.5.13 -SmokeTest
```

Output: `dist\Accelerator-0.5.13\Accelerator.exe`. Keep the entire directory together.

默认执行 Go 测试、`go vet`、Windows 自包含发布；`-SmokeTest` 额外执行桌面离线检查及截图。验证报告位于 `test-results`，不随安装包分发。截图及网络夹具使用测试数据；不会接管真实设备。桌面截图测试需要可用的 Windows 桌面会话。

The default build runs Go tests, `go vet` and self-contained Windows publishing. `-SmokeTest` adds offline desktop checks and screenshots. Reports go to `test-results`. Fixtures use synthetic data and loopback endpoints, not a physical gateway. Desktop rendering checks need a Windows desktop session.

## 安装包 / Setup

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Get-InstallerTools.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Build-Installer.ps1 -OutputName Accelerator-0.5.13 -SkipAppBuild
```

先完成上一步带 `-SmokeTest` 的构建。安装包脚本核对本版二进制、验证报告、原厂驱动签名/哈希及许可后编译。`Get-InstallerTools.ps1` 会在 `.tools` 安装编译器并只读提取 MSI 许可，不安装网络驱动。`-SkipAppBuild` 只跳过已完成的应用构建，不跳过校验。

Complete the smoke-tested app build first. Packaging verifies binary/report hashes, the vendor driver signature/checksum and its license. The tool script installs the compiler locally and reads MSI metadata without installing a network driver. `-SkipAppBuild` keeps all packaging verification.

Output: `dist\Accelerator-0.5.13-Setup-win-x64.exe`.

## 验证范围 / Validation Scope

Offline checks cover protocol framing, TCP/UDP relay, loss/reordering, multi-device state isolation, subscription handling, profile restart, localization, themes and installer ownership fixtures. Synthetic throughput is not a real console benchmark. Real driver installation/removal, LAN compatibility, NAT, long sessions and game performance need separate hardware testing.

离线验证不能替代实机安装、卸载、跨电脑收发、NAT、游戏和长期稳定性验收。不要通过 CI 启动加速、安装驱动或自动改动测试机网络。

Do not run `Repair-Npcap.ps1` as part of a normal build: its opt-in repair path changes the network stack. `Observe-PS5.ps1` is a developer capture tool requiring explicit adapter/device parameters and elevation, not a setup step.

## 对应第三方源码 / Corresponding Third-party Source

`Get-Mihomo.ps1` verifies the fixed upstream binary and source archive. `Build.ps1` packages the source archive under `sources/` and copies dependency licenses. Go dependency versions are recorded in `engine/go.mod` and `engine/go.sum`; runtime and tool downloads are described in the scripts.

Retain `LICENSE`, `THIRD-PARTY-NOTICES.md`, dependency notices and Mihomo corresponding source when redistributing. The optional vendor driver remains under its separate EULA.

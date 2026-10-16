# Contributing / 参与贡献

欢迎通过 Issue 和 Pull Request 改进项目。请先阅读 [构建说明](docs/BUILDING.md) 与 [安全说明](SECURITY.md)。

Contributions are welcome through issues and pull requests. Read [Building](docs/BUILDING.md) and [Security](SECURITY.md) first.

## 问题反馈 / Issues

提供软件版本、Windows 版本、设备型号、连接方式（网线/Wi-Fi）、出口模式及复现步骤。说明是否与 Clash TUN 共存、是否仅某个节点发生问题。避免上传订阅链接、访问令牌、完整 YAML、原始数据包或未经处理的诊断包。

Include the app/Windows version, device model, Ethernet/Wi-Fi, exit mode, reproduction steps and expected/actual behavior. State whether Clash TUN is active and whether the issue affects one node. Do not post credentials, subscription URLs, full YAML, raw packets or unredacted diagnostics.

## 开发 / Development

1. Fork and create a focused branch.
2. Keep changes consistent with the WPF desktop and Go engine architecture.
3. Run checks relevant to your change; use the full offline build for gateway, shared-state or packaging changes.
4. Describe the behavior change, validation and remaining hardware-test limitations.

不要把真实网关测试、驱动安装、路由/DNS 修改或网络修复放进自动测试。实机测试需要操作者明确同意，并先记录设备原设置。保持每台设备的会话、流量与启停隔离，不引入转发热路径上的界面开销。

Do not add physical gateway activation, driver installation, route/DNS changes or network repair to automated tests. Hardware tests need the operator's consent and recorded original settings. Preserve independent device sessions, counters and controls.

提交即表示你有权按项目 GPL-3.0-only 许可贡献相应代码；第三方代码需保留来源及许可。无需签署单独 CLA。

By submitting a contribution, you confirm you may license it under the project's GPL-3.0-only terms. Preserve attribution and licenses for third-party code. No separate CLA is required.

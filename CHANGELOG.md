# Changelog / 更新记录

[简体中文](README.md) · [English](README.en.md)

## 0.5.14 — 2026-10-16

### 简体中文

- 首个正式公开 Release，包含 0.5.13 的全部功能、中英文图文文档和完整源码。
- 修复订阅请求取消或超时时，HTTP 流以正常 EOF 结束而误报成功的边界情况；取消后的内容不会返回。
- 新增请求取消回归测试，并在本机及 GitHub Windows runner 上构建验证。
- 新增手动 Windows 发布构建，仅更新草稿附件，正式发布前核对哈希。

### English

- First published release, including all 0.5.13 features, complete source and bilingual illustrated guides.
- Reject canceled/timed-out subscription bodies even when the HTTP reader reports a clean EOF.
- Added cancellation regression coverage and builds on both the local PC and a GitHub Windows runner.
- Added a manual Windows release build that updates draft assets for verification before publication.

## 0.5.13 — 2026-10-16

Source preparation candidate; superseded by 0.5.14 before publishing a binary release.

### 简体中文

- 软件标题更新为“局域网设备加速（旁路由模式）”。
- 新增居中关于页：高清图标、项目链接、联系方式和鸣谢。
- 设置主题色下方新增简体中文 / English，即时切换并保存；不重启会话或翻译用户命名。
- 支持中文输入法设备命名，添加设备后自动显示加速卡片。
- 更新安装包元数据、项目信息及已修复的速度问题说明。
- 整理发布目录；公开完整源码、构建说明、中英文图文介绍及第三方声明。

### English

- Updated application title and added a centered About page with a high-resolution icon and project link.
- Added persistent Simplified Chinese / English switching below accent colors without restarting sessions.
- Supports Chinese IME device naming; newly added devices appear as acceleration cards automatically.
- Refreshed setup metadata and removed the obsolete unresolved-throughput warning.
- Prepared the complete public source, build instructions, bilingual illustrated README and third-party notices.

## Earlier Milestones / 主要历史更新

| Version | 简体中文 | English |
| --- | --- | --- |
| 0.5.12 | 统一设备卡片、独立启停及状态隔离，保留其他设备连接和统计 | Shared card UI; independent device controls and state isolation |
| 0.5.9–0.5.11 | 多设备参数、节点选择和界面迭代 | Multi-device parameters, node selection and UI iteration |
| 0.5.8 | 直连节点延迟、主题色、深色默认 | Direct node latency, accents and dark default |
| 0.5.7 | 设置整合、深浅主题、图标、节点测试 | Settings, themes, icon and latency tests |
| 0.5.6 | 紧凑侧栏、单一启停按钮、实时速率及累计 | Compact sidebar, combined start/stop, rates and totals |
| 0.5.5 | 转发吞吐修复，用户实测确认有效 | Throughput fix confirmed by the original user |
| 0.5.3–0.5.4 | 订阅下载与 Windows TLS 兼容改进 | Subscription transport and Windows TLS compatibility |
| 0.5.2 | 含可选原厂驱动的安装器与受保护卸载 | Setup with optional vendor driver and guarded removal |
| 0.5.0–0.5.1 | 订阅卡片、加密保存、停止状态和流量信息修复 | Subscription cards, encrypted storage and status fixes |

See [validation history](docs/TEST-REPORT.md) for detailed development evidence and its limitations. Historical sections describe the version at that time, not the current feature set. Local identifiers have been replaced with examples.

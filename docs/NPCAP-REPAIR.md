# Npcap 安装失败诊断

> Public archive: local network/device identifiers below are illustrative replacements. Historical sections describe their original development stage.

检查日期：2026-10-13。

## 现场证据

- 用户截图与安装日志均显示 `0x800700b7`。
- `NPFInstall.log` 显示失败发生在 `INetCfgClassSetup::Install`，新组件 ID 为 `nmap_npcap`。
- Windows 的网络组件列表中存在旧版 `INSECURE_NPCAP`。
- 该旧组件仍绑定在 WLAN、Meta TUN 和其他网卡上。
- 旧组件注册记录为 `HKLM\SYSTEM\CurrentControlSet\Control\Network\{4d36e974-e325-11ce-bfc1-08002be10318}\{7DAF2AC8-E9F6-4765-A842-F1F5D2501341}`，描述引用 `oem21.inf`。
- 当前没有可用的 Npcap 驱动服务，也没有 `System32\Npcap\wpcap.dll` 或 `System32\drivers\npcap.sys`。
- 失败安装产生了待重启文件操作，其中包含 Npcap 驱动文件及安装目录删除记录。

这些信息支持旧组件残留导致安装冲突的判断，尚不能保证清理后一定成功。

## 已准备的限定修复

`scripts/Repair-Npcap.ps1` 默认只读检查。显式传入 `-RemoveLegacyComponent` 才会执行清理，并且需要管理员权限。

清理前要求：

- 只发现一个 `INSECURE_NPCAP` 组件，描述确认为 Npcap。
- 没有正在运行或改变状态的 Npcap/NPF 驱动。
- 已将该组件注册记录和绑定状态备份到项目 `.state` 目录。

只调用 Windows 的 `netcfg -u INSECURE_NPCAP` 移除该旧组件，不直接删除注册表，不执行网络重置，不卸载其他驱动，不改路由、防火墙、代理或 PS5 网关。

旧组件绑定在正在使用的网卡上；移除可能短暂中断网络。成功后脚本停止，不启动安装器、不自动重启。需要用户保存工作并手动重启，清除失败安装的待删除操作后，再安装签名已验证的 Npcap 1.89。

注册表备份只能用于诊断，不等同于完整驱动回滚。清理失败时立即停止，不扩大到其他网络组件。

## PS5 目标

- 用户确认 IPv4：`192.168.50.20`。
- 用户确认 MAC：`02-00-00-00-00-20`。
- 用户提供 IPv6：`fe80::20`，属于链路本地地址。

这个 IPv6 地址本身不代表有公网 IPv6，也不能证明不存在其他 IPv6 地址。首次实机接管前仍需确认是否有其他全局 IPv6；不预先勾选“没有公网 IPv6”。

当前仍未发送 ARP 接管帧或采集真实 PS5 数据包。

## 重启后的复查

2026-10-13 16:45 左右只读复查：

- 本次 Windows 启动时间为 16:38:53；最新安装失败记录发生在此前的 16:35:47，仍为 `0x800700b7`。
- `netcfg -q INSECURE_NPCAP` 仍返回已安装，旧组件注册记录与 WLAN、Meta 等网卡绑定仍存在。
- `npcap` 驱动服务查询仍返回 1060；驱动文件和运行库仍不存在。
- 失败安装的待重启文件操作已经消失。
- 项目中未找到 `.state\npcap-repair-*` 清理备份，因此不能证明此前实际进入了清理步骤。用户报告已运行脚本并重启；不能仅凭缺少记录断言用户运行了哪个命令。

脚本不带参数时只检查，检查完成不代表修复成功。真正清理的入口为：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File ".\scripts\Repair-Npcap.ps1" -RemoveLegacyComponent
```

必须在管理员会话中运行，并先确认可以承受短暂断网。此次仅修改了项目中的提示和清理结果记录，没有执行上述命令。清理模式现在会保存 `after.json`，检查旧组件注册和网卡绑定是否均已移除；不满足时停止并保留证据。

## 已执行的清理

用户明确接受清理可能短暂断网后，2026-10-13 16:49 通过 Windows 管理员权限执行限定清理：

- 管理员进程退出码为 0；`after.json` 中清理退出码为 0，旧组件与旧绑定均为空。
- 独立复查 `netcfg -q INSECURE_NPCAP` 返回未安装。
- WLAN 与 Meta 仍为 Up；WLAN IPv4 仍为 `192.168.50.10/24`，Clash 仍监听 `127.0.0.1:7897`。这些检查不代表已经验证所有网络业务。
- 备份和结果目录：`.state\npcap-repair-20261013-164901-811`，包含 `before.json`、`legacy-component.reg`、`netcfg-output.txt` 和 `after.json`。
- 未自动重启、未启动安装器、未进行 PS5 接管。按此前约定，停下来等待用户手动重启后再继续安装。

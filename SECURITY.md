# Security and Privacy / 安全与隐私

## Reporting / 报告

不要在公开 Issue 中发布可利用漏洞的敏感细节、订阅令牌、密码、完整 YAML 或用户数据。请先通过 README 中的维护者联系方式联系；如果仓库启用了 GitHub 私密漏洞报告，也可以使用该入口。普通功能问题可公开提交。

Do not post exploitable vulnerability details, tokens, passwords, complete YAML or personal data in public issues. Contact the maintainer through the README first, or use GitHub private vulnerability reporting if it is enabled. Ordinary feature/bug reports may be public.

There is no guaranteed response time or independent security audit. Start reports against the latest release.

## Local Data / 本地数据

- Settings and subscriptions: `%LocalAppData%\PS5Accelerator\profile.protected`, current-user DPAPI encrypted; encrypted backup alongside it.
- Standalone core runtime: restricted local temporary YAML, removed on normal shutdown; abnormal termination may leave files for later cleanup.
- Session/diagnostic data can contain LAN IP, MAC, adapter details and counters. Review before sharing.
- Do not upload the application-data directory, `.state`, tool caches or real subscription files.

独立核心运行时需要临时明文配置；本地加密不等于能防止同账户恶意程序读取运行时数据。语言、主题切换不会改变网络配置。

The standalone core needs temporary plaintext configuration. At-rest encryption does not protect runtime data from malicious software operating as the same user.

## Network Boundary / 网络边界

The gateway intentionally forwards IPv4 TCP/UDP through the selected exit. It is not an anonymity guarantee, a firewall or full IPv6 protection. Review public IPv6 and device recovery settings. Driver installation/removal is a privileged operation that may disrupt networking.

不要为排查问题关闭证书校验、添加未知根证书或公开节点凭据。请优先使用脱敏日志和离线夹具复现。

Do not disable certificate validation, add unknown roots or disclose node credentials to troubleshoot. Prefer sanitized reports and offline fixtures.

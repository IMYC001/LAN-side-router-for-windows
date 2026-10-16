# Windows Portability

Version 0.5.13, reviewed 2026-10-16.

The Settings page offers Simplified Chinese and English below the accent colors.
The selected language is saved for the current Windows account. Switching it only
updates presentation text; device names, proxy nodes, network settings and active
sessions remain unchanged. About contains the project link and contact details.
The earlier forwarding throughput problem was fixed in 0.5.5 and confirmed by the
user; actual throughput still depends on the PC, LAN, driver and selected node.

The new optional current-user installer includes the complete official signed
Windows Packet Filter 3.6.2.1 x64 MSI plus the original EULA. The MSI explicitly
permits free/non-profit packaging with complete copies and the EULA; commercial
redistribution requires prior written vendor permission. Existing Npcap or
Windows Packet Filter installations are reused. On a clean machine the wizard
requires explicit driver/network-interruption consent before requesting elevation.
No driver installation was performed during the build. Alternative-driver packet
IO and cross-PC behavior remain pending real-machine acceptance.
Uninstall behavior, ownership and deliberate shared-driver retention are described
in INSTALLATION.md.

## Supported Package

Copy or extract the entire Windows x64 release directory. The application includes
its .NET desktop runtime and Mihomo core; Go and .NET development tools are not
required on the destination computer. On an existing supported packet backend,
the application detects the physical interface and prepares parameters at Start.
The administrator helper still requires Windows consent.

Saved subscriptions are encrypted for the current Windows account. Reimport them
once on another computer. No credentials are included in the distribution.

HTTPS subscription fetching uses the system connection, direct connection and
configured loopback-only Clash SOCKS5/Mixed endpoint. Each route shares a
15-second deadline across the Windows client and an optional Go TLS compatibility
attempt on handshake failure. Explicit certificate rejection is terminal;
both implementations retain certificate verification. Go uses TLS 1.2/1.3 and
Windows certificate trust without the Schannel handshake. It does not add roots
or repair a missing trust store. Safe diagnostics distinguish certificate chain,
name, time and handshake failures without raw errors or subscription URLs.
HTTP refusals, redirects and the 8 MiB decoded limit remain enforced. The Windows
client supports gzip/deflate/br; Go negotiates gzip. Local YAML import remains
available. The reported Windows 10 machine still needs manual acceptance.

## Driver-Free Assessment

The current gateway receives Ethernet ARP requests for a userspace-only /30
gateway, answers them and injects IPv4 return frames on the physical LAN. It does
not assign this gateway to Windows, enable IP forwarding or change the host's
routes, firewall, DNS, or real router.

Windows `SIO_RCVALL` delivers IPv4/IPv6 to raw sockets but explicitly excludes ARP.
The Winsock raw-socket send restrictions also exclude general TCP sending on
Windows client versions. These interfaces cannot substitute for the current
Ethernet receive/inject backend.

Packet Monitor is a capture/diagnostic facility, not a replacement Ethernet
injection API. WFP's documented packet injection functions are driver interfaces.
Using ICS/host forwarding or a virtual network instead would require a different
network arrangement and host configuration changes, with renewed Clash TUN
coexistence validation. That is not a verified driver-free version of this app.

Consequently this release does not advertise driver-free acceleration. It uses
an existing Npcap or Windows Packet Filter installation and blocks clearly when
neither is present. No driver installation, adapter rebind, Windows network
configuration change or live gateway test was performed during this assessment.
Windows Packet Filter is the proposed alternative when Npcap is unavailable;
its actual LAN injection and Wi-Fi compatibility remain pending manual acceptance.
Installation can temporarily disconnect the computer and must be decided by the
user before execution.

## Primary References

- Microsoft, SIO_RCVALL: https://learn.microsoft.com/en-us/windows/win32/winsock/sio-rcvall
- Microsoft, TCP/IP Raw Sockets: https://learn.microsoft.com/en-us/windows/win32/winsock/tcp-ip-raw-sockets-2
- Microsoft, Packet Injection Functions: https://learn.microsoft.com/en-us/windows-hardware/drivers/network/packet-injection-functions
- Microsoft, Packet Monitor: https://learn.microsoft.com/en-us/windows-server/networking/technologies/pktmon/pktmon
- Windows Packet Filter: https://www.ntkernel.com/windows-packet-filter/

# Implementation State

Version 0.5.13, updated 2026-10-16.

0.5.13 adds persistent Chinese/English UI localization and an About page. Original
source, build scripts, tests and installer sources are published under GPL-3.0-only.
Multi-device cards share one WPF component; per-device controls preserve other
devices' relay state and counters. Language changes only affect presentation.
The original user confirmed the 0.5.5 throughput fix. Historical statements below
describe their respective development milestones, not fresh hardware acceptance.

0.5.5 explicitly selects classic SACK/duplicate-ACK loss recovery instead of
RACK in the pinned gVisor stack. On this queued Ethernet path the default RACK
configuration reproducibly retransmitted on a lossless FIFO mock driver.
Congestion control remains Reno; SACK, fast retransmission and the standard
retransmission timer remain active. Fault tests exercise download/upload loss,
reordering, combined faults and a lost single-segment download requiring RTO.

The output batcher now bounds each native burst to 256 frames / 256 KiB,
retaining its 1 ms partial-burst timer and immediate stop/lease validation before
each send. Npcap packs SDK-compatible headers and frames into one reusable Go
buffer, making one synchronous DLL call per burst; the installed SDK produces
identical queue bytes in an offline comparison. Windows Packet Filter uses
SEND_PACKETS_TO_ADAPTER with reusable intermediate buffers and the exact x64
variable request length (16 + 8 * count); the pinned wrapper incorrectly
double-counts its expanded pointer array. Its receive listen mode is unchanged.

The new full-path fixture uses production runGateway, packet validation,
lease watchdog, batcher, relay and loopback SOCKS. Only the physical packet IO
is replaced with an in-memory Ethernet/TCP peer. Repeated optimized download
results and initial baseline measurements are documented in TEST-REPORT.md.
These exclude real drivers, nodes and consoles. No physical gateway, driver
installation or Windows network change was performed for this release;
real-device throughput and alternative-driver packet IO await acceptance.

0.5.4 separates certificate rejection from platform TLS handshake failure.
The Windows callback accepts only SslPolicyErrors.None, retaining strict
verification while collecting enum-only certificate diagnostics. A handshake
failure without explicit certificate rejection can use the embedded Go
fetch-subscription command, with the same resolved proxy and remaining route
deadline. Go TLS requires 1.2 or newer and retains default system roots (Windows
CryptoAPI certificate verification); no InsecureSkipVerify or added trust store.
URLs travel only over stdin, responses return bounded base64 bytes and two
allowlisted metadata headers, and failures return fixed categories. Both clients
reject redirects, non-200 status and oversized decoded bodies. Windows supports
gzip/deflate/br; Go only advertises gzip. This change addresses a possible Win10
handshake limitation; it does not establish the reported machine's root cause
or repair missing roots. True cross-PC acceptance remains pending.

0.5.3 extracts subscription fetching into SubscriptionDownloader. Transport
failures try system proxy, direct connection and the configured literal-loopback
SOCKS5/Mixed proxy, with a 15-second header/body deadline per attempt. Errors
expose only route/category, never URI or exception text. HTTPS validation,
decompression, no redirects, HTTP refusal behavior and the 8 MiB decompressed
body cap remain enforced. Offline fixture checks cover fallback, quota metadata,
safe diagnostics, deadlines, TLS/HTTP refusal and unknown-length oversize bodies.
The reported other computer has not been accessed; its root cause remains unknown.

0.5.2 adds a current-user Windows installer with the complete signed vendor
Windows Packet Filter MSI and original EULA for personal/free/non-commercial
distribution. Existing backends are reused. Driver changes require explicit
network-interruption consent plus elevation. The installer blocks running app
or gateway processes; version 0.5.2 also refuses normal startup during maintenance.
It does not terminate running clients to upgrade. A machine-protected ownership
record captures existing INF packages, publisher certificate and driver file,
and guards driver removal against owner/version/file changes. Uninstall asks
whether an owned driver has become shared before removal; retained shared drivers
lose this app's ownership metadata. Driver MSI removal and non-forced pnputil
package deletion run only with explicit uninstall consent. User data cleanup
rejects linked directories, uses a fixed current-user root, and reports locks.
Failures retain the uninstall entry for retry. Full real-machine installation,
driver packet IO, reboot cleanup and uninstall acceptance remain pending.

Generic ICMP was not added: standard SOCKS5 CONNECT/UDP ASSOCIATE does not carry
ICMP. Gateway echo remains supported. Node-preserving public echo would require
a different L3 tunnel and server capabilities. No direct bypass or fabricated
public replies were introduced; TCP/UDP forwarding was not changed in 0.5.2.

0.5.1 clears device presentation after stop, during stopping and on invalidation,
hides expired parameters and zeros rates. Subscription byte counts use binary
units with the existing GB/MB labels; the card tooltip specifies the cached
provider timestamp and refresh behavior. Status publication uses a unique
temporary file and native ReplaceFileW (MoveFileExW only for an absent first
snapshot), retrying Windows access/sharing/lock conflicts
for at most approximately 500 ms while keeping the previous complete snapshot.
Persistent failure remains fatal. GUI status metadata comes from the same
delete-sharing file handle as the JSON, avoiding both extra path handles and
timestamp/content snapshot mismatch. Valid virtual-gateway echo can discover a
client with cached ARP, and its first request is answered after lease validation.
Public or other private-address ICMP remains unsupported, never fabricated or
sent directly around the node.

0.5 adds a multi-subscription library (schema 3), card metadata from HTTP headers,
independent node choices per record, explicit refresh/rename/delete/switch, and
schema 1/2 migration. Failed parse/import preserves the existing record. The main
page shows only blockers rather than a check table, and moves technical counters
to diagnostics. The branding and connection lifecycle cover IPv4-configurable
devices, still one device per gateway session. A bounded DNS hint parser observes
only MAC/IP-validated client packets; it retains platform-family bits, never raw
domains, and labels hardware as probable or unknown. Mixed-platform traffic is
unknown. Encrypted DNS, browsers and shared service use limit identification.

The Windows built-in driver-free assessment is in PORTABILITY.md. No reliable
substitute for the required Ethernet ARP receive/inject path was found under the
unchanged-host-network constraints. The installed-driver requirement remains;
no drivers or network settings were modified.

The user confirms independent nodes accelerate games and expose only the node's public IP on PS5. Existing Clash works in Global mode; Rule mode exposed both real and node IPs. The user later confirmed that release 0.5.5 resolved the forwarding throughput issue. This does not certify all computers, drivers or nodes. No live gateway, driver installation or Windows network change is part of this verification pass.

## Components

| Component | State |
| --- | --- |
| Four-field manual IPv4 gateway | Implemented; earlier PS5 connection and NAT 2 reported |
| Independent subscription outlet | Selected embedded node; bundled standalone Mihomo v1.19.32 |
| Existing Clash outlet | Retained; switch/start warning and persistent banner require Global mode |
| Subscription import | HTTPS compressed response or local YAML, 8 MiB limit |
| Settings persistence | Immediate DPAPI save, encrypted backup/recovery; schema 3 multi-record library; schema 1/2 migration |
| Node selection | Synchronized subscription list and outlet selector |
| Target identity | Generated-IP ARP or valid cached-gateway IPv4; immutable first-client MAC; optional probable platform from DNS |
| Session duration | Continuous desktop workflow; three-minute controls removed |
| Npcap alternative | Installed Windows Packet Filter receive LISTEN and injection; live test pending |
| Portability | Self-contained Windows x64 directory; one supported installed packet driver required |
| Tray lifecycle | Hide, restore, stop, exit; no automatic gateway start |
| Failure recovery | Lease, stop file, status timeout, suspend stop and interface checks |
| Diagnostics | Typed ZIP allowlist, recent 7,200 samples, credential-exclusion smoke check |
| UI verification | Five tabs at 1180 x 850 and 960 x 700 plus scrolled/alternate outlet views |
| Game/voice/latency and sustained PS5 session | Pending manual acceptance |
| Actual independent-node upstream with Clash TUN | User reports game success; cross-computer compatibility pending |
| Throughput issue | Deferred; remains unresolved |
| IPv6 and full-cone inbound support | Not implemented |

## Data Path

```text
PS5 /30 manual IPv4 settings
  -> physical Npcap OR Windows Packet Filter receive backend
  -> generated-IP validated gateway discovery and immutable MAC binding
  -> separate gVisor/tun2socks IPv4 TCP/UDP stack
  -> local SOCKS5 CONNECT / UDP ASSOCIATE
  -> selected independent Mihomo node OR existing Clash
  -> physical adapter return frames
```

No virtual address is assigned to a Windows interface. No Windows route, firewall, DNS, forwarding or router setting is changed. Only the confirmed device receives virtual-gateway ARP responses. Private destinations are blocked, gateway DNS is remapped, and public ICMP is not fabricated. Wi-Fi injection remains dependent on the installed driver and hardware.

Discovery accepts the first valid device using the generated /30 IP and gateway; it does not infer PlayStation identity from vendor MAC or require the original IP/MAC. It reuses gateway ARP/IP validation, copies the discovered MAC and rejects other source MACs afterward. Valid IPv4 addressed to the PC permits reconnect with cached gateway ARP. Do not configure multiple devices with the same generated address.

## Packet Backends

An existing Npcap backend is preferred. Otherwise a registered `ndisrd` service enables Windows Packet Filter; opening the driver and validating adapter GUID/MAC, MTU and mode remain elevated-worker checks. Missing both drivers blocks acceleration. Neither driver nor installer is bundled or installed automatically.

The alternative backend uses receive LISTEN copies, no tunnel mode or global static rules. A nonzero existing filter mode is refused; normal close resets mode and event. The pinned `ndisapi-go` v1.0.1 has incorrect ReadPacket, GetAdapterMode and queue-query helpers. The backend calls documented READ_PACKET, GET_ADAPTER_MODE and ADAPTER_QUEUE_SIZE IOCTLs directly, validates x64 packed ABI offsets/sizes and retains packet buffers across calls. Bounded event waits permit cancellation.

Only ABI, software filtering and closed/canceled handles have been tested. Live driver behavior, Wi-Fi injection, performance and other computers remain unverified. Commercial driver redistribution needs vendor licensing review.

## Independent Core

`prepare-outbound` extracts one supported embedded proxy and generates a fresh configuration. Subscription rules, TUN, listeners, controllers, scripts, providers and tunnels do not become executable settings. It overrides the node and global physical interface, forces IPv4, disables TUN, and exposes random SOCKS/controller ports on loopback only. The controller uses a random bearer secret.

The core starts unprivileged in a restricted per-session directory. Its controller is checked for readiness, exact selected interface, disabled TUN and loopback listeners before use. A Windows kill-on-job-close handle owns the process. Standard output/error are drained and discarded because third-party messages may expose endpoints or credentials.

Node setup rejects local/private/fake-IP literal servers, chain dependencies and unsupported plugins. Importing a valid YAML does not guarantee a given node or UDP mode is operational; real proxy probes and PS5 testing are still required. Physical-interface settings do not prove immunity to every third-party TUN/WFP policy.

## Settings and Temporary Files

`profile.protected` schema 3 is an atomic DPAPI CurrentUser encrypted record containing up to 32 subscriptions, metadata and selected node per record, active record, adapter, proxy and outlet. Schema 1/2 single subscriptions migrate to a card. Successful import saves immediately; normal startup restores content before parsing/network inventory and never starts acceleration. Parse or refresh failure cannot erase saved subscription content.

The `.bak` file is encrypted too. Missing/corrupt primaries recover from backup; saving after recovery does not replace a good backup with a corrupt primary. Failed decryption leaves the original untouched until a successful import. Copying the whole release directory supports another Windows x64 computer, but DPAPI profiles cannot move across accounts/computers; reimport the subscription there.

Mihomo requires a temporary plaintext configuration under a random restricted `runtime` directory. Normal disposal deletes it after process exit. Startup cleans stale GUID-named directories only when no standalone Mihomo process is running, and rejects reparse roots/targets. A power loss can leave temporary plaintext until cleanup. Smoke tests use dummy nodes and disposable profile directories, without loading or overwriting the user's profile.

## Session Lifecycle

Start refreshes inventory, prepares the selected core and four parameters, then launches an explicit elevated worker and waits for a configured client. No prior passive proof is required for automatic discovery. Historical passive CLI checks remain bounded to 20 seconds and are absent from the desktop workflow.

Live gateway requests require confirmation and a fresh matching preview. Continuous operation is only accepted for gateway mode with `continuous=true` and `seconds=0`; it does not automatically restart or change nodes. Internal bounded requests retain validation but no three-minute option appears in the desktop.

Waiting and forwarding both use a stop file and an eight-second GUI lease. The helper watches stop/lease independently of interface inventory and checks physical interface state every five seconds. Continuous forwarding also checks the local proxy TCP listener every five seconds; three consecutive failures stop the gateway. This does not test remote node or game health. A single remote flow failure does not kill continuous operation.

The GUI stops on suspend or a polling pause over eight seconds using monotonic elapsed time. Initial valid-status acquisition is limited to 45 seconds, including malformed status files; accepted status cannot remain stale indefinitely. Communication failure stops renewing the lease and enforces a nine-second retry cooldown. The GUI does not force-kill an elevated worker during file writes.

Power loss and software stop cannot restore PS5 manual settings. The user must restore automatic IP/DNS or original custom values. Kernel/driver stalls remain outside proven timing guarantees.

The historical `--authorized-gateway-test` automatic-start entry is removed. Ordinary launch never starts a gateway.

## Diagnostics and Verification

Runtime status includes target counters, payload rates, capture drops, TCP retransmits/timeouts and send queue timing. Performance recording stops around 64 MiB per session; it does not store packet payload. Exports select typed status fields and recent samples rather than copying arbitrary files. Device IP/MAC and adapter metadata remain present; this is not an anonymous report.

Go checks cover discovery identity/first reply, malformed/foreign packets, immutable binding, idle/cancel zero sends, alternative driver ABI/software filters, selected-node isolation, continuous restrictions, target filtering, shared UDP sessions and in-memory TCP/UDP/DNS relay. Desktop smoke checks cover encrypted backup recovery/schema migration, two real fresh-process subscription/node restarts, diagnostics exclusions, actual loopback-core isolation/cleanup, synchronized node selection, input locks, retry cooldown and screenshots. Disposable profiles and dummy nodes isolate these checks from real user settings.

These checks do not prove PS5 throughput, NAT, games, voice, sleep recovery or independent upstream coexistence with the active Clash TUN. Go race detection remains unavailable without a supported C compiler.

Historical implementation and source references are retained in `IMPLEMENTATION-0.2.md`.

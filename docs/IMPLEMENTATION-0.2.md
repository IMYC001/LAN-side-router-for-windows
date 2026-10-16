# Historical Implementation State (0.2)

Version 0.2.3-batched-egress, updated 2026-10-13.

## Safety Boundary

Normal startup exposes read-only diagnostics, probes, subscription parsing and previews. Only the separately confirmed elevated `worker <request.json>` opens Npcap; the UI distinguishes passive target observation from a live manual gateway. Both are bounded and tied to a controller lease. Authorized physical target capture and live gateway tests have completed. The user reported NAT Type 3 on the first version and NAT Type 2 on the 0.2.1 retest. There is no OS route/DNS/address/firewall mutation or forwarding toggle.

The user has granted continuing authorization for operations that do not interrupt connectivity, while requiring a stop before new network-risk decisions or manual PS5 testing. Driver installation and 20-second target observation are complete. The user has now accepted the first bounded IPv4 gateway test. Only the known link-local IPv6 was found in their device information; global IPv6 absence and bypass remain unverified.

## Components

| Component | State |
| --- | --- |
| WPF desktop UI | Six-tab manual gateway UI, parameters and stop/restore states |
| Windows adapter / neighbor / listener inventory | Read-only, implemented |
| SOCKS5 CONNECT | Implemented, local echo and TLS probe |
| SOCKS5 UDP ASSOCIATE | Implemented, loopback relay enforcement |
| gVisor IP stack via tun2socks | Implemented, in-memory TCP/UDP end-to-end tested |
| Ethernet target filter and return encapsulation | Implemented, first PS5 TCP/UDP transfer observed |
| Targeted ARP takeover / restoration frame builders | Implemented, never transmitted |
| Clash YAML subscription summary | Implemented; no independent runtime |
| Npcap driver adapter | Implemented, target WLAN capture/injection and proxy return verified |
| Manual /30 gateway, target ARP, gateway ICMP | First manual connection passed; never impersonates router |
| Virtual gateway DNS TCP/UDP mapping | Implemented, in-memory return source/payload verified |
| Bounded helper / lease / stop / interface watcher | Implemented; capture failure and deadline cleanup tests |
| Independent no-TUN Mihomo instance | Pending upstream-interface / TUN isolation testing |
| Proxy STUN mapping diagnostic | Implemented, four-request sample through current Clash |
| UDP association reuse by device/source port | Offline validated; user reported NAT 2 on the PS5 retest |
| Immediate capture / receive event / drop counters | 0.2.2; physical read-only verification and PS5 throughput retest recorded separately |
| Npcap batched physical egress | 0.2.3; uses the Windows send queue for bounded short bursts |
| PS5 game testing | Basic connection and NAT 2 reported; game/voice/latency pending |

## Manual Gateway Lifecycle

The PC never assigns the virtual /30 to a Windows interface. AP/switch carriage of Ethernet frames and PS5 acceptance of these settings require a manual test.

1. Prepare a fresh preview containing physical GUID/IP/prefix/MAC/router and virtual IP/gateway.
2. Confirm target identity and duplicate Windows forwarding status. Either report IPv6 absence or explicitly accept a bounded IPv4-only test with IPv6 bypass still unverified.
3. Separately authorize at most 20 seconds of passive capture for the confirmed MAC; record counters/source IP metadata only, not packet payload. Receipt is not injection proof.
4. Require recent passive success for the same interface/MAC, an accepted restore procedure and explicit live-test consent. The user must record original PS5 settings before changing them.
5. Start the elevated helper with an absolute 180-second deadline. Verify a fresh snapshot matches the preview, probe SOCKS TCP/UDP, then open the exact physical GUID with source-MAC/source-IP BPF and non-promiscuous Ethernet capture.
6. Display ready only after the stack/driver is open. Respond only to the target's ARP request for the synthetic gateway; never impersonate the real router or send DHCP/NACK.
7. Forward target public TCP/UDP via loopback SOCKS; gateway port 53 maps to 1.1.1.1:53. Preserve the DNS source endpoint on return. Block local/private destinations; answer only gateway ICMP echo.
8. Deadline, stop file, heartbeat expiry, interface change, capture/injection error or a recorded relay failure ends the test. Cancel/join pumps and monitor, close capture before the final stopped status.
9. Restore PS5 automatic IP/DNS or its original custom settings manually.

If GUI status communication fails, stop renewing the lease and disallow a new session for nine seconds, longer than the eight-second heartbeat allowance. An offline test verifies that an already queued valid ARP request is discarded after a stop request.

A watchdog cannot restore PS5's static settings. Power loss leaves the PS5 using an unavailable gateway until the user changes its configuration. Router MAC discovery is unnecessary for this gateway. Status/request files are under LocalAppData and contain no subscription credentials or packet payload.

## Known Limits

The user reported only a few megabits per second through the bridge versus roughly 100 without acceleration (exact unit and target comparison not independently confirmed). The former capture opened with a 100 ms buffering timeout and slept on a 10 ms ticker after empty reads. Version 0.2.2 uses `pcap_create`, a 4 MiB kernel capture buffer, immediate delivery before activation, nonblocking reads and the Npcap receive event. Version 0.2.3 batches short physical egress bursts with `pcap_sendqueue_transmit(..., sync=0)` and keeps the batch bounded at 64 frames or 256 KiB, with a 1 ms flush ceiling. The event wait lasts at most 10 ms per native wait and does not hold the packet send lock; available packets are drained without waiting. A separate lifetime lock protects the Npcap-owned event from concurrent close. Native driver stalls remain outside proven timing guarantees.

The GUI displays sampled payload throughput in Mbps and `pcap_stats` receive-buffer drops, with an explicit unavailable state rather than treating missing counters as zero. Neither counter establishes whole-path packet loss. All per-packet stop-file and lease validation remains unchanged; there is no cached safety decision. Its standalone local benchmark measured approximately 35-42 microseconds per check, not sufficient by itself to identify the observed bottleneck.

An 8 MiB concurrent TCP echo with byte validation through memory stacks and a loopback SOCKS server completed at roughly 270-320 Mbit/s on this host. This excludes native capture/injection, file checks and the existing Clash/node/CDN path. It is not a physical gateway or PS5 throughput benchmark, nor proof that the 0.2.2 capture change fixes the reported slowdown. The current bridge still shares Clash rules, DNS and exit choice.

The bridge shares Clash's existing policy. Loopback connection isolation avoids a second TUN but does not prove that upstream nodes are independent from the host's routes or WFP filters.

The first live prototype had per-destination UDP associations. The updated relay shares associations by device IP/source port and reserves users before asynchronous setup so sibling flows cannot close an association during attachment. A single reader demultiplexes replies to contacted destinations only; unrelated sources remain discarded. Different source ports never share. Gateway DNS remapping is isolated from ordinary UDP to preserve response endpoints. Closing the last flow, control failure or engine shutdown closes the association and its related flow endpoints. Offline concurrent-destination, source isolation, DNS source and reference-lifetime tests pass. In the authorized 20:18-20:21 PS5 retest, the user reported NAT Type 2. This is one reported result, not proof of sole causation or a guarantee across nodes/networks. Full-cone filtering, preservation of public PS5 source ports and unsolicited inbound reachability remain unproven.

Adapter-prefix overlap checks do not inspect every VPN policy route. Wi-Fi injection and PS5 TCP/UDP traffic worked; checks after the tests found the existing Meta TUN still Up. Application-level PSN/games/voice and host connectivity during acceleration remain unverified. The latest stopped session recorded TCP 121, UDP 112 flows, 108 UDP associations, 29,167,325 uploaded bytes, 76,682,458 downloaded bytes and zero recorded relay failures. PS5 settings restoration has been requested but not confirmed.

Not every ordinary idle timeout or lost UDP reply increments the failure counter. The bounded test has no comprehensive game reachability monitor. Native DLL/driver stalls are also outside the tested timing guarantees.

Subscriptions are parsed and summarized, not persisted or activated. A future independent Mihomo runtime must ignore subscription-side TUN, listeners, external controllers, routing, scripts and process policy, explicitly bind upstream egress, and be validated with the existing TUN enabled.

`nat-probe` performs at most four STUN binding requests through the existing loopback SOCKS proxy with a 22-second overall budget, using Pion STUN v3.1.7 for message parsing. It compares A/B/A on one association and A on a second, validates server endpoints and transaction IDs, and verifies fingerprints when provided. Only successful complete samples are compared. It does not implement RFC 5780 filtering tests or certify PS5 NAT type. System DNS can yield Clash fake-IP endpoints; they are resolved by the existing proxy policy, which is not altered. The observed sample had equal mappings across the two destinations, stable on repeat, and a different mapping for the second association.

Npcap 1.89 is Running; wpcap.dll 1.10.7 and Packet.dll 1.89 load and enumerate Intel AX201 WLAN. On 2026-10-13, a 20-second passive session received one ARP and one IPv4 frame with the confirmed PS5 MAC after a single ordinary OS ICMP probe to its original IP. The OS selected directly connected WLAN interface 11; the neighbor entry matched the confirmed MAC. Capture emitted zero frames, the helper exited normally, and physical adapter settings were unchanged. This proves target reception, not gateway injection, game forwarding or TUN coexistence during acceleration.

`scripts/Observe-PS5.ps1` performs only bounded passive capture, with optional `-ProbeTarget` sending one ordinary asynchronous ICMP request after capture is ready and the selected route is verified as directly connected on the confirmed WLAN. It records status and network metadata under `.state`, never payload or subscription credentials. Idle capture reports the original device address rather than an unused virtual candidate.

Normal desktop startup remains read-only. An explicit `--authorized-gateway-test "<passive-directory>"` invocation used for the accepted test validates and imports a completed record: age at most ten minutes and not future-dated, normal helper exit, unchanged adapter, passive mode within twenty seconds, received target data, zero sent/forwarded/proxy traffic, exact original IP/MAC and current adapter GUID/IP/MAC/prefix/router/index. Only then does it prepare a fresh preview and start the existing GUI-controlled elevated gateway. Offline smoke checks reject expired/future and altered records. The IPv4-test consent does not claim IPv6 absence or that the user has already saved their settings.

## Sources Consulted

- WinDivert author documentation: https://reqrypt.org/windivert-doc.html
- Npcap developer guide: https://npcap.com/guide/npcap-devguide.html
- Official Npcap SDK 1.16 headers in `.tools/npcap-sdk-1.16` for Windows ABI and pcap APIs.
- tun2socks pinned v2.6.0 core and TCP/UDP handlers: https://github.com/xjasonlyu/tun2socks/tree/v2.6.0/core
- gVisor netstack: https://github.com/google/gvisor
- SOCKS5 / UDP association: RFC 1928, https://www.rfc-editor.org/rfc/rfc1928

The supplied UU research distinguishes observations and inferences. We reproduce the user-confirmed four-field manual workflow; the untested real-router "mirroring" interpretation is not assumed. No proprietary UU binaries are called or redistributed.

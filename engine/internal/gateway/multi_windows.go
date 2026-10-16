package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"accelerator/engine/internal/network"
	"accelerator/engine/internal/npcap"
	"accelerator/engine/internal/packet"
	"accelerator/engine/internal/probe"
	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/socks"
	"accelerator/engine/internal/winpkfilter"
)

// multiGatewayRuntime owns one virtual /30 and one relay stack. Capture,
// injection and packet batching remain shared by the complete session.
type multiGatewayRuntime struct {
	id      string
	plan    network.ManualPlan
	engine  *relay.Engine
	request DeviceRequest
	ctx     context.Context
	cancel  context.CancelFunc
	closed  atomic.Bool

	mu      sync.RWMutex
	bound   bool
	mac     string
	gateway packet.ManualGateway

	received  atomic.Uint64
	sent      atomic.Uint64
	arp       atomic.Uint64
	ipv4      atomic.Uint64
	forwarded atomic.Uint64

	previous relay.Stats
	sampled  time.Time
}

func (r *multiGatewayRuntime) close() {
	if !r.closed.Swap(true) {
		r.cancel()
		r.engine.Close()
	}
}

func runMultiGateway(ctx context.Context, cancel context.CancelFunc, directory string, snapshot network.Snapshot,
	request Request, status *Status) error {
	return runMultiGatewayWith(ctx, cancel, directory, snapshot, request, status, multiGatewayDependencies{
		inspect: network.Inspect, probe: probe.Run,
		open: func(adapter network.Adapter, backend string, addresses []string) (captureHandle, error) {
			if backend == "winpkfilter" {
				return winpkfilter.OpenMany(adapter.GUID, adapter.MAC, addresses)
			}
			return npcap.OpenMultiDiscovery(adapter.GUID, adapter.MAC)
		},
	})
}

// Dependencies are resolved once at startup; tests substitute only the physical
// capture, network inspection and external connectivity probe.
type multiGatewayDependencies struct {
	inspect func(context.Context) (network.Snapshot, error)
	probe   func(context.Context, string) probe.Result
	open    func(network.Adapter, string, []string) (captureHandle, error)
}

func runMultiGatewayWith(ctx context.Context, cancel context.CancelFunc, directory string, snapshot network.Snapshot,
	request Request, status *Status, dependencies multiGatewayDependencies) error {
	if err := validateControl(deviceControl{Revision: 1, Devices: request.Devices}, nil); err != nil {
		return err
	}
	if ctx.Err() != nil || !leaseValid(directory, time.Now()) {
		return nil
	}

	runtimes := make([]*multiGatewayRuntime, 0, len(request.Devices))
	registry := &runtimeRegistry{}
	defer func() {
		for _, runtime := range runtimes {
			runtime.close()
		}
	}()
	proxies := make(map[string]struct{}, len(request.Devices))
	var adapter network.Adapter
	for index, device := range request.Devices {
		plan := network.BuildManualPlan(snapshot, device.Plan)
		if !plan.Prepared {
			return errors.New("设备网络参数未满足，未打开网卡")
		}
		if !matchesDeviceExpected(device.Expected, request.Mode, plan) {
			return errors.New("网络或预览参数已变化，请重新准备；未打开网卡")
		}
		if index == 0 {
			adapter = plan.Adapter
		} else if plan.Adapter.GUID != adapter.GUID || plan.Adapter.Index != adapter.Index ||
			plan.Adapter.IPv4 != adapter.IPv4 || plan.Adapter.MAC != adapter.MAC ||
			plan.Adapter.Gateway != adapter.Gateway || plan.Adapter.Prefix != adapter.Prefix ||
			plan.Backend != runtimes[0].plan.Backend {
			return errors.New("多设备必须使用同一状态未变化的物理网卡")
		}
		gateway, gatewayErr := netip.ParseAddr(plan.Gateway)
		if gatewayErr != nil {
			return errors.New("设备虚拟网关参数无效")
		}
		engine, engineErr := relay.NewWithDNS(socks.Client{Address: plan.Proxy}, gateway, plan.UpstreamDNS)
		if engineErr != nil {
			return engineErr
		}
		deviceCtx, deviceCancel := context.WithCancel(ctx)
		runtimes = append(runtimes, &multiGatewayRuntime{
			id: device.ID, plan: plan, engine: engine, sampled: time.Now(),
			request: device, ctx: deviceCtx, cancel: deviceCancel,
		})
		proxies[plan.Proxy] = struct{}{}
	}
	// Confirm every selected node before the NIC is opened. This gives each
	// device an independent node without exposing partial forwarding state.
	for proxy := range proxies {
		result := dependencies.probe(ctx, proxy)
		if !result.TCP || !result.UDP {
			return errors.New("至少一个设备选择的代理 TCP/UDP 检查未通过，未启用网关")
		}
	}
	if ctx.Err() != nil || !leaseValid(directory, time.Now()) {
		return errors.New("准备超时或界面已关闭，未打开网卡")
	}

	var handle captureHandle
	var err error
	addresses := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		addresses = append(addresses, runtime.plan.DeviceIP)
	}
	handle, err = dependencies.open(adapter, runtimes[0].plan.Backend, addresses)
	if err != nil {
		return err
	}
	defer handle.Close()
	registry.current.Store(&runtimeSet{devices: runtimes})

	local, err := net.ParseMAC(adapter.MAC)
	if err != nil || len(local) != 6 {
		return errors.New("物理网卡 MAC 无效")
	}
	status.Continuous = request.Continuous
	status.Phase, status.Message = "waiting", "网关已就绪，请在每台设备填写对应卡片的四项参数"

	var received, sent atomic.Uint64
	errs := make(chan error, len(runtimes)+4)
	fail := func(failure error) {
		if !errors.Is(failure, context.Canceled) && !errors.Is(failure, context.DeadlineExceeded) {
			select {
			case errs <- failure:
			default:
			}
		}
		cancel()
	}
	var batcher *frameBatcher
	if batch, ok := handle.(batchPacketIO); ok {
		batcher = newFrameBatcher(ctx, directory, batch, fail)
	}
	var workers sync.WaitGroup
	leaseDone := watchLease(ctx, cancel, directory)
	defer func() {
		cancel()
		for _, runtime := range registry.load() {
			runtime.close()
		}
		if batcher != nil {
			batcher.Close()
		}
		<-leaseDone
		workers.Wait()
	}()

	emit := func(owner *multiGatewayRuntime, frame []byte) error {
		if err := owner.ctx.Err(); err != nil {
			return err
		}
		var sendErr error
		if batcher != nil {
			sendErr = batcher.Send(frame)
		} else {
			if !leaseValid(directory, time.Now()) {
				cancel()
				return context.Canceled
			}
			sendErr = handle.Send(frame)
		}
		if sendErr != nil {
			return sendErr
		}
		sent.Add(1)
		owner.sent.Add(1)
		return nil
	}

	startOutput := func(owner *multiGatewayRuntime, gateway packet.ManualGateway) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				ip, readErr := owner.engine.ReadPacket(owner.ctx)
				if readErr != nil {
					if owner.ctx.Err() == nil {
						fail(errors.New("返回数据读取中断，加速停止"))
					}
					return
				}
				frame, outputErr := gateway.Target.Output(ip)
				if outputErr != nil {
					fail(outputErr)
					return
				}
				if emitErr := emit(owner, frame); emitErr != nil {
					if owner.ctx.Err() == nil {
						fail(emitErr)
					}
					return
				}
			}
		}()
	}

	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			frame, readErr := handle.Read(ctx)
			if readErr != nil {
				if ctx.Err() == nil {
					fail(readErr)
				}
				return
			}
			if ctx.Err() != nil {
				return
			}
			received.Add(1)
			for _, runtime := range registry.load() {
				if runtime.closed.Load() {
					continue
				}
				runtime.mu.RLock()
				bound, gateway := runtime.bound, runtime.gateway
				runtime.mu.RUnlock()
				if !bound {
					deviceIP := netip.MustParseAddr(runtime.plan.DeviceIP)
					gatewayIP := netip.MustParseAddr(runtime.plan.Gateway)
					mac, found := packet.DiscoverGatewayClient(frame, local, deviceIP, gatewayIP)
					if !found {
						continue
					}
					gateway = packet.ManualGateway{Address: gatewayIP, Target: packet.Target{
						LocalMAC: local, DeviceMAC: mac, DeviceIP: deviceIP,
						GatewayIP: netip.MustParseAddr(adapter.Gateway),
					}}
					if validateErr := gateway.Validate(); validateErr != nil {
						fail(validateErr)
						return
					}
					runtime.mu.Lock()
					runtime.bound, runtime.mac, runtime.gateway = true, mac.String(), gateway
					runtime.mu.Unlock()
					startOutput(runtime, gateway)
				}
				if reply, ok := gateway.ReplyARP(frame); ok {
					runtime.received.Add(1)
					runtime.arp.Add(1)
					if emitErr := emit(runtime, reply); emitErr != nil {
						if runtime.ctx.Err() == nil {
							fail(emitErr)
							return
						}
					}
					break
				}
				if reply, ok := gateway.ReplyEcho(frame); ok {
					runtime.received.Add(1)
					if emitErr := emit(runtime, reply); emitErr != nil {
						if runtime.ctx.Err() == nil {
							fail(emitErr)
							return
						}
					}
					break
				}
				if ip, ok := gateway.Input(frame); ok {
					runtime.received.Add(1)
					runtime.ipv4.Add(1)
					if injectErr := runtime.engine.Inject(ip); injectErr != nil {
						if runtime.ctx.Err() == nil {
							fail(injectErr)
							return
						}
						break
					}
					runtime.forwarded.Add(1)
					break
				}
			}
		}
	}()

	// Keep the same adapter safety check as the single-device path. It only
	// observes state and ends forwarding before any unexpected NIC change.
	workers.Add(1)
	go func() {
		defer workers.Done()
		check := time.NewTicker(5 * time.Second)
		defer check.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-check.C:
				current, inspectErr := dependencies.inspect(ctx)
				matched := false
				for _, candidate := range current.Adapters {
					if candidate.Index == adapter.Index && candidate.GUID == adapter.GUID && candidate.Status == "Up" &&
						candidate.IPv4 == adapter.IPv4 && candidate.Prefix == adapter.Prefix &&
						candidate.Gateway == adapter.Gateway && candidate.MAC == adapter.MAC && !candidate.Forwarding {
						matched = true
					}
				}
				if inspectErr != nil || !matched {
					if ctx.Err() == nil {
						fail(errors.New("物理网卡状态已变化，加速停止"))
					}
					return
				}
			}
		}
	}()

	if request.Continuous {
		workers.Add(1)
		go func() {
			defer workers.Done()
			check := time.NewTicker(5 * time.Second)
			defer check.Stop()
			failures := make(map[string]int, len(proxies))
			for {
				select {
				case <-ctx.Done():
					return
				case <-check.C:
					currentProxies := map[string]struct{}{}
					for _, runtime := range registry.load() {
						currentProxies[runtime.plan.Proxy] = struct{}{}
					}
					for proxy := range currentProxies {
						dialCtx, finish := context.WithTimeout(ctx, 2*time.Second)
						connection, dialErr := (&net.Dialer{}).DialContext(dialCtx, "tcp", proxy)
						finish()
						if dialErr == nil {
							connection.Close()
							failures[proxy] = 0
						} else {
							failures[proxy]++
						}
						if failures[proxy] >= 3 && ctx.Err() == nil {
							stillActive := false
							for _, runtime := range registry.load() {
								if runtime.plan.Proxy == proxy {
									stillActive = true
								}
							}
							if !stillActive {
								delete(failures, proxy)
								continue
							}
							fail(errors.New("至少一个设备的代理入口连续失联，加速已停止；请恢复设备原网络设置"))
							return
						}
					}
				}
			}
		}()
	}

	deadline, bounded := ctx.Deadline()
	started := time.Now()
	controlResults := make(chan preparedControl)
	controlBusy := false
	var seenRevision int64
	buildAdded := func(buildCtx context.Context, device DeviceRequest) (*multiGatewayRuntime, error) {
		current, inspectErr := dependencies.inspect(buildCtx)
		if inspectErr != nil {
			return nil, errors.New("无法检查新设备网络参数，已有设备继续加速")
		}
		plan := network.BuildManualPlan(current, device.Plan)
		if !plan.Prepared || !matchesDeviceExpected(device.Expected, request.Mode, plan) ||
			plan.Adapter != adapter || plan.Backend != runtimes[0].plan.Backend {
			return nil, errors.New("新设备网络参数或网卡已变化，已有设备继续加速")
		}
		probeResult := dependencies.probe(buildCtx, plan.Proxy)
		if !probeResult.TCP || !probeResult.UDP {
			return nil, errors.New("新设备节点 TCP/UDP 检查未通过，已有设备继续加速")
		}
		gateway, parseErr := netip.ParseAddr(plan.Gateway)
		if parseErr != nil {
			return nil, errors.New("新设备网关无效")
		}
		engine, engineErr := relay.NewWithDNS(socks.Client{Address: plan.Proxy}, gateway, plan.UpstreamDNS)
		if engineErr != nil {
			return nil, errors.New("新设备转发准备失败")
		}
		deviceCtx, deviceCancel := context.WithCancel(ctx)
		return &multiGatewayRuntime{id: device.ID, request: device, plan: plan, engine: engine,
			ctx: deviceCtx, cancel: deviceCancel, sampled: time.Now()}, nil
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		now := time.Now()
		status.Received, status.Sent = received.Load(), sent.Load()
		status.ARP, status.IPv4, status.Forwarded = 0, 0, 0
		status.Stats = relay.Stats{}
		status.Devices = status.Devices[:0]
		connected, identified := 0, 0
		for _, runtime := range registry.load() {
			deviceStatus := runtimeStatus(runtime, now)
			status.Devices = append(status.Devices, deviceStatus)
			status.ARP += deviceStatus.ARP
			status.IPv4 += deviceStatus.IPv4
			status.Forwarded += deviceStatus.Forwarded
			addRelayStats(&status.Stats, deviceStatus.Stats)
			if deviceStatus.Phase == "connected" {
				connected++
			} else if deviceStatus.Phase == "identified" {
				identified++
			}
		}
		if connected > 0 {
			status.Phase, status.Message = "connected", "设备已连接 · 多设备加速中"
		} else if identified > 0 {
			status.Phase, status.Message = "identified", "设备已连接，正在等待加速出口返回数据"
		} else {
			status.Phase, status.Message = "waiting", "网关已就绪，请在每台设备填写对应卡片的四项参数"
		}
		status.Elapsed = int(now.Sub(started).Seconds())
		if bounded {
			status.SecondsLeft = max(0, int(time.Until(deadline).Seconds()))
		}
		if capture, ok := handle.(interface{ Stats() npcap.CaptureStats }); ok {
			status.Capture = capture.Stats()
		}
		if transmit, ok := handle.(interface{ TransmitStats() npcap.TransmitStats }); ok {
			status.Transmit = transmit.TransmitStats()
		}
		if batcher != nil {
			status.PendingFrames = batcher.Pending()
		}
		if err := writeStatus(directory, *status); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			select {
			case failure := <-errs:
				return failure
			default:
			}
			return nil
		case failure := <-errs:
			return failure
		case result := <-controlResults:
			if ctx.Err() != nil {
				for _, runtime := range result.added {
					runtime.close()
				}
				return nil
			}
			var setTargets func([]string) error
			if updater, ok := handle.(interface{ SetTargets([]string) error }); ok {
				setTargets = updater.SetTargets
			}
			controlErr := registry.commit(result, setTargets)
			status.ControlRevision, status.ControlError = result.control.Revision, ""
			if controlErr != nil {
				status.ControlError = controlErr.Error()
			}
			controlBusy = false
		case <-ticker.C:
			if !leaseValid(directory, time.Now()) {
				return nil
			}
			if !request.Continuous && status.Stats.Failed > 0 {
				return errors.New("代理转发出现失败，测试停止；请恢复设备原来的 IP 和 DNS 设置")
			}
			if !controlBusy {
				if control, ok := readDeviceControl(directory, seenRevision); ok {
					seenRevision, controlBusy = control.Revision, true
					previous := registry.load()
					workers.Add(1)
					go func() {
						defer workers.Done()
						result := prepareControl(ctx, control, previous, buildAdded)
						select {
						case controlResults <- result:
						case <-ctx.Done():
							for _, runtime := range result.added {
								runtime.close()
							}
						}
					}()
				}
			}
		}
	}
}

func runtimeStatus(runtime *multiGatewayRuntime, now time.Time) DeviceStatus {
	stats := runtime.engine.Snapshot()
	runtime.mu.RLock()
	bound, mac := runtime.bound, runtime.mac
	runtime.mu.RUnlock()
	elapsed := now.Sub(runtime.sampled).Seconds()
	status := DeviceStatus{
		ID: runtime.id, DeviceIP: runtime.plan.DeviceIP, DeviceMAC: mac,
		Received: runtime.received.Load(), Sent: runtime.sent.Load(),
		ARP: runtime.arp.Load(), IPv4: runtime.ipv4.Load(), Forwarded: runtime.forwarded.Load(),
		Stats: stats,
	}
	if elapsed > 0 {
		status.UploadBPS = float64(stats.Uploaded-runtime.previous.Uploaded) / elapsed
		status.DownloadBPS = float64(stats.Downloaded-runtime.previous.Downloaded) / elapsed
	}
	runtime.previous, runtime.sampled = stats, now
	if !bound {
		status.Phase, status.Message = "waiting", "等待设备填写网络参数"
	} else if stats.Downloaded > 0 {
		status.Phase, status.Message = "connected", "已连接 · 加速中"
	} else {
		status.Phase, status.Message = "identified", "已连接 · 正在建立转发"
	}
	return status
}

func addRelayStats(total *relay.Stats, value relay.Stats) {
	total.TCPStarted += value.TCPStarted
	total.UDPStarted += value.UDPStarted
	total.UDPAssociations += value.UDPAssociations
	total.Failed += value.Failed
	total.Uploaded += value.Uploaded
	total.Downloaded += value.Downloaded
	total.ProxyRead += value.ProxyRead
	total.DeviceRead += value.DeviceRead
	total.DownloadWriteMillis += value.DownloadWriteMillis
	if value.LongestDownloadWriteMillis > total.LongestDownloadWriteMillis {
		total.LongestDownloadWriteMillis = value.LongestDownloadWriteMillis
	}
	total.TCPRetransmits += value.TCPRetransmits
	total.TCPTimeouts += value.TCPTimeouts
	total.TCPSendErrors += value.TCPSendErrors
	total.TCPChecksumErrors += value.TCPChecksumErrors
	total.TCPResetsReceived += value.TCPResetsReceived
	total.PendingPackets += value.PendingPackets
}

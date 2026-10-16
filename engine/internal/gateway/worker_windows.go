package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"accelerator/engine/internal/network"
	"accelerator/engine/internal/npcap"
	"accelerator/engine/internal/packet"
	"accelerator/engine/internal/probe"
	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/socks"
	"accelerator/engine/internal/winpkfilter"

	"golang.org/x/sys/windows"
)

type Request struct {
	Mode       string              `json:"mode"`
	Plan       network.PlanRequest `json:"plan"`
	Confirmed  bool                `json:"confirmed"`
	Seconds    int                 `json:"seconds"`
	Expected   Expected            `json:"expected"`
	Continuous bool                `json:"continuous"`
	Devices    []DeviceRequest     `json:"devices,omitempty"`
}

type DeviceRequest struct {
	ID       string              `json:"id"`
	Plan     network.PlanRequest `json:"plan"`
	Expected Expected            `json:"expected"`
}

type Expected struct {
	AdapterGUID   string `json:"adapterGUID"`
	AdapterIP     string `json:"adapterIP"`
	AdapterMAC    string `json:"adapterMAC"`
	AdapterPrefix int    `json:"adapterPrefix"`
	Router        string `json:"router"`
	DeviceIP      string `json:"deviceIP"`
	Gateway       string `json:"gateway"`
}

func matchesExpected(r Request, p network.ManualPlan) bool {
	return matchesDeviceExpected(r.Expected, r.Mode, p)
}

func matchesDeviceExpected(e Expected, mode string, p network.ManualPlan) bool {
	a := p.Adapter
	return e.AdapterGUID != "" && strings.EqualFold(e.AdapterGUID, a.GUID) &&
		e.AdapterIP == a.IPv4 && strings.EqualFold(e.AdapterMAC, a.MAC) &&
		e.AdapterPrefix == a.Prefix && e.Router == a.Gateway &&
		(mode != "gateway" || (e.DeviceIP == p.DeviceIP && e.Gateway == p.Gateway))
}

type Status struct {
	Phase           string              `json:"phase"`
	Message         string              `json:"message"`
	DeviceIP        string              `json:"deviceIP,omitempty"`
	DeviceMAC       string              `json:"deviceMAC,omitempty"`
	Received        uint64              `json:"received"`
	Sent            uint64              `json:"sent"`
	ARP             uint64              `json:"arp"`
	IPv4            uint64              `json:"ipv4"`
	Forwarded       uint64              `json:"forwarded"`
	SecondsLeft     int                 `json:"secondsLeft"`
	Stats           relay.Stats         `json:"stats"`
	Capture         npcap.CaptureStats  `json:"capture"`
	Transmit        npcap.TransmitStats `json:"transmit"`
	PendingFrames   int                 `json:"pendingFrames"`
	TraceSaved      bool                `json:"traceSaved"`
	UploadBPS       float64             `json:"uploadBytesPerSecond"`
	DownloadBPS     float64             `json:"downloadBytesPerSecond"`
	PassivePassed   bool                `json:"passivePassed"`
	IdentityMethod  string              `json:"identityMethod,omitempty"`
	Stopped         bool                `json:"stopped"`
	Continuous      bool                `json:"continuous"`
	Elapsed         int                 `json:"elapsed"`
	Devices         []DeviceStatus      `json:"devices,omitempty"`
	ControlRevision int64               `json:"controlRevision"`
	ControlError    string              `json:"controlError,omitempty"`
}

type DeviceStatus struct {
	ID          string      `json:"id"`
	Phase       string      `json:"phase"`
	Message     string      `json:"message"`
	DeviceIP    string      `json:"deviceIP,omitempty"`
	DeviceMAC   string      `json:"deviceMAC,omitempty"`
	Received    uint64      `json:"received"`
	Sent        uint64      `json:"sent"`
	ARP         uint64      `json:"arp"`
	IPv4        uint64      `json:"ipv4"`
	Forwarded   uint64      `json:"forwarded"`
	Stats       relay.Stats `json:"stats"`
	UploadBPS   float64     `json:"uploadBytesPerSecond"`
	DownloadBPS float64     `json:"downloadBytesPerSecond"`
}

type packetIO interface {
	Read(context.Context) ([]byte, error)
	Send([]byte) error
}

type captureHandle interface {
	packetIO
	Close()
}

var replaceFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func replaceStatusSnapshot(temporary, destination string) error {
	target, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	source, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	result, _, callErr := replaceFile.Call(uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(source)), 0, 0, 0, 0)
	runtime.KeepAlive(target)
	runtime.KeepAlive(source)
	if result != 0 {
		return nil
	}
	if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
		// The first snapshot has no destination to replace.
		return windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING)
	}
	return callErr
}

func writeStatus(directory string, status Status) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, "status-*.tmp")
	if err != nil {
		return errors.New("无法创建加速状态文件，请检查目录写入权限")
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("无法写入加速状态文件，请检查可用磁盘空间和目录权限")
	}
	// Windows readers or scanners can briefly deny replacement. Retain the
	// previous complete snapshot and retry without deleting the destination.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		err = replaceStatusSnapshot(temp.Name(), filepath.Join(directory, "status.json"))
		if err == nil {
			return nil
		}
		transient := errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
		if !transient || !time.Now().Before(deadline) {
			return errors.New("无法更新加速状态文件；文件持续被占用或目录不可写，已停止网关")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func loadRequest(path string) (Request, error) {
	var r Request
	info, err := os.Stat(path)
	if err != nil || info.Size() > 65536 {
		return r, errors.New("测试请求不存在或过大")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, errors.New("测试请求格式无效")
	}
	if r.Mode != "passive" && r.Mode != "gateway" {
		return r, errors.New("未知测试模式")
	}
	validDuration := r.Seconds >= 1 && r.Seconds <= 180 && (r.Mode != "passive" || r.Seconds <= 20)
	if r.Continuous {
		validDuration = r.Mode == "gateway" && r.Seconds == 0
	}
	if !r.Confirmed || !validDuration {
		return r, errors.New("需明确确认；测试限制 180 秒，持续加速仅允许网关模式")
	}
	if len(r.Devices) > 0 {
		if r.Mode != "gateway" || len(r.Devices) > 8 {
			return r, errors.New("设备加速仅支持 1 到 8 台设备的网关模式")
		}
		ids := make(map[string]struct{}, len(r.Devices))
		for _, device := range r.Devices {
			if strings.TrimSpace(device.ID) == "" {
				return r, errors.New("多设备请求缺少设备标识")
			}
			if _, exists := ids[device.ID]; exists {
				return r, errors.New("多设备请求包含重复设备标识")
			}
			ids[device.ID] = struct{}{}
		}
	}
	return r, nil
}

func leaseValid(directory string, now time.Time) bool {
	if _, err := os.Stat(filepath.Join(directory, "stop")); err == nil {
		return false
	}
	info, err := os.Stat(filepath.Join(directory, "lease"))
	return err == nil && now.Sub(info.ModTime()) <= 8*time.Second && info.ModTime().Sub(now) <= 5*time.Second
}

// Run is reachable only through the explicit elevated worker entry point.
// Bounded tests have a separate wall-clock limit. All sessions require a GUI
// heartbeat. No OS routing or PS5 configuration is changed here.
func Run(path string) (returned error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	status := Status{Phase: "preparing", Message: "正在检查网卡与目标"}
	defer func() {
		status.Stopped, status.SecondsLeft = true, 0
		if returned != nil {
			status.Phase, status.Message = "failed", returned.Error()
		} else if status.Phase != "complete" {
			status.Phase = "stopped"
			status.Message = "加速已停止；若已修改设备，请恢复原来的 IP 和 DNS 设置"
		}
		if err := writeStatus(directory, status); returned == nil && err != nil {
			returned = err
		}
	}()
	request, err := loadRequest(path)
	if err != nil {
		return err
	}
	if !leaseValid(directory, time.Now()) {
		return errors.New("界面连接已失效，未打开网卡")
	}
	if err := writeStatus(directory, status); err != nil {
		return err
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if request.Continuous {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(request.Seconds)*time.Second)
	}
	defer cancel()
	snapshot, err := network.Inspect(ctx)
	if err != nil {
		return errors.New("无法重新检查网络状态")
	}
	if len(request.Devices) > 0 {
		return runMultiGateway(ctx, cancel, directory, snapshot, request, &status)
	}
	planRequest := request.Plan
	if request.Mode == "passive" {
		// Passive reception neither forwards IPv4 nor touches IPv6.
		planRequest.IPv6Absent = true
	}
	plan := network.BuildManualPlan(snapshot, planRequest)
	if !plan.Prepared {
		return errors.New("网卡、主机身份或 IPv6 条件未满足，未打开网卡")
	}
	if !matchesExpected(request, plan) {
		return errors.New("网络或预览参数已变化，请重新准备；未打开网卡")
	}
	if request.Mode == "gateway" {
		result := probe.Run(ctx, plan.Proxy)
		if !result.TCP || !result.UDP {
			return errors.New("代理 TCP/UDP 检查未通过，未启用网关")
		}
	}
	if ctx.Err() != nil || !leaseValid(directory, time.Now()) {
		return errors.New("准备超时或界面已关闭，未打开网卡")
	}
	status.DeviceMAC, status.DeviceIP = plan.DeviceMAC, plan.DeviceIP
	status.Continuous = request.Continuous
	if request.Mode == "passive" {
		status.DeviceIP = plan.OriginalIP
		resolved, err := network.ResolveMAC(plan.OriginalIP, plan.Adapter.IPv4)
		if err != nil {
			return fmt.Errorf("无法通过 ARP 找到设备；请保持原网络设置并运行一次网络测试: %w", err)
		}
		if !strings.EqualFold(resolved.String(), plan.DeviceMAC) {
			return fmt.Errorf("ARP 找到的设备 MAC 是 %s，不是已填写的 %s", resolved, plan.DeviceMAC)
		}
		status.Phase = "complete"
		status.Message = "已通过 Windows ARP 确认设备；无线网卡不保证能旁路监听其他客户端帧"
		status.PassivePassed = true
		status.IdentityMethod = "windows-arp"
		if err := writeStatus(directory, status); err != nil {
			return err
		}
		return nil
	}
	filterIP := ""
	if request.Mode == "gateway" {
		filterIP = plan.DeviceIP
	}
	var handle captureHandle
	if plan.Backend == "winpkfilter" {
		handle, err = winpkfilter.Open(plan.Adapter.GUID, plan.Adapter.MAC, filterIP)
	} else if request.Plan.AutoDiscover {
		handle, err = npcap.OpenDiscovery(plan.Adapter.GUID, plan.Adapter.MAC, filterIP)
	} else {
		handle, err = npcap.OpenDuplex(plan.Adapter.GUID, plan.DeviceMAC, filterIP)
	}
	if err != nil {
		return err
	}
	defer handle.Close()
	local, _ := net.ParseMAC(plan.Adapter.MAC)
	device, _ := net.ParseMAC(plan.DeviceMAC)
	if request.Plan.AutoDiscover {
		device, err = waitForClient(ctx, directory, handle, plan, &status)
		if err != nil || len(device) == 0 {
			return err
		}
	}
	status.DeviceMAC = device.String()
	g := packet.ManualGateway{Address: netip.MustParseAddr(plan.Gateway), Target: packet.Target{
		LocalMAC: local, DeviceMAC: device,
		DeviceIP: netip.MustParseAddr(plan.DeviceIP), GatewayIP: netip.MustParseAddr(plan.Adapter.Gateway),
	}}
	if err := g.Validate(); err != nil {
		return errors.New("网关信息不完整，未发送数据")
	}
	engine, err := relay.NewWithDNS(socks.Client{Address: plan.Proxy}, g.Address, plan.UpstreamDNS)
	if err != nil {
		return err
	}
	defer engine.Close()
	return runGateway(ctx, cancel, directory, handle, engine, g, plan.Adapter, &status)
}

func waitForClient(ctx context.Context, directory string, h packetIO, plan network.ManualPlan, status *Status) (net.HardwareAddr, error) {
	ctx, cancelWaiting := context.WithCancel(ctx)
	defer cancelWaiting()
	leaseDone := watchLease(ctx, cancelWaiting, directory)
	defer func() { cancelWaiting(); <-leaseDone }()
	status.Phase, status.Message = "waiting", "网关已就绪，请在设备填写本次四项参数；正在等待连接"
	local, _ := net.ParseMAC(plan.Adapter.MAC)
	ip, gateway := netip.MustParseAddr(plan.DeviceIP), netip.MustParseAddr(plan.Gateway)
	networkChecks := make(chan bool, 1)
	checksDone := make(chan struct{})
	go func() {
		defer close(checksDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snapshot, err := network.Inspect(ctx)
				found := false
				for _, adapter := range snapshot.Adapters {
					if adapter == plan.Adapter {
						found = true
					}
				}
				select {
				case networkChecks <- err == nil && found:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	defer func() { cancelWaiting(); <-checksDone }()
	for ctx.Err() == nil && leaseValid(directory, time.Now()) {
		if err := writeStatus(directory, *status); err != nil {
			return nil, err
		}
		select {
		case valid := <-networkChecks:
			if !valid {
				return nil, errors.New("物理网卡状态已变化，已停止等待主机")
			}
		default:
		}
		slice, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		frame, err := h.Read(slice)
		expired := slice.Err()
		cancel()
		if err != nil && expired == nil {
			return nil, err
		}
		mac, valid := packet.DiscoverGatewayClient(frame, local, ip, gateway)
		if !valid {
			continue
		}
		if ctx.Err() != nil || !leaseValid(directory, time.Now()) {
			return nil, nil
		}
		status.DeviceMAC, status.IdentityMethod = mac.String(), "configured-gateway-client"
		status.Phase, status.Message = "identified", "已自动识别主机，正在连接加速出口"
		// The first request is answered only after full validation. The normal
		// gateway retains this immutable MAC for every subsequent frame.
		g := packet.ManualGateway{Address: gateway, Target: packet.Target{LocalMAC: local, DeviceMAC: mac, DeviceIP: ip}}
		if reply, ok := g.ReplyARP(frame); ok {
			if err := h.Send(reply); err != nil {
				return nil, err
			}
			status.ARP++
			status.Sent++
		} else if reply, ok := g.ReplyEcho(frame); ok {
			if err := h.Send(reply); err != nil {
				return nil, err
			}
			status.Sent++
		}
		return mac, nil
	}
	return nil, nil
}

func runPassive(ctx context.Context, directory string, h packetIO, plan network.ManualPlan, status *Status) error {
	status.Phase, status.Message = "listening", "只读观察设备；不会发送数据包"
	status.DeviceIP = plan.OriginalIP
	targetMAC, _ := net.ParseMAC(plan.DeviceMAC)
	// Read in short slices to refresh the GUI lease even if PS5 is idle.
	for ctx.Err() == nil {
		if !leaseValid(directory, time.Now()) {
			return nil
		}
		slice, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		frame, err := h.Read(slice)
		sliceError := slice.Err()
		cancel()
		if err != nil && sliceError == nil {
			return err
		}
		if frame != nil && len(frame) >= 14 && bytes.Equal(frame[6:12], targetMAC) {
			switch binary.BigEndian.Uint16(frame[12:14]) {
			case 0x0806:
				if len(frame) >= 42 && bytes.Equal(frame[22:28], targetMAC) {
					status.ARP++
					status.Received++
				}
			case 0x0800:
				if len(frame) >= 34 && frame[14]>>4 == 4 {
					status.IPv4++
					status.Received++
					status.DeviceIP = netip.AddrFrom4([4]byte(frame[26:30])).String()
				}
			}
		}
		if capture, ok := h.(interface{ Stats() npcap.CaptureStats }); ok {
			status.Capture = capture.Stats()
		}
		if err := writeStatus(directory, *status); err != nil {
			return err
		}
	}
	status.Phase = "complete"
	status.PassivePassed = status.Received > 0
	if status.PassivePassed {
		status.IdentityMethod = "captured-source"
	}
	status.Message = "只读观察完成，未发送数据包"
	if !status.PassivePassed {
		status.Message = "未收到设备数据；不能据此判断注入能力，也不能确认设备离线"
	}
	return nil
}

func runGateway(ctx context.Context, cancel context.CancelFunc, directory string, h packetIO, engine *relay.Engine,
	g packet.ManualGateway, original network.Adapter, status *Status) error {
	if ctx.Err() != nil || !leaseValid(directory, time.Now()) {
		return nil
	}
	if status.DeviceMAC != "" {
		status.Phase, status.Message = "identified", "已自动识别主机，正在等待代理返回数据"
	} else {
		status.Phase, status.Message = "waiting", "网关等待设备手动设置；尚未识别连接"
	}
	trace, err := os.OpenFile(filepath.Join(directory, "performance.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	var traceEncoder *json.Encoder
	var traceBytes int64
	if err == nil {
		defer trace.Close()
		traceEncoder = json.NewEncoder(trace)
		status.TraceSaved = true
	}
	var received, sent, arp, ipv4, forwarded atomic.Uint64
	arp.Store(status.ARP)
	sent.Store(status.Sent)
	errs := make(chan error, 3)
	fail := func(err error) {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			select {
			case errs <- err:
			default:
			}
		}
		cancel()
	}
	var batcher *frameBatcher
	if batch, ok := h.(batchPacketIO); ok {
		batcher = newFrameBatcher(ctx, directory, batch, fail)
		defer batcher.Close()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	leaseDone := watchLease(ctx, cancel, directory)
	defer func() {
		cancel()
		<-leaseDone
	}()
	emit := func(frame []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if batcher != nil {
			err = batcher.Send(frame)
		} else {
			if !leaseValid(directory, time.Now()) {
				cancel()
				return context.Canceled
			}
			err = h.Send(frame)
		}
		if err != nil {
			return err
		}
		sent.Add(1)
		return nil
	}
	go func() {
		defer wg.Done()
		for {
			frame, err := h.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					fail(err)
				}
				return
			}
			if ctx.Err() != nil {
				return
			}
			received.Add(1)
			if reply, ok := g.ReplyARP(frame); ok {
				arp.Add(1)
				if err := emit(reply); err != nil {
					fail(err)
					return
				}
				continue
			}
			if reply, ok := g.ReplyEcho(frame); ok {
				if err := emit(reply); err != nil {
					fail(err)
					return
				}
				continue
			}
			if ip, ok := g.Input(frame); ok {
				ipv4.Add(1)
				if err := engine.Inject(ip); err != nil {
					fail(err)
					return
				}
				forwarded.Add(1)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			ip, err := engine.ReadPacket(ctx)
			if err != nil {
				if ctx.Err() == nil {
					fail(errors.New("返回数据读取中断，加速停止"))
				}
				return
			}
			frame, err := g.Target.Output(ip)
			if err != nil {
				fail(err)
				return
			}
			if err := emit(frame); err != nil {
				fail(err)
				return
			}
		}
	}()
	defer func() {
		cancel()
		engine.Close()
		wg.Wait()
	}()
	deadline, bounded := ctx.Deadline()
	startedAt := time.Now()
	sampleAt := time.Now()
	var previous relay.Stats
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	// Read interface properties independently; a slow inventory check must
	// not delay the lease watchdog or the absolute session deadline.
	wg.Add(1)
	go func() {
		defer wg.Done()
		check := time.NewTicker(5 * time.Second)
		defer check.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-check.C:
				current, err := network.Inspect(ctx)
				matched := false
				for _, a := range current.Adapters {
					if a.Index == original.Index && a.GUID == original.GUID && a.Status == "Up" &&
						a.IPv4 == original.IPv4 && a.Prefix == original.Prefix && a.Gateway == original.Gateway && a.MAC == original.MAC && !a.Forwarding {
						matched = true
					}
				}
				if err != nil || !matched {
					if ctx.Err() == nil {
						fail(errors.New("物理网卡状态已变化，加速停止"))
					}
					return
				}
			}
		}
	}()
	if status.Continuous {
		wg.Add(1)
		go func() {
			defer wg.Done()
			check := time.NewTicker(5 * time.Second)
			defer check.Stop()
			failures := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-check.C:
					dialCtx, finish := context.WithTimeout(ctx, 2*time.Second)
					conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", engine.ProxyAddress())
					finish()
					if err == nil {
						conn.Close()
						failures = 0
					} else {
						failures++
					}
					if failures >= 3 && ctx.Err() == nil {
						fail(errors.New("代理入口连续失联，加速已停止；请恢复设备原网络设置"))
						return
					}
				}
			}
		}()
	}
	for {
		status.Received, status.Sent = received.Load(), sent.Load()
		status.ARP, status.IPv4, status.Forwarded = arp.Load(), ipv4.Load(), forwarded.Load()
		status.Stats = engine.Snapshot()
		now := time.Now()
		if elapsed := now.Sub(sampleAt).Seconds(); elapsed > 0 {
			status.UploadBPS = float64(status.Stats.Uploaded-previous.Uploaded) / elapsed
			status.DownloadBPS = float64(status.Stats.Downloaded-previous.Downloaded) / elapsed
		}
		sampleAt, previous = now, status.Stats
		if capture, ok := h.(interface{ Stats() npcap.CaptureStats }); ok {
			status.Capture = capture.Stats()
		}
		if transmit, ok := h.(interface{ TransmitStats() npcap.TransmitStats }); ok {
			status.Transmit = transmit.TransmitStats()
		}
		if batcher != nil {
			status.PendingFrames = batcher.Pending()
		}
		status.Elapsed = int(time.Since(startedAt).Seconds())
		if bounded {
			status.SecondsLeft = max(0, int(time.Until(deadline).Seconds()))
		}
		if status.Stats.Downloaded > 0 {
			status.Phase, status.Message = "connected", "设备已连接 · 加速中"
		} else if status.ARP > 0 || status.IPv4 > 0 {
			status.Phase, status.Message = "identified", "设备已连接，正在等待加速出口返回数据"
		}
		if traceEncoder != nil {
			err := traceEncoder.Encode(struct {
				Time   time.Time `json:"time"`
				Status *Status   `json:"status"`
			}{Time: now.UTC(), Status: status})
			if err != nil {
				traceEncoder = nil
				status.TraceSaved = false
			}
			if position, err := trace.Seek(0, 1); err == nil {
				traceBytes = position
			}
			if traceBytes >= 64*1024*1024 {
				// Keep continuous operation from filling the disk with diagnostics.
				traceEncoder = nil
				status.TraceSaved = false
			}
		}
		if err := writeStatus(directory, *status); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			select {
			case err := <-errs:
				return err
			default:
			}
			return nil
		case err := <-errs:
			return err
		case <-ticker.C:
			if !leaseValid(directory, time.Now()) {
				return nil
			}
			// Any forwarding failure ends the initial bounded test. It does
			// not silently change nodes, routes or proxy policy.
			if !status.Continuous && engine.Snapshot().Failed > 0 {
				return errors.New("代理转发出现失败，测试停止；请恢复设备原来的 IP 和 DNS 设置")
			}
		}
	}
}

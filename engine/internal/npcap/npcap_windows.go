package npcap

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Device struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Inventory struct {
	Version string   `json:"version"`
	Devices []Device `json:"devices"`
}

type api struct {
	dll                          windows.Handle
	version, find, free          uintptr
	create, activate, close      uintptr
	snaplen, promisc, timeout    uintptr
	immediate, bufferSize        uintptr
	next, nonblock, event, stats uintptr
	compile, setfilter, freecode uintptr
	datalink, send               uintptr
	sendQueueTransmit            uintptr
}

func load() (*api, error) {
	if runtime.GOARCH != "amd64" {
		return nil, errors.New("Npcap 桥仅支持 Windows x64")
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	h, err := windows.LoadLibraryEx(filepath.Join(system, "Npcap", "wpcap.dll"), 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, errors.New("无法加载系统 Npcap 运行库")
	}
	a := &api{dll: h}
	for name, pointer := range map[string]*uintptr{
		"pcap_lib_version": &a.version, "pcap_findalldevs": &a.find, "pcap_freealldevs": &a.free,
		"pcap_create": &a.create, "pcap_activate": &a.activate, "pcap_close": &a.close,
		"pcap_set_snaplen": &a.snaplen, "pcap_set_promisc": &a.promisc, "pcap_set_timeout": &a.timeout,
		"pcap_set_immediate_mode": &a.immediate, "pcap_set_buffer_size": &a.bufferSize,
		"pcap_next_ex": &a.next, "pcap_getevent": &a.event, "pcap_stats": &a.stats,
		"pcap_setnonblock": &a.nonblock, "pcap_compile": &a.compile, "pcap_setfilter": &a.setfilter,
		"pcap_freecode": &a.freecode, "pcap_datalink": &a.datalink, "pcap_sendpacket": &a.send,
		"pcap_sendqueue_transmit": &a.sendQueueTransmit,
	} {
		*pointer, err = windows.GetProcAddress(h, name)
		if err != nil {
			windows.FreeLibrary(h)
			return nil, fmt.Errorf("Npcap 缺少接口 %s", name)
		}
	}
	return a, nil
}

type nativeDevice struct {
	Next        *nativeDevice
	Name        *byte
	Description *byte
	Addresses   uintptr
	Flags       uint32
}

func cstring(p *byte) string {
	if p == nil {
		return ""
	}
	var b []byte
	for i := 0; i < 4096; i++ {
		c := *(*byte)(unsafe.Add(unsafe.Pointer(p), i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func enumerate(a *api) (Inventory, error) {
	v, _, _ := syscall.SyscallN(a.version)
	// The DLL owns this address; reinterpret its return slot as a native
	// pointer, never as an offset into Go-managed memory.
	result := Inventory{Version: cstring(*(**byte)(unsafe.Pointer(&v))), Devices: []Device{}}
	var devices *nativeDevice
	var errbuf [256]byte
	code, _, _ := syscall.SyscallN(a.find, uintptr(unsafe.Pointer(&devices)), uintptr(unsafe.Pointer(&errbuf[0])))
	if int32(code) != 0 {
		return result, errors.New("Npcap 网卡枚举失败")
	}
	defer syscall.SyscallN(a.free, uintptr(unsafe.Pointer(devices)))
	for d, count := devices, 0; d != nil && count < 256; d, count = d.Next, count+1 {
		result.Devices = append(result.Devices, Device{Name: cstring(d.Name), Description: cstring(d.Description)})
	}
	return result, nil
}

// Inspect loads only the installed DLL and enumerates interfaces. It never
// opens a capture handle or emits a packet.
func Inspect() (Inventory, error) {
	a, err := load()
	if err != nil {
		return Inventory{}, err
	}
	defer windows.FreeLibrary(a.dll)
	return enumerate(a)
}

type Handle struct {
	a          *api
	handle     uintptr
	mu         sync.Mutex
	waitMu     sync.RWMutex
	event      windows.Handle
	closed     bool
	sendBuffer []byte
}

type CaptureStats struct {
	Available bool   `json:"available"`
	Received  uint32 `json:"received"`
	Dropped   uint32 `json:"dropped"`
}

// Windows pcap_stat contains six 32-bit counters, including three extensions.
type nativeStats struct {
	Received, Dropped, InterfaceDropped uint32
	Captured, Sent, NetworkDropped      uint32
}

type bpfProgram struct {
	Length uint32
	_      uint32
	Data   uintptr
}

type packetHeader struct {
	Seconds, Microseconds int32
	Captured, Length      uint32
}

type sendQueue struct {
	MaxLen uint32
	Len    uint32
	Buffer uintptr
}

func Open(guid, mac string, deviceIP string) (*Handle, error) {
	return open(guid, mac, deviceIP, false, false)
}

func open(guid, mac, deviceIP string, transmitOnly, discover bool) (*Handle, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("网卡读取需要单独的管理员辅助进程")
	}
	m, err := net.ParseMAC(mac)
	if err != nil || len(m) != 6 || m[0]&1 != 0 {
		return nil, errors.New("无效目标 MAC")
	}
	a, err := load()
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			windows.FreeLibrary(a.dll)
		}
	}()
	inventory, err := enumerate(a)
	if err != nil {
		return nil, err
	}
	name := `\Device\NPF_{` + strings.Trim(guid, "{}") + `}`
	found := false
	for _, d := range inventory.Devices {
		if strings.EqualFold(d.Name, name) {
			name, found = d.Name, true
		}
	}
	if !found {
		return nil, errors.New("Npcap 未找到选定物理网卡")
	}
	cs, _ := windows.BytePtrFromString(name)
	var errbuf [256]byte
	raw, _, _ := syscall.SyscallN(a.create, uintptr(unsafe.Pointer(cs)), uintptr(unsafe.Pointer(&errbuf[0])))
	runtime.KeepAlive(cs)
	if raw == 0 {
		return nil, errors.New("Npcap 无法打开选定物理网卡")
	}
	h := &Handle{a: a, handle: raw}
	fail := func(message string) (*Handle, error) {
		syscall.SyscallN(a.close, raw)
		return nil, errors.New(message)
	}
	// Immediate delivery is needed for the PS5's ACKs and game datagrams;
	// a sniffing-oriented buffer timeout unnecessarily holds these packets.
	for _, setting := range []struct {
		function uintptr
		value    uintptr
	}{
		{a.snaplen, 65536}, {a.promisc, 0}, {a.timeout, 10},
		{a.bufferSize, 4 << 20}, {a.immediate, 1},
	} {
		code, _, _ := syscall.SyscallN(setting.function, raw, setting.value)
		if int32(code) != 0 {
			return fail("Npcap 无法设置即时接收参数，停止测试")
		}
	}
	code, _, _ := syscall.SyscallN(a.activate, raw)
	// Positive activation results are warnings; do not silently accept a
	// configuration that differs from the requested capture behavior.
	if int32(code) != 0 {
		return fail("Npcap 无法按请求激活物理网卡，停止测试")
	}
	dlt, _, _ := syscall.SyscallN(a.datalink, raw)
	if dlt != 1 {
		return fail("该网卡没有提供以太网数据帧，停止测试")
	}
	// MAC is parsed before formatting; no user-provided BPF expression is used.
	filter := "ether src " + m.String() + " and (arp or ip)"
	if deviceIP != "" {
		ip := net.ParseIP(deviceIP)
		if ip == nil || ip.To4() == nil {
			return fail("无效 IPv4 过滤地址")
		}
		filter = "ether src " + m.String() + " and (arp or (ip and src host " + ip.String() + "))"
		if discover {
			filter = "(arp and src host " + ip.String() + ") or (ip and src host " + ip.String() + ")"
		}
	} else if discover {
		// All virtual /30 clients send ARP broadcasts or frames addressed to
		// this computer's NIC. One filter can therefore cover every device.
		filter = "(arp or ip) and (ether dst " + m.String() + " or ether dst ff:ff:ff:ff:ff:ff)"
	}
	if transmitOnly {
		// Keep this handle's receive buffer idle. Target reception has its
		// own handle and lock, independent of synchronous native sends.
		filter = "ether src 00:00:00:00:00:00"
	}
	expression, _ := windows.BytePtrFromString(filter)
	var program bpfProgram
	code, _, _ = syscall.SyscallN(a.compile, raw, uintptr(unsafe.Pointer(&program)), uintptr(unsafe.Pointer(expression)), 1, 0xffffffff)
	runtime.KeepAlive(expression)
	if int32(code) != 0 {
		return fail("Npcap 无法编译目标过滤器")
	}
	code, _, _ = syscall.SyscallN(a.setfilter, raw, uintptr(unsafe.Pointer(&program)))
	syscall.SyscallN(a.freecode, uintptr(unsafe.Pointer(&program)))
	if int32(code) != 0 {
		return fail("Npcap 无法限定目标过滤器")
	}
	code, _, _ = syscall.SyscallN(a.nonblock, raw, 1, uintptr(unsafe.Pointer(&errbuf[0])))
	if int32(code) != 0 {
		return fail("Npcap 无法启用可取消的读取模式")
	}
	event, _, _ := syscall.SyscallN(a.event, raw)
	if event == 0 || event == ^uintptr(0) {
		return fail("Npcap 无法提供接收通知，停止测试")
	}
	h.event = windows.Handle(event)
	success = true
	return h, nil
}

func (h *Handle) Read(ctx context.Context) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil, errors.New("Npcap 已关闭")
		}
		var header *packetHeader
		var data *byte
		code, _, _ := syscall.SyscallN(h.a.next, h.handle, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data)))
		var frame []byte
		if int32(code) == 1 && header != nil && data != nil && header.Captured > 0 && header.Captured <= 65536 && header.Captured == header.Length {
			frame = append([]byte(nil), unsafe.Slice(data, int(header.Captured))...)
		}
		h.mu.Unlock()
		if frame != nil {
			return frame, nil
		}
		if int32(code) < 0 {
			return nil, errors.New("Npcap 数据读取失败")
		}
		// Wait only after draining available packets. Protect the Npcap-owned
		// event from Close without holding the lock needed by packet sends.
		h.waitMu.RLock()
		h.mu.Lock()
		closed, event := h.closed, h.event
		h.mu.Unlock()
		if closed {
			h.waitMu.RUnlock()
			return nil, errors.New("Npcap 已关闭")
		}
		_, err := windows.WaitForSingleObject(event, 10)
		h.waitMu.RUnlock()
		if err != nil {
			return nil, errors.New("Npcap 接收通知失败")
		}
	}
}

func (h *Handle) Send(frame []byte) error {
	if len(frame) < 60 || len(frame) > 65536 {
		return errors.New("无效以太网帧长度")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("Npcap 已关闭")
	}
	code, _, _ := syscall.SyscallN(h.a.send, h.handle, uintptr(unsafe.Pointer(&frame[0])), uintptr(len(frame)))
	runtime.KeepAlive(frame)
	if int32(code) != 0 {
		return errors.New("Npcap 发送失败")
	}
	return nil
}

// encodeSendQueue uses the SDK's packed pcap_pkthdr + frame layout. Keep its
// backing allocation between sends instead of allocating a native queue and
// crossing into the DLL once for every packet.
func encodeSendQueue(reuse []byte, frames [][]byte) ([]byte, error) {
	const headerSize = 16
	var total uint64
	for _, frame := range frames {
		if len(frame) < 60 || len(frame) > 65536 {
			return nil, errors.New("无效以太网帧长度")
		}
		total += uint64(len(frame)) + headerSize
	}
	if total > uint64(^uint32(0)) {
		return nil, errors.New("Npcap 批量发送队列过大")
	}
	if uint64(cap(reuse)) < total {
		reuse = make([]byte, int(total))
	} else {
		reuse = reuse[:int(total)]
	}
	offset := 0
	for _, frame := range frames {
		header := reuse[offset : offset+headerSize]
		clear(header[:8]) // Timestamps are ignored for unsynchronized sends.
		binary.LittleEndian.PutUint32(header[8:12], uint32(len(frame)))
		binary.LittleEndian.PutUint32(header[12:16], uint32(len(frame)))
		offset += headerSize
		copy(reuse[offset:], frame)
		offset += len(frame)
	}
	return reuse, nil
}

// SendBatch makes one synchronous native call for the entire burst. Npcap
// waits for completion before returning, so the Go buffer stays alive until
// the driver no longer references it. The handle lock also serializes reuse.
func (h *Handle) SendBatch(frames [][]byte) error {
	if len(frames) == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("Npcap 已关闭")
	}
	data, err := encodeSendQueue(h.sendBuffer, frames)
	if err != nil {
		return err
	}
	h.sendBuffer = data
	queue := sendQueue{
		MaxLen: uint32(cap(data)), Len: uint32(len(data)),
		Buffer: uintptr(unsafe.Pointer(&data[0])),
	}
	sent, _, _ := syscall.SyscallN(h.a.sendQueueTransmit, h.handle, uintptr(unsafe.Pointer(&queue)), 0)
	runtime.KeepAlive(data)
	if uint32(sent) != queue.Len {
		return errors.New("Npcap 批量发送未完成")
	}
	return nil
}

func (h *Handle) Stats() CaptureStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return CaptureStats{}
	}
	var counters nativeStats
	code, _, _ := syscall.SyscallN(h.a.stats, h.handle, uintptr(unsafe.Pointer(&counters)))
	if int32(code) != 0 {
		return CaptureStats{}
	}
	return CaptureStats{Available: true, Received: counters.Received, Dropped: counters.Dropped}
}

func (h *Handle) Close() {
	h.waitMu.Lock()
	defer h.waitMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		syscall.SyscallN(h.a.close, h.handle)
		h.sendBuffer = nil
		windows.FreeLibrary(h.a.dll)
	}
}

type TransmitStats struct {
	Batches       uint64  `json:"batches"`
	Frames        uint64  `json:"frames"`
	TotalMillis   float64 `json:"totalMillis"`
	LongestMillis float64 `json:"longestMillis"`
}

// Duplex uses separate pcap handles: a driver send must never hold the lock
// needed to receive TCP ACKs or report capture statistics.
type Duplex struct {
	receive *Handle
	send    *Handle
	batches atomic.Uint64
	frames  atomic.Uint64
	nanos   atomic.Uint64
	longest atomic.Uint64
}

func OpenDuplex(guid, mac, deviceIP string) (*Duplex, error) {
	return openDuplex(guid, mac, deviceIP, false)
}

func OpenDiscovery(guid, localMAC, deviceIP string) (*Duplex, error) {
	return openDuplex(guid, localMAC, deviceIP, true)
}

// OpenMultiDiscovery shares one receive/send pair among all configured
// virtual /30 clients. It is deliberately separate from the single-device
// entry point so its filter cannot change the existing fast path.
func OpenMultiDiscovery(guid, localMAC string) (*Duplex, error) {
	return openDuplex(guid, localMAC, "", true)
}

func openDuplex(guid, mac, deviceIP string, discover bool) (*Duplex, error) {
	receive, err := open(guid, mac, deviceIP, false, discover)
	if err != nil {
		return nil, err
	}
	send, err := open(guid, mac, deviceIP, true, false)
	if err != nil {
		receive.Close()
		return nil, err
	}
	return &Duplex{receive: receive, send: send}, nil
}

func (d *Duplex) Read(ctx context.Context) ([]byte, error) { return d.receive.Read(ctx) }

func (d *Duplex) record(start time.Time, frames int, err error) error {
	elapsed := uint64(time.Since(start))
	d.batches.Add(1)
	d.nanos.Add(elapsed)
	for previous := d.longest.Load(); elapsed > previous; previous = d.longest.Load() {
		if d.longest.CompareAndSwap(previous, elapsed) {
			break
		}
	}
	if err == nil {
		d.frames.Add(uint64(frames))
	}
	return err
}

func (d *Duplex) Send(frame []byte) error {
	start := time.Now()
	return d.record(start, 1, d.send.Send(frame))
}

func (d *Duplex) SendBatch(frames [][]byte) error {
	start := time.Now()
	return d.record(start, len(frames), d.send.SendBatch(frames))
}

func (d *Duplex) Stats() CaptureStats { return d.receive.Stats() }

func (d *Duplex) TransmitStats() TransmitStats {
	return TransmitStats{
		Batches: d.batches.Load(), Frames: d.frames.Load(),
		TotalMillis:   float64(d.nanos.Load()) / float64(time.Millisecond),
		LongestMillis: float64(d.longest.Load()) / float64(time.Millisecond),
	}
}

func (d *Duplex) Close() {
	d.receive.Close()
	d.send.Close()
}

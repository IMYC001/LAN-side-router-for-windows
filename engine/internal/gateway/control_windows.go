package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
)

type deviceControl struct {
	Revision int64           `json:"revision"`
	Devices  []DeviceRequest `json:"devices"`
}

type runtimeSet struct {
	devices []*multiGatewayRuntime
}

// Only the control loop publishes sets. The packet pump reads one immutable
// slice per frame, without an extra packet-path mutex or filesystem access.
type runtimeRegistry struct {
	current atomic.Pointer[runtimeSet]
}

func (r *runtimeRegistry) load() []*multiGatewayRuntime { return r.current.Load().devices }

func validateControl(control deviceControl, retained []*multiGatewayRuntime) error {
	if control.Revision < 1 || len(control.Devices) < 1 || len(control.Devices) > 8 {
		return errors.New("设备变更请求无效")
	}
	ids, slots := map[string]bool{}, map[int]bool{}
	for _, device := range control.Devices {
		if device.ID == "" || ids[device.ID] || device.Plan.Slot < 0 || device.Plan.Slot >= 8 || slots[device.Plan.Slot] {
			return errors.New("设备标识或网络槽位重复或无效")
		}
		ids[device.ID], slots[device.Plan.Slot] = true, true
		for _, runtime := range retained {
			if runtime.id == device.ID && !reflect.DeepEqual(runtime.request, device) {
				return errors.New("请先停止此设备，再更改它的节点或网络参数")
			}
		}
	}
	return nil
}

func readDeviceControl(directory string, after int64) (deviceControl, bool) {
	path := filepath.Join(directory, "control.json")
	info, err := os.Stat(path)
	if err != nil || info.Size() > 65536 {
		return deviceControl{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return deviceControl{}, false
	}
	var control deviceControl
	if json.Unmarshal(data, &control) != nil || control.Revision <= after {
		return deviceControl{}, false
	}
	return control, true
}

type preparedControl struct {
	control deviceControl
	next    []*multiGatewayRuntime
	added   []*multiGatewayRuntime
	err     error
}

func prepareControl(ctx context.Context, control deviceControl, previous []*multiGatewayRuntime,
	build func(context.Context, DeviceRequest) (*multiGatewayRuntime, error)) preparedControl {
	result := preparedControl{control: control}
	if result.err = validateControl(control, previous); result.err != nil {
		return result
	}
	existing := map[string]*multiGatewayRuntime{}
	for _, runtime := range previous {
		existing[runtime.id] = runtime
	}
	for _, device := range control.Devices {
		if result.err = ctx.Err(); result.err != nil {
			break
		}
		runtime := existing[device.ID]
		if runtime == nil {
			runtime, result.err = build(ctx, device)
			if result.err != nil {
				break
			}
			result.added = append(result.added, runtime)
		}
		result.next = append(result.next, runtime)
	}
	if result.err == nil {
		result.err = ctx.Err()
	}
	if result.err != nil {
		for _, runtime := range result.added {
			runtime.close()
		}
		result.added = nil
	}
	return result
}

func (r *runtimeRegistry) commit(result preparedControl, setTargets func([]string) error) error {
	if result.err != nil {
		return result.err
	}
	addresses := make([]string, 0, len(result.next))
	keep := map[*multiGatewayRuntime]bool{}
	for _, runtime := range result.next {
		addresses = append(addresses, runtime.plan.DeviceIP)
		keep[runtime] = true
	}
	if setTargets != nil {
		if err := setTargets(addresses); err != nil {
			for _, runtime := range result.added {
				runtime.close()
			}
			return err
		}
	}
	previous := r.load()
	r.current.Store(&runtimeSet{devices: result.next})
	for _, runtime := range previous {
		if !keep[runtime] {
			runtime.close()
		}
	}
	return nil
}

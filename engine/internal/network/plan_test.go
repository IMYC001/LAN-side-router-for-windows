package network

import (
	"testing"
	"time"
)

func testSnapshot() Snapshot {
	return Snapshot{
		Captured: time.Now().Format(time.RFC3339), Npcap: true,
		Adapters: []Adapter{{Name: "Ethernet", Index: 1, Physical: true, Status: "Up", MAC: "02-00-00-00-00-01", IPv4: "192.168.1.20", Prefix: 24, Gateway: "192.168.1.1"}},
		Neighbors: []Neighbor{
			{IP: "192.168.1.1", MAC: "02-00-00-00-00-03", Index: 1},
			{IP: "192.168.1.35", MAC: "02-00-00-00-00-02", Index: 1},
		},
	}
}

func testRequest() PlanRequest {
	return PlanRequest{AdapterIndex: 1, DeviceIP: "192.168.1.35", DeviceMAC: "02-00-00-00-00-02", Proxy: "127.0.0.1:7890", IPv6Absent: true}
}

func TestExecutionAlwaysLocked(t *testing.T) {
	p := BuildPlan(testSnapshot(), testRequest())
	if p.CanStart || p.NetworkWrites != 0 {
		t.Fatal("preflight enabled live execution")
	}
}

func TestUnsafeTargetsBlocked(t *testing.T) {
	for _, address := range []string{"192.168.1.20", "192.168.1.1", "192.168.1.0", "192.168.1.255", "192.168.2.35", "::1"} {
		request := testRequest()
		request.DeviceIP = address
		plan := BuildPlan(testSnapshot(), request)
		found := false
		for _, check := range plan.Checks {
			if check.Name == "设备地址" && check.Status == "block" {
				found = true
			}
		}
		if !found {
			t.Fatalf("unsafe target %s was not blocked", address)
		}
	}
}

func TestVirtualAdapterAndIPv6AreBlocked(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Adapters[0].Physical = false
	request := testRequest()
	request.IPv6Absent = false
	plan := BuildPlan(snapshot, request)
	for _, name := range []string{"物理网卡", "PS5 IPv6"} {
		found := false
		for _, check := range plan.Checks {
			if check.Name == name && check.Status == "block" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s was not blocked", name)
		}
	}
}

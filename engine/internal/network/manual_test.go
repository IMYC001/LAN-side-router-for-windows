package network

import (
	"net/netip"
	"testing"
)

func TestManualParametersAndSubnetConflict(t *testing.T) {
	s := testSnapshot()
	s.Adapters = append(s.Adapters, Adapter{IPv4: "10.254.0.1", Prefix: 16})
	r := testRequest()
	r.Slot = 1
	p := BuildManualPlan(s, r)
	if !p.Prepared || p.NetworkWrites != 0 || p.Netmask != "255.255.255.252" ||
		p.DeviceIP != "10.253.84.2" || p.Gateway != "10.253.84.1" || p.DNS != p.Gateway {
		t.Fatalf("unexpected plan: %+v", p)
	}
}

func TestManualDeviceSlotsUseDistinctStableSubnets(t *testing.T) {
	s := testSnapshot()
	seen := map[string]bool{}
	for slot := 0; slot < 8; slot++ {
		r := testRequest()
		r.Slot = slot
		p := BuildManualPlan(s, r)
		if !p.Prepared || p.Gateway == "" || seen[p.Gateway] {
			t.Fatalf("slot %d did not receive a unique plan: %+v", slot, p)
		}
		seen[p.Gateway] = true
	}
	r := testRequest()
	r.Slot = 8
	if BuildManualPlan(s, r).Prepared {
		t.Fatal("unsupported device slot was accepted")
	}
}

func TestManualSlotDoesNotBorrowAnotherDeviceSubnet(t *testing.T) {
	s := testSnapshot()
	s.Adapters = append(s.Adapters, Adapter{IPv4: "10.254.83.1", Prefix: 30})
	p := BuildManualPlan(s, testRequest())
	if p.Prepared {
		t.Fatalf("slot zero borrowed another device subnet: %+v", p)
	}
	r := testRequest()
	r.Slot = 1
	p = BuildManualPlan(s, r)
	if !p.Prepared || p.Gateway != "10.253.84.1" {
		t.Fatalf("unrelated device slot changed unexpectedly: %+v", p)
	}
}

func TestManualDoesNotUseStaleNeighborAsIdentity(t *testing.T) {
	s := testSnapshot()
	s.Neighbors = nil
	if !BuildManualPlan(s, testRequest()).Prepared {
		t.Fatal("user-confirmed MAC cannot depend on the old IP neighbor entry")
	}
	r := testRequest()
	r.DeviceMAC = "ff:ff:ff:ff:ff:ff"
	if BuildManualPlan(s, r).Prepared {
		t.Fatal("broadcast identity accepted")
	}
	r = testRequest()
	r.IPv6Absent = false
	if BuildManualPlan(s, r).Prepared {
		t.Fatal("unconfirmed IPv6 accepted")
	}
}

func TestManualAllSubnetsConflict(t *testing.T) {
	s := testSnapshot()
	for _, prefix := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		p := netip.MustParsePrefix(prefix)
		s.Adapters = append(s.Adapters, Adapter{IPv4: p.Addr().Next().String(), Prefix: p.Bits()})
	}
	if BuildManualPlan(s, testRequest()).Prepared {
		t.Fatal("overlapping subnet accepted")
	}
}

func TestManualIPv4TestDoesNotClaimIPv6Absent(t *testing.T) {
	r := testRequest()
	r.IPv6Absent = false
	r.IPv6UnverifiedAccepted = true
	p := BuildManualPlan(testSnapshot(), r)
	if !p.Prepared {
		t.Fatal("explicitly accepted IPv4-only test blocked")
	}
	for _, check := range p.Checks {
		if check.Name == "设备 IPv6" && check.Status == "review" &&
			check.Detail == "用户接受仅 IPv4 的限时测试；公网 IPv6 和绕行尚未验证。" {
			return
		}
	}
	t.Fatal("IPv6 uncertainty was not retained")
}

func TestAutomaticPlanHasNoMachineIdentityAndNpcapIsOptional(t *testing.T) {
	s, r := testSnapshot(), testRequest()
	r.DeviceIP, r.DeviceMAC, r.AutoDiscover = "", "", true
	s.Npcap, s.WinpkFilter = false, true
	p := BuildManualPlan(s, r)
	if !p.Prepared || p.DeviceMAC != "" || p.OriginalIP != "" || p.Backend != "winpkfilter" || p.NetworkWrites != 0 {
		t.Fatalf("automatic portable plan rejected: %+v", p)
	}
	s.WinpkFilter = false
	if BuildManualPlan(s, r).Prepared {
		t.Fatal("plan promised Ethernet without either backend")
	}
	s.Npcap = true
	if p := BuildManualPlan(s, r); !p.Prepared || p.Backend != "npcap" {
		t.Fatal("existing working backend lost")
	}
}

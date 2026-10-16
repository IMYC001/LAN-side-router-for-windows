package nodelatency

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"accelerator/engine/internal/subscription"
)

func TestSamplesExcludeResolutionAndUseMedian(t *testing.T) {
	count := 0
	endpoint := subscription.Endpoint{Server: "fixture.invalid", Port: 443}
	result, err := sample(context.Background(), endpoint,
		func(context.Context, string) ([]netip.Addr, error) {
			time.Sleep(80 * time.Millisecond)
			return []netip.Addr{netip.MustParseAddr("203.0.113.1")}, nil
		},
		func(_ context.Context, ip netip.Addr, port int, udp bool) (time.Duration, error) {
			if ip.String() != "203.0.113.1" || port != 443 || udp {
				t.Fatal("wrong endpoint")
			}
			delays := []time.Duration{40 * time.Millisecond, 350 * time.Millisecond, 42 * time.Millisecond}
			delay := delays[count]
			count++
			return delay, nil
		})
	if err != nil || result.Delay != 42 || result.Method != "TCP" || count != 3 {
		t.Fatalf("expected median 42ms without DNS time: %+v %v", result, err)
	}
}

func TestRejectFakeDNSAndCancelWithoutPublishing(t *testing.T) {
	probes := 0
	resolve := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("198.18.1.1")}, nil
	}
	probe := func(context.Context, netip.Addr, int, bool) (time.Duration, error) { probes++; return 0, nil }
	if _, err := sample(context.Background(), subscription.Endpoint{}, resolve, probe); err == nil || probes != 0 {
		t.Fatal("fake-IP response measured")
	}
	ctx, cancel := context.WithCancel(context.Background())
	resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.1")}, nil
	}
	_, err := sample(ctx, subscription.Endpoint{}, resolve,
		func(context.Context, netip.Addr, int, bool) (time.Duration, error) {
			cancel()
			return time.Millisecond, nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled probe published a result")
	}
}

func TestUDPUsesICMPAndBlockedProbeIsNotZeroLatency(t *testing.T) {
	resolve := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.1")}, nil
	}
	for _, blocked := range []bool{false, true} {
		result, err := sample(context.Background(), subscription.Endpoint{UDPOnly: true}, resolve,
			func(_ context.Context, _ netip.Addr, _ int, udp bool) (time.Duration, error) {
				if !udp {
					t.Fatal("UDP node tested as TCP")
				}
				if blocked {
					return 0, errors.New("blocked")
				}
				return 30 * time.Millisecond, nil
			})
		if blocked && err == nil || !blocked && (err != nil || result.Method != "ICMP" || result.Delay != 30) {
			t.Fatalf("incorrect UDP result: %+v %v", result, err)
		}
	}
}

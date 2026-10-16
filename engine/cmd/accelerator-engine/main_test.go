package main

import (
	"testing"

	"accelerator/engine/internal/subscriptionfetch"
)

func TestNetworkMutationCommandsRefused(t *testing.T) {
	for _, command := range []string{"start", "arp", "capture", "install-driver"} {
		reply := handle(request{Command: command})
		if reply.OK || reply.Error == "" {
			t.Fatalf("%s enabled", command)
		}
	}
}

func TestSubscriptionFetchIPCRefusesUnsafeInput(t *testing.T) {
	reply := handle(request{Command: "fetch-subscription", Fetch: subscriptionfetch.Request{URL: "http://example.invalid"}})
	data, ok := reply.Data.(subscriptionfetch.Result)
	if !reply.OK || !ok || data.Failure != "address" {
		t.Fatal("subscription fetch command or validation unavailable")
	}
}

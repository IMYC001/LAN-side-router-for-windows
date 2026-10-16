package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"accelerator/engine/internal/gateway"
	"accelerator/engine/internal/natprobe"
	"accelerator/engine/internal/network"
	"accelerator/engine/internal/nodelatency"
	"accelerator/engine/internal/npcap"
	"accelerator/engine/internal/probe"
	"accelerator/engine/internal/simulation"
	"accelerator/engine/internal/subscription"
	"accelerator/engine/internal/subscriptionfetch"
)

type request struct {
	Command      string                       `json:"command"`
	Proxy        string                       `json:"proxy"`
	Plan         network.PlanRequest          `json:"plan"`
	Subscription string                       `json:"subscription"`
	Outbound     subscription.OutboundRequest `json:"outbound"`
	Fetch        subscriptionfetch.Request    `json:"fetch"`
	Latency      nodelatency.Request          `json:"latency"`
}

type response struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func handle(r request) response {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var result any
	var err error
	switch r.Command {
	case "diagnostics":
		result, err = network.Inspect(ctx)
	case "probe":
		result = probe.Run(ctx, r.Proxy)
	case "nat-probe":
		result, err = natprobe.Run(ctx, r.Proxy)
	case "selftest":
		result, err = simulation.Run(ctx)
	case "plan":
		var snapshot network.Snapshot
		snapshot, err = network.Inspect(ctx)
		if err == nil {
			result = network.BuildPlan(snapshot, r.Plan)
		}
	case "manual-plan":
		var snapshot network.Snapshot
		snapshot, err = network.Inspect(ctx)
		if err == nil {
			result = network.BuildManualPlan(snapshot, r.Plan)
		}
	case "npcap":
		result, err = npcap.Inspect()
	case "subscription":
		result, err = subscription.Parse([]byte(r.Subscription))
	case "node-latency":
		result, err = nodelatency.Measure(ctx, []byte(r.Subscription), r.Latency)
	case "fetch-subscription":
		result = subscriptionfetch.Fetch(ctx, r.Fetch)
	case "prepare-outbound":
		result, err = subscription.BuildOutbound([]byte(r.Subscription), r.Outbound)
	case "version":
		result = map[string]any{"version": "0.5.13", "runtime": debugInfo(), "networkTakeoverEnabled": false,
			"autoDiscoveryAvailable": true, "winpkFilterAvailable": true, "independentOutboundAvailable": true, "continuousGatewayAvailable": true}
	case "start", "arp", "capture", "install-driver":
		err = errors.New("此命令不提供真实操作；限时测试须通过界面确认并启动独立辅助进程")
	default:
		err = errors.New("未知命令")
	}
	if err != nil {
		return response{OK: false, Error: err.Error()}
	}
	return response{OK: true, Data: result}
}

func debugInfo() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.GoVersion
	}
	return "unknown"
}

func main() {
	if len(os.Args) > 1 {
		if len(os.Args) == 3 && os.Args[1] == "worker" {
			if err := gateway.Run(os.Args[2]); err != nil {
				os.Exit(1)
			}
			return
		}
		if os.Args[1] == "version" {
			json.NewEncoder(os.Stdout).Encode(handle(request{Command: "version"}))
			return
		}
		fmt.Fprintln(os.Stderr, "Use JSON requests on stdin. Bounded tests require an explicit worker request.")
		os.Exit(2)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), subscription.MaxBytes*6+65536)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r request
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			encoder.Encode(response{OK: false, Error: "无效 JSON 请求"})
			continue
		}
		encoder.Encode(handle(r))
	}
}

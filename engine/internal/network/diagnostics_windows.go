package network

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type Adapter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Index       int    `json:"index"`
	GUID        string `json:"guid"`
	MAC         string `json:"mac"`
	Physical    bool   `json:"physical"`
	Status      string `json:"status"`
	IPv4        string `json:"ipv4"`
	Prefix      int    `json:"prefix"`
	Gateway     string `json:"gateway"`
	Forwarding  bool   `json:"forwarding"`
}

type Neighbor struct {
	IP    string `json:"ip"`
	MAC   string `json:"mac"`
	Index int    `json:"index"`
	State string `json:"state"`
}

type Listener struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type Snapshot struct {
	Adapters    []Adapter  `json:"adapters"`
	Neighbors   []Neighbor `json:"neighbors"`
	Listeners   []Listener `json:"listeners"`
	Npcap       bool       `json:"npcap"`
	WinpkFilter bool       `json:"winpkFilter"`
	Admin       bool       `json:"admin"`
	ReadOnly    bool       `json:"readOnly"`
	Captured    string     `json:"captured"`
}

// This script queries networking state; it performs no discovery broadcasts,
// configuration changes, driver loads or packet injection.
const inventory = `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$rows = @()
foreach ($a in @(Get-NetAdapter)) {
  $ip = Get-NetIPAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -notlike '169.254.*' } | Select-Object -First 1
  $gateway = Get-NetRoute -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Sort-Object RouteMetric | Select-Object -First 1
  $iface = Get-NetIPInterface -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1
  $rows += [pscustomobject]@{
    name=$a.Name; description=$a.InterfaceDescription; index=[int]$a.ifIndex;
    guid=[string]$a.InterfaceGuid; mac=[string]$a.MacAddress; physical=[bool]$a.HardwareInterface;
    status=[string]$a.Status; ipv4=[string]$ip.IPAddress; prefix=[int]$ip.PrefixLength;
    gateway=[string]$gateway.NextHop; forwarding=([string]$iface.Forwarding -eq 'Enabled')
  }
}
$neighbors = @(Get-NetNeighbor -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object {
  $_.State -notin @('Unreachable','Incomplete') -and $_.LinkLayerAddress -and $_.LinkLayerAddress -ne '00-00-00-00-00-00'
} | ForEach-Object {
  [pscustomobject]@{ ip=$_.IPAddress; mac=$_.LinkLayerAddress; index=[int]$_.ifIndex; state=[string]$_.State }
})
$listeners = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object {
  $_.LocalPort -in @(1080,7890,7891,7892,7897,10808)
} | ForEach-Object { [pscustomobject]@{ address=$_.LocalAddress; port=[int]$_.LocalPort } })
[pscustomobject]@{ adapters=@($rows); neighbors=@($neighbors); listeners=@($listeners) } | ConvertTo-Json -Depth 5 -Compress
`

func Inspect(ctx context.Context) (Snapshot, error) {
	units := utf16.Encode([]rune(inventory))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	hideWindow(command)
	output, err := command.Output()
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return Snapshot{}, err
	}
	neighbors := make([]Neighbor, 0, len(snapshot.Neighbors))
	for _, n := range snapshot.Neighbors {
		ip, ipErr := netip.ParseAddr(n.IP)
		mac, macErr := net.ParseMAC(n.MAC)
		if ipErr == nil && ip.Is4() && ip.IsPrivate() && macErr == nil && len(mac) == 6 && mac[0]&1 == 0 {
			neighbors = append(neighbors, n)
		}
	}
	snapshot.Neighbors = neighbors
	_, statErr := os.Stat(filepath.Join(os.Getenv("SystemRoot"), "System32", "Npcap", "wpcap.dll"))
	snapshot.Npcap = statErr == nil
	key, keyErr := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\ndisrd`, registry.QUERY_VALUE)
	if keyErr == nil {
		snapshot.WinpkFilter = true
		key.Close()
	}
	snapshot.Admin = windows.GetCurrentProcessToken().IsElevated()
	snapshot.ReadOnly = true
	snapshot.Captured = time.Now().Format(time.RFC3339)
	return snapshot, nil
}

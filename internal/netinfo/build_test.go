package netinfo_test

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/seanmizen/snetwork/internal/collect"
	"github.com/seanmizen/snetwork/internal/fixture"
	"github.com/seanmizen/snetwork/internal/netinfo"
)

// now is the fixed time for every scenario.
var now = time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)

func scenario(t *testing.T, name string) netinfo.Snapshot {
	t.Helper()
	dir := filepath.Join("testdata", name)
	goos := "darwin"
	if name == "linux" {
		goos = "linux"
	}
	return netinfo.Build(collect.Collect(goos, fixture.Runner(dir), fixture.WiFi(dir)), now)
}

func TestHome(t *testing.T) {
	s := scenario(t, "home")
	if len(s.Problems) > 0 {
		t.Fatalf("problems: %v", s.Problems)
	}
	if s.Location.Kind != "lan" || s.Location.Router != "alpha" || s.Location.Label != "LAN of alpha" ||
		s.Location.Address != "10.20.30.102" || s.Location.Interface != "en0" {
		t.Errorf("location = %+v", s.Location)
	}
	wantSubnets := []netinfo.Subnet{
		{Prefix: "10.20.30.0/24", Via: "direct", Interface: "en0", Router: "alpha"},
		{Prefix: "10.40.50.0/24", Via: "vpn", Interface: "utun7", Router: "bravo"},
		{Prefix: "100.64.0.0/10", Via: "vpn", Interface: "utun7", Note: "tailnet"},
	}
	if !reflect.DeepEqual(s.Subnets, wantSubnets) {
		t.Errorf("subnets = %+v", s.Subnets)
	}
	if len(s.Devices) != 2 || s.Devices[0].Count == nil || s.Devices[1].Count != nil || s.Devices[1].Router != "bravo" {
		t.Fatalf("devices = %+v", s.Devices)
	}
	for _, d := range s.Devices[0].Devices {
		if d.IP == s.Location.Address {
			t.Error("this machine is counted as a device")
		}
	}
	if !s.Devices[0].Devices[0].Gateway {
		t.Error("the first device should be the gateway")
	}
	var tsIface netinfo.Interface
	for _, i := range s.Interfaces {
		if i.Name == "utun7" {
			tsIface = i
		}
		if i.Default != (i.Name == "en0") {
			t.Errorf("%s default = %v", i.Name, i.Default)
		}
	}
	if tsIface.Label != "Tailscale" {
		t.Errorf("utun7 label = %q", tsIface.Label)
	}
	if len(s.DHCP) != 1 || s.DHCP[0].Length != 24*time.Hour || s.DHCP[0].Server != "10.20.30.1" {
		t.Errorf("dhcp = %+v", s.DHCP)
	}
	if s.WiFi == nil || !s.WiFi.Associated || s.WiFi.Name != "" || s.WiFi.Channel != 112 {
		t.Errorf("wifi = %+v", s.WiFi)
	}
	if s.VPN.AllTrafficVia != "" || len(s.VPN.Others) != 2 || s.VPN.Tailscale.State != "Running" {
		t.Errorf("vpn = %+v", s.VPN)
	}
	if fw := s.Firewall.Tools[0]; fw.State != "on" || fw.Detail != "stealth off" {
		t.Errorf("firewall = %+v", s.Firewall)
	}
}

func TestHotspotWithFullTunnel(t *testing.T) {
	s := scenario(t, "hotspot")
	if s.Location.Kind != "hotspot" || s.Location.Address != "172.20.10.6" {
		t.Errorf("location = %+v", s.Location)
	}
	if s.VPN.AllTrafficVia != "utun9" {
		t.Errorf("all traffic via = %q", s.VPN.AllTrafficVia)
	}
	// both home LANs are reached through the tailnet, not directly
	via := map[string]string{}
	for _, sn := range s.Subnets {
		via[sn.Prefix] = sn.Via + " " + sn.Router
	}
	want := map[string]string{
		"172.20.10.0/28": "direct ", "10.20.30.0/24": "vpn alpha", "10.40.50.0/24": "vpn bravo",
		"100.64.0.0/10": "vpn ", "0.0.0.0/0": "vpn ",
	}
	if !reflect.DeepEqual(via, want) {
		t.Errorf("subnets = %v", via)
	}
	if s.Firewall.Tools[0].Detail != "stealth on" {
		t.Errorf("firewall = %+v", s.Firewall)
	}
	if len(s.Listening) != 1 || s.Listening[0].Port != 22 || s.Listening[0].Family != "both" {
		t.Errorf("listening = %+v", s.Listening)
	}
}

func TestBare(t *testing.T) {
	s := scenario(t, "bare")
	if s.VPN.Tailscale.Installed {
		t.Error("tailscale should read as not installed")
	}
	if len(s.Problems) > 0 {
		t.Errorf("a missing tool is not a problem: %v", s.Problems)
	}
	if s.Location.Kind != "other" || s.Location.Interface != "en8" {
		t.Errorf("location = %+v", s.Location)
	}
	if s.WiFi == nil || s.WiFi.PowerOn {
		t.Errorf("wifi = %+v", s.WiFi)
	}
	if n := s.Devices[0].Count; n == nil || *n != 2 {
		t.Errorf("devices = %+v", s.Devices[0])
	}
	if s.Devices[0].Devices[1].Private != true {
		t.Error("02:… is a private address")
	}
	states := map[string]string{}
	for _, i := range s.Interfaces {
		states[i.Name] = i.State
	}
	if states["en0"] != "no link" || states["en8"] != "up" || states["bridge0"] != "disabled" {
		t.Errorf("states = %v", states)
	}
	if !s.DHCP[0].Expires.Before(now) || s.DHCP[0].Domain != "lan.example" {
		t.Errorf("lease = %+v", s.DHCP[0])
	}
}

func TestProblemIsReported(t *testing.T) {
	dir := filepath.Join("testdata", "home")
	failing := func(key string, argv []string) ([]byte, error) {
		if key == "dns" {
			return nil, errBoom
		}
		return fixture.Runner(dir)(key, argv)
	}
	s := netinfo.Build(collect.Collect("darwin", failing, fixture.WiFi(dir)), now)
	if !reflect.DeepEqual(s.Problems, []string{"dns: boom"}) || len(s.DNS.General) != 0 {
		t.Errorf("problems = %v, dns = %+v", s.Problems, s.DNS)
	}
}

type boom struct{}

func (boom) Error() string { return "boom" }

var errBoom = boom{}

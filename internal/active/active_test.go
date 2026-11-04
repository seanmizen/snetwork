package active

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/seanmizen/snetwork/internal/collect"
	"github.com/seanmizen/snetwork/internal/fixture"
	"github.com/seanmizen/snetwork/internal/netinfo"
)

func TestParsers(t *testing.T) {
	if ms, ok := ParsePing("64 bytes from 10.1.2.1: icmp_seq=0 ttl=64 time=4.123 ms"); !ok || ms != 4.123 {
		t.Errorf("ParsePing = %v, %v", ms, ok)
	}
	if _, ok := ParsePing("Request timeout for icmp_seq 0"); ok {
		t.Error("a timeout has no time")
	}
	for out, want := range map[string]string{
		"pong from alpha (100.64.0.11) via 10.1.2.3:41641 in 4ms": "direct 10.1.2.3:41641",
		"pong from bravo (100.64.0.12) via DERP(fra) in 31ms":     "relay fra",
	} {
		if path, _, ok := ParseTailscalePing(out); !ok || path != want {
			t.Errorf("ParseTailscalePing(%q) = %q", out, path)
		}
	}
	trace := " 1  10.1.2.1  3.1 ms\n 2  100.64.0.1  9.0 ms\n 3  192.0.2.1  12 ms\n"
	if got := ParseTraceroute(trace); got != "yes (hop 2: 100.64.0.1)" {
		t.Errorf("ParseTraceroute = %q", got)
	}
	if got := ParseTraceroute(" 1  10.1.2.1  3 ms\n 2  192.0.2.1  9 ms\n"); got != "no" {
		t.Errorf("ParseTraceroute = %q", got)
	}
	if got := ParseTraceroute(" 1  *\n"); got != "unknown: no hops answered" {
		t.Errorf("ParseTraceroute = %q", got)
	}
	if ip := ParseTrace("fl=1\nip=192.0.2.7\nts=1\n")["ip"]; ip != "192.0.2.7" {
		t.Errorf("ParseTrace ip = %q", ip)
	}
	for in, want := range map[string]string{"minix.lan.": "minix", "basecamp": "basecamp", "7.2.0.192.in-addr.arpa.": "", "": ""} {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q", in, got)
		}
	}
}

func TestSweepTargets(t *testing.T) {
	got := SweepTargets("10.1.2.0/24", map[string]bool{"10.1.2.7": true})
	if len(got) != 253 || got[0] != netip.MustParseAddr("10.1.2.1") || got[252] != netip.MustParseAddr("10.1.2.254") {
		t.Errorf("got %d targets, %v … %v", len(got), got[0], got[len(got)-1])
	}
	if got := SweepTargets("172.20.10.0/28", nil); len(got) != 14 {
		t.Errorf("a /28 has 14 hosts, got %d", len(got))
	}
	if got := SweepTargets("172.17.0.0/16", nil); got != nil {
		t.Error("a /16 is never swept")
	}
}

var now = time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)

func load(t *testing.T, name, goos string) (netinfo.Snapshot, netinfo.Raw) {
	dir := filepath.Join("..", "netinfo", "testdata", name)
	raw := collect.Collect(goos, fixture.Runner(dir), fixture.WiFi(dir))
	return netinfo.Build(raw, now), raw
}

func TestPlan(t *testing.T) {
	s, _ := load(t, "home", "darwin")
	Plan(&s)
	a := s.Active
	if len(a.Sweeps) != 1 || a.Sweeps[0].Prefix != "10.20.30.0/24" || a.Sweeps[0].Probed != 254 {
		t.Errorf("sweeps = %+v (never the subnet behind the tailnet)", a.Sweeps)
	}
	if len(a.Gateways) != 1 || a.Gateways[0].Target != "10.20.30.1" {
		t.Errorf("gateways = %+v", a.Gateways)
	}
	if len(a.Peers) != 2 { // alpha and bravo; the phone is offline
		t.Errorf("peers = %+v", a.Peers)
	}
	if len(a.DNS) != 2 || a.DNS[0].Server != "100.100.100.100" || a.DNS[1].Server != "10.20.30.1" {
		t.Errorf("dns = %+v", a.DNS)
	}
	if a.WiFiScan == nil || a.Internet == nil || a.Names == nil {
		t.Error("wifi scan, internet and names should be planned")
	}

	l, _ := load(t, "linux", "linux")
	Plan(&l)
	skipped := map[string]string{}
	for _, sw := range l.Active.Sweeps {
		skipped[sw.Prefix] = sw.Skipped
	}
	if skipped["172.17.0.0/16"] != "larger than a /24" || skipped["192.168.50.0/24"] != "" {
		t.Errorf("sweeps = %+v", l.Active.Sweeps)
	}
	if !l.Active.WiFiScan.Done || l.Active.WiFiScan.Err == "" {
		t.Error("a Linux Wi-Fi scan needs root and is skipped")
	}
}

func TestRun(t *testing.T) {
	s, raw := load(t, "home", "darwin")
	Plan(&s)
	arp, _ := os.ReadFile(filepath.Join("..", "netinfo", "testdata", "home", "arp.txt"))
	f := &Fake{Neighbours: append(arp, []byte("? (10.20.30.150) at 0:16:3e:0:0:99 on en0 ifscope [ethernet]\n")...)}
	var mu sync.Mutex
	shared, sharedRaw := s, raw
	Run(context.Background(), s, f, func(fn func(*netinfo.Snapshot, *netinfo.Raw)) {
		mu.Lock()
		defer mu.Unlock()
		fn(&shared, &sharedRaw)
	})
	if len(f.Swept) != 253 {
		t.Errorf("swept %d addresses", len(f.Swept))
	}
	a := shared.Active
	if !a.Sweeps[0].Done || !a.Gateways[0].Done || !a.Peers[0].Done || !a.DNS[0].Done || !a.Internet.Done || !a.WiFiScan.Done || !a.Names.Done {
		t.Fatalf("not everything finished: %+v", a)
	}
	names := map[string]string{}
	found := false
	for _, d := range shared.Devices[0].Devices {
		names[d.IP] = d.Name + " " + d.NameSource
		found = found || d.IP == "10.20.30.150"
	}
	if !found {
		t.Error("the device the sweep found is missing")
	}
	// the router's DNS outranks mDNS; mDNS fills the gaps
	if names["10.20.30.106"] != "tv dns" || names["10.20.30.107"] != "archer mdns" {
		t.Errorf("names = %v", names)
	}
	if !shared.Devices[0].Swept || shared.Devices[1].Swept {
		t.Error("only the direct subnet is swept")
	}
}

// TestNamesAskTheGateway: with no DNS server from the lease (Tailscale owns
// resolv.conf), the reverse lookups go to the subnet's gateway.
func TestNamesAskTheGateway(t *testing.T) {
	s, raw := load(t, "home", "darwin")
	Plan(&s)
	for i := range s.DHCP {
		s.DHCP[i].DNS = nil
	}
	gw := ""
	for _, d := range s.Devices[0].Devices {
		if d.Gateway {
			gw = d.IP
		}
	}
	f := &Fake{}
	var mu sync.Mutex
	names(context.Background(), f, func(fn func(*netinfo.Snapshot, *netinfo.Raw)) {
		mu.Lock()
		defer mu.Unlock()
		fn(&s, &raw)
	})
	if gw == "" || len(f.DNSAsked) != 1 || f.DNSAsked[0] != gw {
		t.Errorf("asked %v, want the gateway %q", f.DNSAsked, gw)
	}
}

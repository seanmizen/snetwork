// Package active runs the probes behind --active. Each probe sends packets
// but changes nothing: no lease renewal, no reconnect, no tailnet change.
//
// Sweeps stay on subnets this machine sits on, at most a /24, and never
// cross a VPN: ARP does not cross a router, so a sweep through the tailnet
// would be slow and would miss every device that ignores ping.
package active

import (
	"net/netip"
	"strings"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// MaxSweepBits is the smallest prefix length a sweep accepts: a /24 is
// 254 probes, a /16 would be 65,534.
const MaxSweepBits = 24

// LookupName is the name each DNS probe resolves.
const LookupName = "example.com"

// Plan fills s.Active with every probe, all pending. It is pure, so tests
// can check what --active will do without sending anything.
func Plan(s *netinfo.Snapshot) {
	a := &netinfo.Active{Names: &netinfo.NamesProbe{}, Internet: &netinfo.Internet{}}
	for _, g := range s.Devices {
		if g.Via != "direct" {
			continue
		}
		p := netip.MustParsePrefix(g.Prefix)
		sw := netinfo.Sweep{Prefix: g.Prefix}
		if p.Bits() < MaxSweepBits {
			sw.Skipped, sw.Done = "larger than a /24", true
		} else {
			sw.Probed = 1<<(32-p.Bits()) - 2
		}
		a.Sweeps = append(a.Sweeps, sw)
	}
	seen := map[string]bool{}
	for _, d := range s.Defaults {
		if _, err := netip.ParseAddr(d.Gateway); err != nil || seen[d.Gateway] {
			continue
		}
		seen[d.Gateway] = true
		a.Gateways = append(a.Gateways, netinfo.Probe{Target: d.Gateway, Interface: d.Interface})
	}
	if s.VPN.Tailscale.State == "Running" {
		for _, p := range s.VPN.Tailscale.Peers {
			if p.Online {
				a.Peers = append(a.Peers, netinfo.PeerPing{Peer: p.Name})
			}
		}
	}
	seen = map[string]bool{}
	for _, r := range s.DNS.General {
		for _, ns := range r.Nameservers {
			if strings.HasPrefix(ns, "fe80:") || seen[ns] {
				continue
			}
			seen[ns] = true
			a.DNS = append(a.DNS, netinfo.DNSProbe{Server: ns, Name: LookupName})
			break // one server per resolver is enough to time it
		}
	}
	if s.WiFi != nil && s.WiFi.Associated {
		a.WiFiScan = &netinfo.WiFiScan{}
		if s.OS == "linux" {
			a.WiFiScan.Err, a.WiFiScan.Done = "a scan needs root on Linux", true
		}
	}
	s.Active = a
}

// SweepTargets are the host addresses of a prefix: no network or
// broadcast address, and not this machine.
func SweepTargets(prefix string, own map[string]bool) []netip.Addr {
	p, err := netip.ParsePrefix(prefix)
	if err != nil || p.Bits() < MaxSweepBits || !p.Addr().Is4() {
		return nil
	}
	p = p.Masked()
	var out []netip.Addr
	first := p.Addr().Next()
	for a := first; p.Contains(a); a = a.Next() {
		if !p.Contains(a.Next()) {
			break // broadcast
		}
		if !own[a.String()] {
			out = append(out, a)
		}
	}
	return out
}

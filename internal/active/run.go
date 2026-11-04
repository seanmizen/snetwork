package active

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// Prober sends the probes. Real is the real one; tests use a fake.
type Prober interface {
	// Sweep makes the OS resolve every address in targets, so the ARP
	// cache fills. It sends one small UDP datagram per address.
	Sweep(ctx context.Context, targets []netip.Addr) error
	// RereadNeighbours returns the ARP or neighbour table after a sweep,
	// under the source key that Build reads ("arp" or "ip-neigh").
	RereadNeighbours(ctx context.Context) (key string, out []byte, err error)
	Ping(ctx context.Context, target string) (ms float64, err error)
	TailscalePing(ctx context.Context, peer string) (path string, ms float64, err error)
	Resolve(ctx context.Context, server, name string) (answer string, ms float64, err error)
	// ReverseDNS and MDNS return ip -> name for the addresses they could name.
	ReverseDNS(ctx context.Context, server string, ips []string) map[string]string
	MDNS(ctx context.Context, ips []string) map[string]string
	WiFiScan(ctx context.Context, channel int) netinfo.WiFiScan
	Internet(ctx context.Context) netinfo.Internet
}

// Update applies a change to the shared snapshot. The caller holds the lock
// and redraws after each change.
type Update func(func(s *netinfo.Snapshot, raw *netinfo.Raw))

// Run runs every planned probe at once and reports each result through
// update. It returns when all are done. Plan must have run first.
func Run(ctx context.Context, s netinfo.Snapshot, p Prober, update Update) {
	var wg sync.WaitGroup
	goDo := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	a := s.Active
	own := map[string]bool{}
	for _, ifc := range s.Interfaces {
		for _, addr := range ifc.IPv4 {
			if pf, err := netip.ParsePrefix(addr); err == nil {
				own[pf.Addr().String()] = true
			}
		}
	}

	// Sweep, recount, then name what was found.
	goDo(func() {
		var targets []netip.Addr
		for _, sw := range a.Sweeps {
			if sw.Skipped == "" {
				targets = append(targets, SweepTargets(sw.Prefix, own)...)
			}
		}
		if len(targets) > 0 {
			_ = p.Sweep(ctx, targets)
			if key, out, err := p.RereadNeighbours(ctx); err == nil {
				update(func(s *netinfo.Snapshot, raw *netinfo.Raw) {
					raw.Out[key] = out
					netinfo.RebuildDevices(s, *raw)
				})
			}
		}
		update(func(s *netinfo.Snapshot, _ *netinfo.Raw) {
			for i := range s.Active.Sweeps {
				s.Active.Sweeps[i].Done = true
			}
			for i := range s.Devices {
				s.Devices[i].Swept = s.Devices[i].Via == "direct"
			}
		})
		names(ctx, p, update)
	})
	for i, g := range a.Gateways {
		goDo(func() {
			ms, err := p.Ping(ctx, g.Target)
			update(func(s *netinfo.Snapshot, _ *netinfo.Raw) {
				r := &s.Active.Gateways[i]
				r.RTTms, r.Err, r.Done = ms, errText(err), true
			})
		})
	}
	for i, pp := range a.Peers {
		goDo(func() {
			path, ms, err := p.TailscalePing(ctx, pp.Peer)
			update(func(s *netinfo.Snapshot, _ *netinfo.Raw) {
				r := &s.Active.Peers[i]
				r.Path, r.RTTms, r.Err, r.Done = path, ms, errText(err), true
			})
		})
	}
	for i, d := range a.DNS {
		goDo(func() {
			ans, ms, err := p.Resolve(ctx, d.Server, d.Name)
			update(func(s *netinfo.Snapshot, _ *netinfo.Raw) {
				r := &s.Active.DNS[i]
				r.Answer, r.RTTms, r.Err, r.Done = ans, ms, errText(err), true
			})
		})
	}
	if a.WiFiScan != nil && !a.WiFiScan.Done {
		goDo(func() {
			sc := p.WiFiScan(ctx, s.WiFi.Channel)
			sc.Done = true
			update(func(s *netinfo.Snapshot, _ *netinfo.Raw) { s.Active.WiFiScan = &sc })
		})
	}
	goDo(func() {
		in := p.Internet(ctx)
		in.Done = true
		update(func(s *netinfo.Snapshot, _ *netinfo.Raw) { s.Active.Internet = &in })
	})
	wg.Wait()
}

// names asks the router's DNS and mDNS about every device on a direct
// subnet, then stores the answers where Build reads them.
func names(ctx context.Context, p Prober, update Update) {
	var ips []string
	servers := map[string][]string{} // dns server -> ips
	update(func(s *netinfo.Snapshot, _ *netinfo.Raw) {
		dnsFor := map[string]string{}
		for _, l := range s.DHCP {
			if len(l.DNS) > 0 {
				dnsFor[l.Interface] = l.DNS[0]
			}
		}
		for _, g := range s.Devices {
			if g.Via != "direct" {
				continue
			}
			for _, d := range g.Devices {
				ips = append(ips, d.IP)
				ips = append(ips, d.Also...)
				if srv := dnsFor[g.Interface]; srv != "" {
					servers[srv] = append(servers[srv], d.IP)
					servers[srv] = append(servers[srv], d.Also...)
				}
			}
		}
	})
	var mu sync.Mutex
	dnsNames, mdnsNames := map[string]string{}, map[string]string{}
	var wg sync.WaitGroup
	for srv, list := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := p.ReverseDNS(ctx, srv, list)
			mu.Lock()
			for k, v := range got {
				dnsNames[k] = v
			}
			mu.Unlock()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		got := p.MDNS(ctx, ips)
		mu.Lock()
		for k, v := range got {
			mdnsNames[k] = v
		}
		mu.Unlock()
	}()
	wg.Wait()
	update(func(s *netinfo.Snapshot, raw *netinfo.Raw) {
		raw.Out["names-dns"] = nameLines(dnsNames)
		raw.Out["names-mdns"] = nameLines(mdnsNames)
		netinfo.RebuildDevices(s, *raw)
		found := map[string]bool{}
		for k := range dnsNames {
			found[k] = true
		}
		for k := range mdnsNames {
			found[k] = true
		}
		s.Active.Names.Found, s.Active.Names.Done = len(found), true
	})
}

func nameLines(m map[string]string) []byte {
	var lines []string
	for ip, n := range m {
		if n != "" {
			lines = append(lines, ip+" "+n)
		}
	}
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n"))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}

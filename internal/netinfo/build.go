package netinfo

import (
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Raw is the unparsed output of every source, by source key. Package
// collect fills it from this machine; tests fill it from fixture files.
type Raw struct {
	OS  string            // "darwin" or "linux": which parsers apply
	Out map[string][]byte // source key -> output
	// WiFi is CoreWLAN's reading on macOS; nil elsewhere, or with no Wi-Fi.
	WiFi *WiFiRaw
	// Errs holds sources that failed; NotPresent, tools not installed.
	Errs       map[string]string
	NotPresent map[string]bool
}

// S returns one source's output as text.
func (r Raw) S(key string) string { return string(r.Out[key]) }

// parsed is what the OS-specific parsers produce. Build does the rest.
type parsed struct {
	ifs       []Interface
	routes    []route
	arp       []arpEntry
	leases    []Lease
	dns       DNS
	wifi      *WiFi
	firewall  Firewall
	listening []Listener
	vpnOthers []VPNService
}

// hotspot is the subnet that iOS Personal Hotspot always hands out.
var hotspot = netip.MustParsePrefix("172.20.10.0/28")

// tailnetRange is Tailscale's address range.
var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

// Build turns raw output into a Snapshot. It is pure: the same Raw and time
// always give the same Snapshot.
func Build(r Raw, now time.Time) Snapshot {
	s := Snapshot{Taken: now, OS: r.OS}
	for src, err := range r.Errs {
		s.Problems = append(s.Problems, src+": "+err)
	}
	sort.Strings(s.Problems)

	// Tailscale first: interfaces, subnets and location all use it.
	switch {
	case r.NotPresent["tailscale"]:
		s.VPN.Tailscale = Tailscale{Installed: false}
	case len(r.Out["tailscale"]) > 0:
		if ts, err := parseTailscale(r.Out["tailscale"]); err == nil {
			s.VPN.Tailscale = ts
		} else {
			s.Problems = append(s.Problems, "tailscale: "+err.Error())
		}
	}

	var p parsed
	if r.OS == "linux" {
		p = parseLinux(r, s.VPN.Tailscale, now)
	} else {
		p = parseDarwin(r, s.VPN.Tailscale, now)
	}
	s.Interfaces, s.DHCP, s.DNS, s.WiFi = p.ifs, p.leases, p.dns, p.wifi
	s.Firewall, s.Listening = p.firewall, p.listening
	s.VPN.Others = otherVPNs(p.vpnOthers)

	s.Defaults = defaults(p.routes)
	if len(s.Defaults) > 0 {
		for i := range s.Interfaces {
			if s.Interfaces[i].Name == s.Defaults[0].Interface {
				s.Interfaces[i].Default = true
			}
		}
	}
	s.Subnets, s.VPN.AllTrafficVia = buildSubnets(s.Interfaces, p.routes, s.VPN.Tailscale)
	s.Devices = buildDevices(s.Subnets, p.arp, s.Defaults, ownAddrs(s.Interfaces))
	nameDevices(&s, r)
	s.Location = locate(s)
	return s
}

// RebuildDevices recounts devices from a fresh ARP or neighbour table, as
// --active does after a sweep. It keeps answers from remote routers.
func RebuildDevices(s *Snapshot, r Raw) {
	var arp []arpEntry
	if r.OS == "linux" {
		arp = parseNeigh(r.S("ip-neigh"))
	} else {
		arp = parseARP(r.S("arp"))
	}
	fresh := buildDevices(s.Subnets, arp, s.Defaults, ownAddrs(s.Interfaces))
	for i := range fresh {
		for _, old := range s.Devices {
			if old.Prefix != fresh[i].Prefix {
				continue
			}
			if fresh[i].Count == nil {
				fresh[i] = old // a remote router's answer
			} else {
				fresh[i].Swept, fresh[i].Probed = old.Swept, old.Probed
			}
		}
	}
	s.Devices = fresh
	nameDevices(s, r)
}

// defaults returns the default routes, the one in use first. A scoped route
// (I flag on macOS) and a route with a higher metric come after.
func defaults(routes []route) []Route {
	var list []route
	for _, r := range routes {
		if r.prefix.Bits() == 0 {
			list = append(list, r)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		si, sj := strings.Contains(list[i].flags, "I"), strings.Contains(list[j].flags, "I")
		if si != sj {
			return !si
		}
		return list[i].metric < list[j].metric
	})
	out := make([]Route, 0, len(list))
	for _, r := range list {
		out = append(out, Route{Prefix: "default", Gateway: r.gateway, Interface: r.netif, Flags: r.flags})
	}
	return out
}

// buildSubnets lists the subnets this machine sits on, then those it reaches
// through a tunnel. It also returns a tunnel that takes all traffic.
func buildSubnets(ifs []Interface, routes []route, ts Tailscale) ([]Subnet, string) {
	routerFor := func(p netip.Prefix) string {
		for _, peer := range ts.Peers {
			for _, r := range peer.Routes {
				if rp, err := netip.ParsePrefix(r); err == nil && rp.Masked() == p {
					return peer.Name
				}
			}
		}
		return ""
	}
	tunnel := map[string]string{} // interface -> label
	for _, ifc := range ifs {
		if ifc.Kind == "tunnel" {
			tunnel[ifc.Name] = ifc.Label
		}
	}

	var list []Subnet
	have := map[netip.Prefix]bool{}
	for _, ifc := range ifs {
		if ifc.Kind == "loopback" || ifc.Kind == "tunnel" || ifc.Kind == "virtual" {
			continue
		}
		for _, a := range ifc.IPv4 {
			p, err := netip.ParsePrefix(a)
			if err != nil || p.Bits() == 32 || p.Addr().IsLinkLocalUnicast() {
				continue
			}
			p = p.Masked()
			if have[p] {
				continue
			}
			have[p] = true
			list = append(list, Subnet{Prefix: p.String(), Via: "direct", Interface: ifc.Name, Router: routerFor(p)})
		}
	}

	var halves []string // tunnels holding 0/1 or 128/1
	for _, r := range routes {
		label, isTunnel := tunnel[r.netif]
		if !isTunnel || have[r.prefix] {
			continue
		}
		switch {
		case r.prefix.Bits() == 0:
			continue
		case r.prefix.Bits() == 1:
			halves = append(halves, r.netif)
			continue
		case r.prefix.Bits() == 32, r.prefix.Addr().IsMulticast(), r.prefix.Addr().IsLoopback():
			continue
		}
		have[r.prefix] = true
		sn := Subnet{Prefix: r.prefix.String(), Via: "vpn", Interface: r.netif, Router: routerFor(r.prefix)}
		if label == "Tailscale" && r.prefix == tailnetRange {
			sn.Note = "tailnet"
		}
		list = append(list, sn)
	}
	allVia := ""
	if len(halves) == 2 && halves[0] == halves[1] && tunnel[halves[0]] != "Tailscale" {
		allVia = halves[0]
		list = append(list, Subnet{Prefix: "0.0.0.0/0", Via: "vpn", Interface: allVia, Note: "all traffic"})
	}
	return list, allVia
}

// buildDevices counts the ARP cache per direct subnet. A subnet behind a
// tunnel gets a nil count: ARP does not cross a router.
func buildDevices(subnets []Subnet, arp []arpEntry, defs []Route, own map[string]bool) []DeviceGroup {
	gw := map[string]bool{}
	for _, d := range defs {
		gw[d.Gateway] = true
	}
	var groups []DeviceGroup
	for _, sn := range subnets {
		if sn.Note == "all traffic" || sn.Note == "tailnet" {
			continue
		}
		g := DeviceGroup{Prefix: sn.Prefix, Via: sn.Via, Interface: sn.Interface, Router: sn.Router}
		if sn.Via == "direct" {
			p := netip.MustParsePrefix(sn.Prefix)
			for a := range own {
				if ip, err := netip.ParseAddr(a); err == nil && p.Contains(ip) && (g.Self == "" || ip.Less(netip.MustParseAddr(g.Self))) {
					g.Self = a
				}
			}
			for _, e := range arp {
				if e.netif == sn.Interface && p.Contains(e.ip) && !own[e.ip.String()] {
					g.Devices = append(g.Devices, Device{
						IP: e.ip.String(), MAC: e.mac, Interface: e.netif,
						Private: privateMAC(e.mac), Gateway: gw[e.ip.String()],
					})
				}
			}
			sort.Slice(g.Devices, func(i, j int) bool {
				return netip.MustParseAddr(g.Devices[i].IP).Less(netip.MustParseAddr(g.Devices[j].IP))
			})
			n := len(g.Devices)
			g.Count = &n
		}
		groups = append(groups, g)
	}
	return groups
}

// ownAddrs are this machine's IPv4 addresses. ARP lists them as
// permanent entries; they are not other devices.
func ownAddrs(ifs []Interface) map[string]bool {
	own := map[string]bool{}
	for _, i := range ifs {
		for _, a := range i.IPv4 {
			if p, err := netip.ParsePrefix(a); err == nil {
				own[p.Addr().String()] = true
			}
		}
	}
	return own
}

// otherVPNs drops Tailscale from the System Settings list: it has its own
// section.
func otherVPNs(all []VPNService) []VPNService {
	var out []VPNService
	for _, v := range all {
		if !strings.EqualFold(v.Name, "Tailscale") {
			out = append(out, v)
		}
	}
	return out
}

// locate answers "where am I" from the default route.
func locate(s Snapshot) Location {
	if len(s.Defaults) == 0 {
		return Location{Kind: "offline", Label: "offline: no default route"}
	}
	d := s.Defaults[0]
	loc := Location{Interface: d.Interface, Gateway: d.Gateway}
	var ifc *Interface
	for i := range s.Interfaces {
		if s.Interfaces[i].Name == d.Interface {
			ifc = &s.Interfaces[i]
		}
	}
	if ifc != nil && len(ifc.IPv4) > 0 {
		loc.Address, _, _ = strings.Cut(ifc.IPv4[0], "/")
	}
	if gw, err := netip.ParseAddr(d.Gateway); err == nil && hotspot.Contains(gw) {
		loc.Kind, loc.Label = "hotspot", "iPhone hotspot"
		return loc
	}
	for _, sn := range s.Subnets {
		if sn.Via == "direct" && sn.Interface == d.Interface && sn.Router != "" {
			loc.Kind, loc.Router = "lan", sn.Router
			loc.Label = "LAN of " + sn.Router
			return loc
		}
	}
	if ifc != nil && ifc.Kind == "tunnel" {
		loc.Kind, loc.Label = "other", "all traffic through "+ifc.Name
		return loc
	}
	loc.Kind, loc.Label = "other", "other network"
	return loc
}

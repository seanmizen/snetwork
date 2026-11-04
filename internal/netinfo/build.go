package netinfo

import (
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Raw is the unparsed output of every source. Package collect fills it from
// this machine; tests fill it from fixture files. An entry in Errs means
// that source could not be read.
type Raw struct {
	Ifconfig   string
	Services   string            // networksetup -listnetworkserviceorder
	Routes     string            // netstat -rn -f inet
	ARP        string            // arp -an
	DNS        string            // scutil --dns
	VPNList    string            // scutil --nc list
	Listening  string            // netstat -anv -p tcp
	FWGlobal   string            // socketfilterfw --getglobalstate
	FWStealth  string            // socketfilterfw --getstealthmode
	Tailscale  []byte            // tailscale status --json
	Summaries  map[string]string // ipconfig getsummary <iface>, by interface
	WiFi       *WiFiRaw          // nil when there is no Wi-Fi interface
	Errs       map[string]string // source -> error
	NotPresent map[string]bool   // sources whose tool is not installed
}

// hotspot is the subnet that iOS Personal Hotspot always hands out.
var hotspot = netip.MustParsePrefix("172.20.10.0/28")

// tailnetRange is Tailscale's address range.
var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

// Build turns raw output into a Snapshot. It is pure: the same Raw and time
// always give the same Snapshot.
func Build(r Raw, now time.Time) Snapshot {
	s := Snapshot{Taken: now}
	for src, err := range r.Errs {
		s.Problems = append(s.Problems, src+": "+err)
	}
	sort.Strings(s.Problems)

	// Tailscale first: interfaces, subnets and location all use it.
	switch {
	case r.NotPresent["tailscale"]:
		s.VPN.Tailscale = Tailscale{Installed: false}
	case len(r.Tailscale) > 0:
		if ts, err := parseTailscale(r.Tailscale); err == nil {
			s.VPN.Tailscale = ts
		} else {
			s.Problems = append(s.Problems, "tailscale: "+err.Error())
		}
	}
	s.VPN.Others = otherVPNs(parseVPNServices(r.VPNList))

	routes := parseRoutes(r.Routes)
	s.Interfaces = buildInterfaces(parseIfconfig(r.Ifconfig), parseServiceOrder(r.Services), s.VPN.Tailscale)
	s.Defaults = defaults(routes)
	if len(s.Defaults) > 0 {
		for i := range s.Interfaces {
			if s.Interfaces[i].Name == s.Defaults[0].Interface {
				s.Interfaces[i].Default = true
			}
		}
	}
	s.Subnets, s.VPN.AllTrafficVia = buildSubnets(s.Interfaces, routes, s.VPN.Tailscale)
	s.Devices = buildDevices(s.Subnets, parseARP(r.ARP), s.Defaults, ownAddrs(s.Interfaces))

	for _, ifc := range s.Interfaces {
		if out, ok := r.Summaries[ifc.Name]; ok {
			if l, ok := parseLease(ifc.Name, out, now.Location()); ok {
				s.DHCP = append(s.DHCP, l)
			}
		}
	}
	s.DNS = parseDNS(r.DNS)
	if r.WiFi != nil {
		s.WiFi = WiFiFromRaw(*r.WiFi)
	}
	s.Firewall = parseFirewall(r.FWGlobal, r.FWStealth)
	s.Listening = parseListening(r.Listening)
	s.Location = locate(s)
	return s
}

// buildInterfaces merges ifconfig with the service order. A service whose
// device ifconfig does not list is an unplugged adapter.
func buildInterfaces(ifs []ifconfigEntry, svcs []service, ts Tailscale) []Interface {
	byDev := map[string]service{}
	for _, s := range svcs {
		if s.device != "" {
			byDev[s.device] = s
		}
	}
	tsIPs := map[string]bool{}
	for _, ip := range ts.Self.IPs {
		tsIPs[ip] = true
	}
	present := map[string]bool{}
	var list []Interface
	for _, e := range ifs {
		present[e.name] = true
		ifc := Interface{Name: e.name, MTU: e.mtu, MAC: e.mac, IPv6: e.ipv6}
		for _, p := range e.ipv4 {
			ifc.IPv4 = append(ifc.IPv4, p.String())
		}
		svc, isSvc := byDev[e.name]
		if isSvc {
			ifc.Label, ifc.Order = svc.port, svc.order
		}
		ifc.Kind = kindOf(e.name, ifc.Label)
		switch {
		case e.status == "active":
			ifc.State = "up"
		case e.status == "inactive":
			ifc.State = "no link"
		case e.has("UP") && e.has("RUNNING"):
			ifc.State = "up"
		default:
			ifc.State = "down"
		}
		if ifc.Kind == "tunnel" {
			ifc.Label = "tunnel"
			for _, p := range e.ipv4 {
				if tsIPs[p.Addr().String()] || (len(tsIPs) == 0 && tailnetRange.Contains(p.Addr())) {
					ifc.Label = "Tailscale"
				}
			}
		}
		if ifc.Label == "" {
			ifc.Label = defaultLabel(ifc.Kind)
		}
		list = append(list, ifc)
	}
	for _, s := range svcs {
		if s.device != "" && !present[s.device] {
			state := "not plugged in"
			if s.disabled {
				state = "disabled"
			}
			list = append(list, Interface{
				Name: s.device, Label: s.port, Kind: kindOf(s.device, s.port),
				State: state, Order: s.order,
			})
		}
	}
	return list
}

func kindOf(name, port string) string {
	p := strings.ToLower(port)
	switch {
	case name == "lo0" || strings.HasPrefix(name, "lo"):
		return "loopback"
	case p == "wi-fi":
		return "wifi"
	case strings.HasPrefix(p, "iphone"):
		return "iphone"
	case strings.Contains(p, "bridge") || strings.HasPrefix(name, "bridge"):
		return "bridge"
	case strings.Contains(p, "ethernet") || strings.Contains(p, "lan") || strings.HasPrefix(p, "thunderbolt"):
		return "ethernet"
	case strings.HasPrefix(name, "utun") || strings.HasPrefix(name, "ipsec") || strings.HasPrefix(name, "ppp"):
		return "tunnel"
	}
	return "other"
}

func defaultLabel(kind string) string {
	switch kind {
	case "loopback":
		return "loopback"
	case "bridge":
		return "bridge"
	}
	return "system"
}

// defaults returns the default routes, the one in use first. A route with
// the I flag is scoped to its interface and is not the system default.
func defaults(routes []route) []Route {
	var main, scoped []Route
	for _, r := range routes {
		if r.prefix.Bits() != 0 {
			continue
		}
		rt := Route{Prefix: "default", Gateway: r.gateway, Interface: r.netif, Flags: r.flags}
		if strings.Contains(r.flags, "I") {
			scoped = append(scoped, rt)
		} else {
			main = append(main, rt)
		}
	}
	return append(main, scoped...)
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
		if ifc.Kind == "loopback" || ifc.Kind == "tunnel" {
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

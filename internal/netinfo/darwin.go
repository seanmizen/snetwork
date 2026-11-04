package netinfo

import (
	"strings"
	"time"
)

// parseDarwin reads the macOS sources.
func parseDarwin(r Raw, ts Tailscale, now time.Time) parsed {
	p := parsed{
		ifs:       buildInterfaces(parseIfconfig(r.S("ifconfig")), parseServiceOrder(r.S("services")), ts),
		routes:    parseRoutes(r.S("routes")),
		arp:       parseARP(r.S("arp")),
		dns:       parseDNS(r.S("dns")),
		listening: parseListening(r.S("listening")),
		vpnOthers: parseVPNServices(r.S("vpnlist")),
		firewall:  parseFirewall(r.S("fw-global"), r.S("fw-stealth")),
	}
	for _, ifc := range p.ifs {
		if out, ok := r.Out["getsummary-"+ifc.Name]; ok {
			if l, ok := parseLease(ifc.Name, string(out), now.Location()); ok {
				p.leases = append(p.leases, l)
			}
		}
	}
	if r.WiFi != nil {
		p.wifi = WiFiFromRaw(*r.WiFi)
		// A Shortcut named "Wi-Fi name" can read the name that macOS hides
		// from the terminal.
		if p.wifi.Name == "" {
			p.wifi.Name = strings.TrimSpace(r.S("wifi-name"))
			if p.wifi.Name != "" {
				p.wifi.NameSource = "shortcut"
			}
		}
	}
	return p
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
		if svc, ok := byDev[e.name]; ok {
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
	case strings.HasPrefix(name, "lo"):
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

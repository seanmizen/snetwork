package render

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// Sections are the section names, in display order. `snetwork <name>`
// shows one in full; `snetwork all` shows every one.
var Sections = []string{"interfaces", "subnets", "devices", "dhcp", "dns", "wifi", "vpn", "firewall", "listening"}

// summaryInterfaces is how many interfaces the summary shows.
const summaryInterfaces = 3

// Headline is the first line: where this machine is.
func Headline(s netinfo.Snapshot, st Style) string {
	parts := []string{st.paint("bold", "snetwork"), s.Location.Label}
	if s.Location.Address != "" {
		parts = append(parts, s.Location.Address+" on "+ifaceLabel(s, s.Location.Interface))
	}
	ts := s.VPN.Tailscale
	switch {
	case !ts.Installed:
		parts = append(parts, "no tailscale")
	case ts.State == "Running":
		parts = append(parts, "tailnet up")
	case ts.State != "":
		parts = append(parts, st.paint(roleWarn, "tailnet "+strings.ToLower(ts.State)))
	}
	if s.VPN.AllTrafficVia != "" {
		parts = append(parts, st.paint(roleWarn, "all traffic via "+s.VPN.AllTrafficVia))
	}
	return strings.Join(parts, st.paint(roleHint, " · ")) + "\n"
}

// Summary is plain `snetwork`: every section, a few lines each.
func Summary(s netinfo.Snapshot, st Style) string {
	var b strings.Builder
	b.WriteString(Headline(s, st))
	for _, name := range Sections {
		b.WriteString("\n")
		b.WriteString(st.heading(name, "snetwork "+name))
		b.WriteString(summaryBody(name, s, st))
	}
	b.WriteString(problems(s, st))
	return b.String()
}

// All is `snetwork all`: every section in full.
func All(s netinfo.Snapshot, st Style) string {
	var b strings.Builder
	b.WriteString(Headline(s, st))
	for _, name := range Sections {
		body, _ := fullBody(name, s, "", st)
		b.WriteString("\n")
		b.WriteString(st.heading(name, ""))
		b.WriteString(body)
	}
	b.WriteString(problems(s, st))
	return b.String()
}

// Section is `snetwork <name> [item]`: one section in full.
func Section(name, item string, s netinfo.Snapshot, st Style) (string, error) {
	body, err := fullBody(name, s, item, st)
	if err != nil {
		return "", err
	}
	return st.heading(name, "") + body + problems(s, st), nil
}

func problems(s netinfo.Snapshot, st Style) string {
	if len(s.Problems) == 0 {
		return ""
	}
	return "\n" + st.paint(roleWarn, "could not read: "+strings.Join(s.Problems, "; ")) + "\n"
}

func summaryBody(name string, s netinfo.Snapshot, st Style) string {
	switch name {
	case "interfaces":
		return interfacesSummary(s, st)
	case "subnets":
		return subnetsTable(s, st)
	case "devices":
		return devicesSummary(s, st)
	case "dhcp":
		return dhcpBody(s, "", false, st)
	case "dns":
		return dnsSummary(s, st)
	case "wifi":
		return wifiSummary(s, st)
	case "vpn":
		return vpnSummary(s, st)
	case "firewall":
		return firewallBody(s, false, st)
	case "listening":
		return listeningSummary(s, st)
	}
	return ""
}

// ItemError is a section item that does not exist, or a section that takes
// no item.
type ItemError struct{ msg string }

func (e ItemError) Error() string { return e.msg }

func fullBody(name string, s netinfo.Snapshot, item string, st Style) (string, error) {
	takesItem := map[string]bool{"interfaces": true, "devices": true, "dhcp": true, "vpn": true}
	if item != "" && !takesItem[name] {
		return "", ItemError{fmt.Sprintf("%s takes no item (got %q)", name, item)}
	}
	switch name {
	case "interfaces":
		return interfacesFull(s, item, st)
	case "subnets":
		return subnetsTable(s, st) + defaultsBody(s, st), nil
	case "devices":
		return devicesFull(s, item, st)
	case "dhcp":
		if item != "" && !hasLease(s, item) {
			return "", ItemError{fmt.Sprintf("no DHCP lease on %q", item)}
		}
		return dhcpBody(s, item, true, st), nil
	case "dns":
		return dnsFull(s, st), nil
	case "wifi":
		return wifiFull(s, st), nil
	case "vpn":
		return vpnFull(s, item, st)
	case "firewall":
		return firewallBody(s, true, st), nil
	case "listening":
		return listeningFull(s, st), nil
	}
	return "", ItemError{fmt.Sprintf("no section %q (sections: %s)", name, strings.Join(Sections, ", "))}
}

/* ---------- interfaces ---------- */

// rankInterfaces orders interfaces for display: the default route, then
// interfaces with an IPv4 address, then network services without one, then
// the rest.
func rankInterfaces(list []netinfo.Interface) []netinfo.Interface {
	rank := func(i netinfo.Interface) int {
		switch {
		case i.Default:
			return 0
		case len(i.IPv4) > 0 && i.Kind != "loopback" && i.Kind != "tunnel":
			return 1
		case len(i.IPv4) > 0 && i.Kind == "tunnel":
			return 2
		case i.Order > 0:
			return 3
		case i.Kind == "loopback":
			return 5
		}
		return 4
	}
	out := append([]netinfo.Interface(nil), list...)
	sort.SliceStable(out, func(a, b int) bool {
		ra, rb := rank(out[a]), rank(out[b])
		if ra != rb {
			return ra < rb
		}
		oa, ob := out[a].Order, out[b].Order
		if oa != ob && oa > 0 && ob > 0 {
			return oa < ob
		}
		return out[a].Name < out[b].Name
	})
	return out
}

func ipv4Text(i netinfo.Interface) string {
	var parts []string
	for _, a := range i.IPv4 {
		if i.Kind == "tunnel" {
			a = strings.TrimSuffix(a, "/32")
		}
		parts = append(parts, a)
	}
	return dash(strings.Join(parts, ", "))
}

func stateCell(state string) cell {
	if state == "up" {
		return cs(roleOK, state)
	}
	return cs(roleHint, state)
}

func interfaceRow(i netinfo.Interface) []cell {
	extra := c("")
	if i.Default {
		extra = c("← default route")
	}
	return []cell{cs(roleName, i.Name), c(i.Label), c(ipv4Text(i)), stateCell(i.State), extra}
}

func interfacesSummary(s netinfo.Snapshot, st Style) string {
	ranked := rankInterfaces(s.Interfaces)
	n := min(summaryInterfaces, len(ranked))
	var rows [][]cell
	for _, i := range ranked[:n] {
		rows = append(rows, interfaceRow(i))
	}
	out := st.table("  ", rows)
	if more := len(ranked) - n; more > 0 {
		out += "  " + st.paint(roleHint, fmt.Sprintf("+ %d more", more)) + "\n"
	}
	return out
}

func interfacesFull(s netinfo.Snapshot, item string, st Style) (string, error) {
	var list []netinfo.Interface
	var rows [][]cell
	for _, i := range rankInterfaces(s.Interfaces) {
		if item != "" && i.Name != item {
			continue
		}
		list = append(list, i)
		rows = append(rows, interfaceRow(i))
	}
	if len(list) == 0 {
		return "", ItemError{fmt.Sprintf("no interface %q", item)}
	}
	var b strings.Builder
	for n, line := range st.tableLines("  ", rows) {
		i := list[n]
		b.WriteString(line + "\n")
		if len(i.IPv6) > 0 {
			b.WriteString("    " + st.paint(roleHint, "ipv6 "+strings.Join(i.IPv6, ", ")) + "\n")
		}
		var detail []string
		if i.MAC != "" {
			detail = append(detail, "mac "+i.MAC)
		}
		if i.MTU > 0 {
			detail = append(detail, fmt.Sprintf("mtu %d", i.MTU))
		}
		if i.Order > 0 {
			detail = append(detail, fmt.Sprintf("service #%d", i.Order))
		}
		if len(detail) > 0 {
			b.WriteString("    " + st.paint(roleHint, strings.Join(detail, " · ")) + "\n")
		}
	}
	return b.String(), nil
}

// ifaceLabel names an interface the friendly way: "Wi-Fi", "Tailscale".
func ifaceLabel(s netinfo.Snapshot, name string) string {
	for _, i := range s.Interfaces {
		if i.Name == name && i.Label != "" && i.Label != "system" {
			return i.Label
		}
	}
	return name
}

/* ---------- subnets ---------- */

func subnetsTable(s netinfo.Snapshot, st Style) string {
	if len(s.Subnets) == 0 {
		return "  " + st.paint(roleHint, "none: no IPv4 address on any interface") + "\n"
	}
	var rows [][]cell
	online, total := 0, len(s.VPN.Tailscale.Peers)
	for _, p := range s.VPN.Tailscale.Peers {
		if p.Online {
			online++
		}
	}
	for _, sn := range s.Subnets {
		path := sn.Interface
		note := ""
		switch {
		case sn.Via == "direct":
			if sn.Router != "" {
				note = sn.Router + " routes it when you are away"
			}
		case sn.Note == "all traffic":
			note = "all traffic, through a VPN that is not Tailscale"
		case sn.Note == "tailnet":
			path = strings.ToLower(ifaceLabel(s, sn.Interface))
			note = fmt.Sprintf("tailnet · %d of %d peers online", online, total)
		default:
			path = strings.ToLower(ifaceLabel(s, sn.Interface))
			if sn.Router != "" {
				path += " → " + sn.Router
			}
		}
		via := "direct"
		if sn.Via == "vpn" {
			via = "via vpn"
		}
		spec := ""
		if sn.Note == "all traffic" {
			spec = roleWarn
		}
		rows = append(rows, []cell{cs(roleName, sn.Prefix), c(via), c(path), cs(orHint(spec), note)})
	}
	return st.table("  ", rows)
}

func orHint(spec string) string {
	if spec == "" {
		return roleHint
	}
	return spec
}

func defaultsBody(s netinfo.Snapshot, st Style) string {
	if len(s.Defaults) == 0 {
		return "\n  " + st.paint(roleWarn, "no default route: offline") + "\n"
	}
	var rows [][]cell
	for i, d := range s.Defaults {
		gw := d.Gateway
		if strings.HasPrefix(gw, "link#") {
			gw = "–"
		}
		note := cs(roleHint, "scoped to "+d.Interface)
		if i == 0 {
			note = cs(roleOK, "in use")
		}
		rows = append(rows, []cell{c("default"), c(gw), c(d.Interface), note})
	}
	return "\n  " + st.paint("bold", "default routes") + "\n" + st.table("    ", rows)
}

/* ---------- devices ---------- */

func devicesSummary(s netinfo.Snapshot, st Style) string {
	if len(s.Devices) == 0 {
		return "  " + st.paint(roleHint, "no subnets") + "\n"
	}
	var rows [][]cell
	for _, g := range s.Devices {
		if g.Count == nil {
			rows = append(rows, []cell{cs(roleName, g.Prefix), c("–"), cs(roleHint, behind(g))})
			continue
		}
		rows = append(rows, []cell{cs(roleName, g.Prefix), cf("", "%d seen recently", *g.Count), cs(roleHint, "ARP cache, no scan")})
	}
	return st.table("  ", rows)
}

func behind(g netinfo.DeviceGroup) string {
	if g.Router != "" {
		return "behind " + g.Router + ": not visible from here"
	}
	return "behind a VPN: not visible from here"
}

func devicesFull(s netinfo.Snapshot, item string, st Style) (string, error) {
	var b strings.Builder
	found := false
	for gi, g := range s.Devices {
		if item != "" && g.Prefix != item {
			continue
		}
		found = true
		if gi > 0 && item == "" {
			b.WriteString("\n")
		}
		if g.Count == nil {
			b.WriteString(st.table("  ", [][]cell{{cs(roleName, g.Prefix), c("via vpn · " + strings.ToLower(ifaceLabel(s, g.Interface))), cs(roleHint, behind(g))}}))
			continue
		}
		head := fmt.Sprintf("direct · %s · %d seen recently", g.Interface, *g.Count)
		b.WriteString(st.table("  ", [][]cell{{cs(roleName, g.Prefix), c(head), cs(roleHint, "ARP cache, no scan")}}))
		var rows [][]cell
		if self := selfAddr(s, g); self != "" {
			rows = append(rows, []cell{c(self), cs(roleHint, "this machine"), c("")})
		}
		for _, d := range g.Devices {
			var notes []string
			if d.Gateway {
				notes = append(notes, "gateway")
			}
			if d.Private {
				notes = append(notes, "private address")
			}
			rows = append(rows, []cell{c(d.IP), c(d.MAC), cs(roleHint, strings.Join(notes, " · "))})
		}
		b.WriteString(st.table("    ", rows))
	}
	if !found {
		return "", ItemError{fmt.Sprintf("no subnet %q (try one from snetwork subnets)", item)}
	}
	return b.String(), nil
}

func selfAddr(s netinfo.Snapshot, g netinfo.DeviceGroup) string {
	p, err := netip.ParsePrefix(g.Prefix)
	if err != nil {
		return ""
	}
	for _, i := range s.Interfaces {
		if i.Name != g.Interface {
			continue
		}
		for _, a := range i.IPv4 {
			if ap, err := netip.ParsePrefix(a); err == nil && p.Contains(ap.Addr()) {
				return ap.Addr().String()
			}
		}
	}
	return ""
}

/* ---------- dhcp ---------- */

func hasLease(s netinfo.Snapshot, iface string) bool {
	for _, l := range s.DHCP {
		if l.Interface == iface {
			return true
		}
	}
	return false
}

func dhcpBody(s netinfo.Snapshot, item string, full bool, st Style) string {
	if len(s.DHCP) == 0 {
		return "  " + st.paint(roleHint, "no DHCP lease on any interface") + "\n"
	}
	var b strings.Builder
	for _, l := range s.DHCP {
		if item != "" && l.Interface != item {
			continue
		}
		var first []string
		if l.Server != "" {
			first = append(first, "from "+l.Server)
		}
		if l.Length > 0 {
			first = append(first, human(l.Length)+" lease")
		}
		if !l.Expires.IsZero() {
			left := l.Expires.Sub(s.Taken)
			if left >= 0 {
				first = append(first, human(left)+" left")
			} else {
				first = append(first, st.paint(roleBad, "expired "+human(left)+" ago"))
			}
		}
		lead := fmt.Sprintf("  %s", st.paint(roleName, l.Interface))
		pad := strings.Repeat(" ", max(2, 6-len(l.Interface)))
		b.WriteString(lead + pad + strings.Join(first, st.paint(roleHint, " · ")) + "\n")
		indent := "  " + strings.Repeat(" ", len(l.Interface)) + pad
		router, dns := strings.Join(l.Routers, ", "), strings.Join(l.DNS, ", ")
		switch {
		case router != "" && router == dns:
			b.WriteString(indent + "router + dns " + router + " " + st.paint(roleHint, "(same box)") + "\n")
		default:
			b.WriteString(indent + "router " + dash(router) + st.paint(roleHint, " · ") + "dns " + dash(dns) + "\n")
		}
		if !full {
			continue
		}
		rows := [][]cell{{cs(roleHint, "address"), c(dash(l.Address))}}
		if l.Domain != "" {
			rows = append(rows, []cell{cs(roleHint, "domain"), c(l.Domain)})
		}
		if !l.Start.IsZero() {
			rows = append(rows, []cell{cs(roleHint, "started"), c(when(l.Start, s.Taken))})
		}
		if !l.Expires.IsZero() {
			rows = append(rows, []cell{cs(roleHint, "expires"), c(when(l.Expires, s.Taken))})
		}
		rows = append(rows, []cell{cs(roleHint, "state"), c(strings.ToLower(dash(l.State)))})
		b.WriteString(st.table(indent, rows))
	}
	return b.String()
}

// when prints a time relative to now: "18:20 today", "18:20 tomorrow",
// or the date.
func when(t, now time.Time) string {
	t = t.In(now.Location())
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	day := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location()).Sub(time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location()))
	switch day / (24 * time.Hour) {
	case 0:
		return t.Format("15:04") + " today"
	case 1:
		return t.Format("15:04") + " tomorrow"
	case -1:
		return t.Format("15:04") + " yesterday"
	}
	return t.Format("2006-01-02 15:04")
}

/* ---------- dns ---------- */

// nameserverLabel says who a nameserver is.
func nameserverLabel(s netinfo.Snapshot, ns string) string {
	switch {
	case ns == "100.100.100.100" || strings.HasPrefix(ns, "fd7a:115c:a1e0::53"):
		return "tailscale MagicDNS"
	case strings.HasPrefix(ns, "fe80:"):
		return "router (IPv6 link-local)"
	}
	for _, l := range s.DHCP {
		for _, d := range l.DNS {
			if d != ns {
				continue
			}
			for _, r := range l.Routers {
				if r == ns {
					return "router, from DHCP"
				}
			}
			return "from DHCP"
		}
	}
	return ""
}

// firstIPv4 picks the nameserver to show on one line: IPv4 if there is one.
func firstIPv4(list []string) string {
	for _, n := range list {
		if a, err := netip.ParseAddr(n); err == nil && a.Is4() {
			return n
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return ""
}

func dnsSummary(s netinfo.Snapshot, st Style) string {
	if len(s.DNS.General) == 0 {
		return "  " + st.paint(roleWarn, "no resolvers") + "\n"
	}
	var rows [][]cell
	for i, r := range s.DNS.General {
		ns := firstIPv4(r.Nameservers)
		search := ""
		if len(r.Search) > 0 {
			search = "search " + strings.Join(r.Search, " ")
		}
		rows = append(rows, []cell{cf(roleHint, "%d", i+1), cs(roleName, ns), c(nameserverLabel(s, ns)), cs(roleHint, search)})
	}
	return st.table("  ", rows)
}

func dnsFull(s netinfo.Snapshot, st Style) string {
	var b strings.Builder
	b.WriteString("  " + st.paint("bold", "any name") + st.paint(roleHint, ", asked in this order") + "\n")
	for i, r := range s.DNS.General {
		var rows [][]cell
		for j, ns := range r.Nameservers {
			n := ""
			if j == 0 {
				n = fmt.Sprintf("%d", i+1)
			}
			rows = append(rows, []cell{cs(roleHint, n), cs(roleName, ns), c(nameserverLabel(s, ns))})
		}
		b.WriteString(st.table("    ", rows))
		var notes []string
		if r.Interface != "" {
			notes = append(notes, "on "+r.Interface)
		}
		if len(r.Search) > 0 {
			notes = append(notes, "search "+strings.Join(r.Search, " "))
		}
		if len(r.Flags) > 0 {
			notes = append(notes, strings.ToLower(strings.Join(r.Flags, ", ")))
		}
		if len(notes) > 0 {
			b.WriteString("       " + st.paint(roleHint, strings.Join(notes, " · ")) + "\n")
		}
	}
	var rows [][]cell
	var mdns []string
	for _, r := range s.DNS.Domain {
		if r.Options == "mdns" {
			mdns = append(mdns, r.Domain)
			continue
		}
		ns := firstIPv4(r.Nameservers)
		rows = append(rows, []cell{cs(roleName, r.Domain), c(ns), c(nameserverLabel(s, ns))})
	}
	if len(mdns) > 0 {
		label := mdns[0]
		if len(mdns) > 1 {
			label = fmt.Sprintf("%s + %d reverse zones", mdns[0], len(mdns)-1)
		}
		rows = append(rows, []cell{cs(roleName, label), c("multicast DNS"), cs(roleHint, "answers stay on the local network")})
	}
	if len(rows) > 0 {
		b.WriteString("\n  " + st.paint("bold", "only for one domain") + "\n")
		b.WriteString(st.table("    ", rows))
	}
	return b.String()
}

/* ---------- wifi ---------- */

func wifiName(w *netinfo.WiFi) string {
	if w.Name == "" {
		return "name hidden by macOS"
	}
	return w.Name
}

func wifiLine(w *netinfo.WiFi) []string {
	parts := []string{wifiName(w)}
	if w.Band != "" {
		parts = append(parts, fmt.Sprintf("%s ch %d", w.Band, w.Channel))
	}
	if w.Width != "" {
		parts = append(parts, w.Width)
	}
	if w.RSSI != 0 {
		parts = append(parts, fmt.Sprintf("%d dBm", w.RSSI))
	}
	if w.RateMbps > 0 {
		parts = append(parts, fmt.Sprintf("%g Mb/s", w.RateMbps))
	}
	return parts
}

func wifiState(s netinfo.Snapshot) (string, bool) {
	switch w := s.WiFi; {
	case w == nil:
		return "no Wi-Fi interface", false
	case !w.PowerOn:
		return w.Interface + ": Wi-Fi is off", false
	case !w.Associated:
		return w.Interface + ": on, not connected", false
	}
	return "", true
}

func wifiSummary(s netinfo.Snapshot, st Style) string {
	if msg, ok := wifiState(s); !ok {
		return "  " + st.paint(roleHint, msg) + "\n"
	}
	parts := wifiLine(s.WiFi)
	if s.WiFi.Name == "" {
		parts[0] = st.paint(roleHint, parts[0])
	}
	return "  " + strings.Join(parts, st.paint(roleHint, " · ")) + "\n"
}

func wifiFull(s netinfo.Snapshot, st Style) string {
	if msg, ok := wifiState(s); !ok {
		return "  " + st.paint(roleHint, msg) + "\n"
	}
	w := s.WiFi
	name := c(wifiName(w))
	if w.Name == "" {
		name = cs(roleHint, wifiName(w)+" (grant Location to your terminal to see it)")
	}
	rows := [][]cell{
		{cs(roleHint, "network"), name},
		{cs(roleHint, "interface"), c(w.Interface)},
		{cs(roleHint, "channel"), cf("", "%d · %s · %s", w.Channel, w.Band, w.Width)},
		{cs(roleHint, "signal"), cf("", "%d dBm", w.RSSI)},
		{cs(roleHint, "noise"), cf("", "%d dBm", w.Noise)},
		{cs(roleHint, "snr"), cf("", "%d dB", w.RSSI-w.Noise)},
		{cs(roleHint, "rate"), cf("", "%g Mb/s", w.RateMbps)},
		{cs(roleHint, "standard"), c(dash(w.PHY))},
		{cs(roleHint, "security"), c(dash(w.Security))},
	}
	if w.Country != "" {
		rows = append(rows, []cell{cs(roleHint, "country"), c(w.Country)})
	}
	return st.table("  ", rows)
}

/* ---------- vpn ---------- */

func tailscaleState(ts netinfo.Tailscale) (string, bool) {
	switch {
	case !ts.Installed:
		return "not installed", false
	case ts.State == "Running":
		return "connected", true
	case ts.State == "":
		return "unknown", false
	}
	return strings.ToLower(ts.State), false
}

func otherState(v netinfo.VPNService) string {
	switch strings.ToLower(v.State) {
	case "connected":
		return "on"
	case "disconnected":
		return "off"
	}
	return strings.ToLower(v.State)
}

func vpnSummary(s netinfo.Snapshot, st Style) string {
	ts := s.VPN.Tailscale
	state, ok := tailscaleState(ts)
	spec := roleOK
	if !ok {
		spec = roleWarn
	}
	parts := []string{st.paint(spec, state)}
	if ok {
		var peers []string
		for _, p := range ts.Peers {
			m := st.paint(roleOK, "✓")
			if !p.Online {
				m = st.paint(roleBad, "✗")
			}
			peers = append(peers, p.Name+" "+m)
		}
		if len(peers) > 0 {
			parts = append(parts, strings.Join(peers, " "))
		}
		if ts.ExitNode != "" {
			parts = append(parts, "exit node "+ts.ExitNode)
		} else {
			parts = append(parts, "no exit node")
		}
	}
	rows := [][]cell{{c("tailscale"), c(strings.Join(parts, st.paint(roleHint, " · ")))}}
	if len(s.VPN.Others) > 0 {
		var o []string
		for _, v := range s.VPN.Others {
			o = append(o, v.Name+" "+otherState(v))
		}
		rows = append(rows, []cell{c("others"), c(strings.Join(o, st.paint(roleHint, " · ")))})
	}
	if s.VPN.AllTrafficVia != "" {
		rows = append(rows, []cell{c("tunnel"), cs(roleWarn, s.VPN.AllTrafficVia+" takes all traffic, and is not in System Settings")})
	}
	// the cells hold colour already, so table must not count it: paint
	// after alignment by building lines by hand
	var b strings.Builder
	for _, r := range rows {
		b.WriteString("  " + fmt.Sprintf("%-11s", r[0].text) + st.paint(r[1].spec, r[1].text) + "\n")
	}
	return b.String()
}

func vpnFull(s netinfo.Snapshot, item string, st Style) (string, error) {
	ts := s.VPN.Tailscale
	var b strings.Builder
	state, _ := tailscaleState(ts)
	rows := [][]cell{{cs(roleHint, "tailscale"), c(state)}}
	if ts.Tailnet != "" {
		rows = append(rows, []cell{cs(roleHint, "tailnet"), c(ts.Tailnet)})
	}
	if ts.Suffix != "" {
		rows = append(rows, []cell{cs(roleHint, "magicdns"), c(ts.Suffix)})
	}
	if ts.Self.Name != "" {
		rows = append(rows, []cell{cs(roleHint, "this machine"), c(ts.Self.Name + "  " + strings.Join(ts.Self.IPs, ", "))})
	}
	exit := "none"
	if ts.ExitNode != "" {
		exit = ts.ExitNode
	}
	if ts.Installed {
		rows = append(rows, []cell{cs(roleHint, "exit node"), c(exit)})
	}
	if item == "" {
		b.WriteString(st.table("  ", rows))
	}
	for _, h := range ts.Health {
		b.WriteString("  " + st.paint(roleWarn, "health: "+h) + "\n")
	}
	if len(ts.Peers) > 0 {
		var prow [][]cell
		found := false
		for _, p := range ts.Peers {
			if item != "" && p.Name != item {
				continue
			}
			found = true
			path := ""
			switch {
			case !p.Online && p.LastSeen != "":
				path = lastSeen(p.LastSeen, s.Taken)
			case p.Direct != "":
				path = "direct " + p.Direct
			case p.Relay != "":
				path = "relay " + p.Relay
			}
			online := cs(roleOK, "✓ online")
			if !p.Online {
				online = cs(roleBad, "✗ offline")
			}
			routes := ""
			if len(p.Routes) > 0 {
				routes = "routes " + strings.Join(p.Routes, ", ")
			}
			prow = append(prow, []cell{cs(roleName, p.Name), c(p.OS), online, c(routes), cs(roleHint, path)})
		}
		if item != "" && !found {
			return "", ItemError{fmt.Sprintf("no tailnet peer %q", item)}
		}
		if item == "" {
			b.WriteString("\n  " + st.paint("bold", "peers") + "\n")
		}
		b.WriteString(st.table("    ", prow))
	} else if item != "" {
		return "", ItemError{fmt.Sprintf("no tailnet peer %q", item)}
	}
	if item != "" {
		return b.String(), nil
	}
	if len(s.VPN.Others) > 0 {
		var orow [][]cell
		for _, v := range s.VPN.Others {
			en := ""
			if !v.Enabled {
				en = "disabled"
			}
			orow = append(orow, []cell{c(v.Name), c(otherState(v)), cs(roleHint, en)})
		}
		b.WriteString("\n  " + st.paint("bold", "other VPNs in System Settings") + "\n")
		b.WriteString(st.table("    ", orow))
	}
	if s.VPN.AllTrafficVia != "" {
		b.WriteString("\n  " + st.paint(roleWarn, s.VPN.AllTrafficVia+" takes all IPv4 traffic (routes 0/1 and 128.0/1), and is not in System Settings") + "\n")
	}
	return b.String(), nil
}

// lastSeen turns Tailscale's RFC 3339 time into "last seen 1h 5m ago".
func lastSeen(t string, now time.Time) string {
	ts, err := time.Parse(time.RFC3339Nano, t)
	if err != nil {
		return "last seen " + t
	}
	return "last seen " + human(now.Sub(ts)) + " ago"
}

/* ---------- firewall ---------- */

func firewallBody(s netinfo.Snapshot, full bool, st Style) string {
	fw := s.Firewall
	app := fw.App
	switch fw.Stealth {
	case "on", "off":
		app += " · stealth " + fw.Stealth
	}
	spec := roleOK
	if fw.App != "on" && fw.App != "block all" {
		spec = roleWarn
	}
	rows := [][]cell{
		{c("macOS app firewall"), cs(spec, app)},
		{c("pf"), cs(roleHint, fw.PF)},
	}
	out := st.table("  ", rows)
	if full {
		out += "  " + st.paint(roleHint, "pf needs root to read: sudo pfctl -s info") + "\n"
	}
	return out
}

/* ---------- listening ---------- */

// listeningSummaryMax is how many ports the summary names.
const listeningSummaryMax = 5

func listeningSummary(s netinfo.Snapshot, st Style) string {
	if len(s.Listening) == 0 {
		return "  " + st.paint(roleHint, "nothing listens beyond loopback") + "\n"
	}
	var names []string
	for i, l := range s.Listening {
		if i == listeningSummaryMax {
			names = append(names, "…")
			break
		}
		names = append(names, fmt.Sprintf("%d %s", l.Port, dash(l.Process)))
	}
	noun := "ports"
	if len(s.Listening) == 1 {
		noun = "port"
	}
	return fmt.Sprintf("  %d %s reachable beyond loopback: %s\n", len(s.Listening), noun, strings.Join(names, ", "))
}

func listeningFull(s netinfo.Snapshot, st Style) string {
	if len(s.Listening) == 0 {
		return "  " + st.paint(roleHint, "nothing listens beyond loopback") + "\n"
	}
	rows := [][]cell{{cs(roleHint, "port"), cs(roleHint, "on"), cs(roleHint, "family"), cs(roleHint, "process")}}
	for _, l := range s.Listening {
		proc := dash(l.Process)
		if l.PID > 0 {
			proc += fmt.Sprintf(" (%d)", l.PID)
		}
		on := l.Address
		if on == "*" {
			on = "all addresses"
		}
		rows = append(rows, []cell{cf(roleName, "%d", l.Port), c(on), c(l.Family), c(proc)})
	}
	return st.table("  ", rows)
}

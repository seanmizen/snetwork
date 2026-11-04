package netinfo

import (
	"bufio"
	"encoding/json"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// parseLinux reads the Linux sources: iproute2 (JSON), iw, ss, dhcpcd,
// resolv.conf and systemctl.
func parseLinux(r Raw, ts Tailscale, now time.Time) parsed {
	wifiIfs := parseIwDev(r.S("iw-dev"))
	addrs := parseIPAddr(r.Out["ip-addr"])
	p := parsed{
		ifs:       linuxInterfaces(addrs, wifiIfs, ts),
		routes:    parseIPRoute(r.Out["ip-route"]),
		arp:       parseNeigh(r.S("ip-neigh")),
		dns:       parseResolvConf(r.S("resolv"), r.S("resolved")),
		listening: parseSS(r.S("ss")),
		firewall:  parseSystemctlFirewall(r.S("fw-systemctl"), r.NotPresent["fw-systemctl"]),
	}
	for _, a := range addrs {
		for _, ai := range a.AddrInfo {
			if ai.Family != "inet" || !ai.Dynamic {
				continue
			}
			l := parseDhcpcd(a.IfName, r.S("dhcpcd-"+a.IfName))
			l.Address = ai.Local
			if ai.ValidLife > 0 && ai.ValidLife < 1<<32-1 {
				l.Expires = now.Add(time.Duration(ai.ValidLife) * time.Second).Truncate(time.Second)
				if l.Length > 0 {
					l.Start = l.Expires.Add(-l.Length)
				}
			}
			p.leases = append(p.leases, l)
		}
	}
	for name, w := range wifiIfs {
		w.Interface = name
		applyIwLink(w, r.S("iw-link-"+name))
		if p.wifi == nil || w.Associated {
			p.wifi = w
		}
	}
	return p
}

// ipAddr is one entry of `ip -j addr`.
type ipAddr struct {
	IfName    string   `json:"ifname"`
	Flags     []string `json:"flags"`
	MTU       int      `json:"mtu"`
	OperState string   `json:"operstate"`
	LinkType  string   `json:"link_type"`
	Address   string   `json:"address"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
		Dynamic   bool   `json:"dynamic"`
		ValidLife int64  `json:"valid_life_time"`
	} `json:"addr_info"`
}

func parseIPAddr(b []byte) []ipAddr {
	var list []ipAddr
	_ = json.Unmarshal(b, &list) // bad output: no interfaces, not a crash
	return list
}

func linuxInterfaces(addrs []ipAddr, wifi map[string]*WiFi, ts Tailscale) []Interface {
	tsIPs := map[string]bool{}
	for _, ip := range ts.Self.IPs {
		tsIPs[ip] = true
	}
	var list []Interface
	for _, a := range addrs {
		ifc := Interface{Name: a.IfName, MTU: a.MTU}
		if a.LinkType == "ether" {
			ifc.MAC = normalizeMAC(a.Address)
		}
		isTS := strings.HasPrefix(a.IfName, "tailscale")
		for _, ai := range a.AddrInfo {
			switch {
			case ai.Family == "inet":
				ifc.IPv4 = append(ifc.IPv4, ai.Local+"/"+strconv.Itoa(ai.PrefixLen))
				isTS = isTS || tsIPs[ai.Local]
			case ai.Family == "inet6" && ai.Scope == "global":
				ifc.IPv6 = append(ifc.IPv6, ai.Local)
			}
		}
		_, isWiFi := wifi[a.IfName]
		switch {
		case a.LinkType == "loopback":
			ifc.Kind, ifc.Label = "loopback", "loopback"
		case isWiFi:
			ifc.Kind, ifc.Label = "wifi", "Wi-Fi"
		case isTS:
			ifc.Kind, ifc.Label = "tunnel", "Tailscale"
		case a.LinkType == "none" || strings.HasPrefix(a.IfName, "wg") || strings.HasPrefix(a.IfName, "tun"):
			ifc.Kind, ifc.Label = "tunnel", "tunnel"
		case a.IfName == "docker0" || strings.HasPrefix(a.IfName, "br-"):
			ifc.Kind, ifc.Label = "bridge", "Docker bridge"
		case strings.HasPrefix(a.IfName, "veth"):
			ifc.Kind, ifc.Label = "virtual", "veth"
		case a.LinkType == "ether":
			ifc.Kind, ifc.Label = "ethernet", "Ethernet"
		default:
			ifc.Kind, ifc.Label = "other", "system"
		}
		up := false
		for _, f := range a.Flags {
			up = up || f == "UP"
		}
		switch {
		case a.OperState == "UP", a.OperState == "UNKNOWN" && up:
			ifc.State = "up"
		case up:
			ifc.State = "no link"
		default:
			ifc.State = "down"
		}
		list = append(list, ifc)
	}
	return list
}

// parseIPRoute reads `ip -j route show table all`. It keeps unicast routes
// from the main table and Tailscale's table 52.
func parseIPRoute(b []byte) []route {
	var rows []struct {
		Type    string   `json:"type"`
		Dst     string   `json:"dst"`
		Gateway string   `json:"gateway"`
		Dev     string   `json:"dev"`
		Metric  int      `json:"metric"`
		Table   string   `json:"table"`
		Flags   []string `json:"flags"`
	}
	_ = json.Unmarshal(b, &rows)
	var list []route
	for _, r := range rows {
		if (r.Type != "" && r.Type != "unicast") || (r.Table != "" && r.Table != "main" && r.Table != "52") {
			continue
		}
		p, ok := parseDestination(r.Dst)
		if !ok {
			continue
		}
		gw := r.Gateway
		if gw == "" {
			gw = "link"
		}
		list = append(list, route{prefix: p, gateway: gw, netif: r.Dev, metric: r.Metric, flags: strings.Join(r.Flags, ",")})
	}
	return list
}

// parseNeigh reads `ip -j neigh`. Entries without a hardware address
// (FAILED, INCOMPLETE) and IPv6 neighbours are dropped.
func parseNeigh(s string) []arpEntry {
	var rows []struct {
		Dst    string   `json:"dst"`
		Dev    string   `json:"dev"`
		LLAddr string   `json:"lladdr"`
		State  []string `json:"state"`
	}
	_ = json.Unmarshal([]byte(s), &rows)
	var list []arpEntry
	for _, r := range rows {
		ip, err := netip.ParseAddr(r.Dst)
		if err != nil || !ip.Is4() || r.LLAddr == "" || ip.IsMulticast() {
			continue
		}
		bad := false
		for _, st := range r.State {
			bad = bad || st == "FAILED" || st == "INCOMPLETE"
		}
		if !bad {
			list = append(list, arpEntry{ip: ip, mac: normalizeMAC(r.LLAddr), netif: r.Dev})
		}
	}
	return list
}

// parseResolvConf reads /etc/resolv.conf. When it points at the
// systemd-resolved stub, the upstream file names the real servers.
func parseResolvConf(main, upstream string) DNS {
	read := func(s string) Resolver {
		var r Resolver
		sc := bufio.NewScanner(strings.NewReader(s))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 2 || strings.HasPrefix(f[0], "#") {
				continue
			}
			switch f[0] {
			case "nameserver":
				r.Nameservers = append(r.Nameservers, f[1])
			case "search", "domain":
				r.Search = append(r.Search, f[1:]...)
			}
		}
		return r
	}
	var d DNS
	first := read(main)
	if len(first.Nameservers) > 0 {
		d.General = append(d.General, first)
	}
	if len(first.Nameservers) == 1 && first.Nameservers[0] == "127.0.0.53" {
		if up := read(upstream); len(up.Nameservers) > 0 {
			up.Flags = []string{"upstream of systemd-resolved"}
			d.General = append(d.General, up)
		}
	}
	for i := range d.General {
		d.General[i].Order = i + 1
	}
	return d
}

// parseDhcpcd reads `dhcpcd -U <iface>`: shell variables, maybe quoted.
func parseDhcpcd(iface, s string) Lease {
	l := Lease{Interface: iface, State: "dynamic"}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `'"`)
		switch k {
		case "dhcp_server_identifier":
			l.Server = v
		case "routers":
			l.Routers = strings.Fields(v)
		case "domain_name_servers":
			l.DNS = strings.Fields(v)
		case "domain_name":
			l.Domain = v
		case "dhcp_lease_time":
			if n, err := strconv.Atoi(v); err == nil {
				l.Length = time.Duration(n) * time.Second
			}
		}
	}
	if l.Server != "" {
		l.State = "bound"
	}
	return l
}

var iwChannel = regexp.MustCompile(`channel (\d+) \((\d+) MHz\)(?:, width: (\d+) MHz)?`)

// parseIwDev reads `iw dev`: one Wi-Fi interface per "Interface" line.
func parseIwDev(s string) map[string]*WiFi {
	out := map[string]*WiFi{}
	var cur *WiFi
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "Interface "):
			cur = &WiFi{PowerOn: true}
			out[strings.TrimPrefix(line, "Interface ")] = cur
		case cur == nil:
		case strings.HasPrefix(line, "ssid "):
			cur.Name = strings.TrimPrefix(line, "ssid ")
		case strings.HasPrefix(line, "channel "):
			if m := iwChannel.FindStringSubmatch(line); m != nil {
				cur.Channel, _ = strconv.Atoi(m[1])
				mhz, _ := strconv.Atoi(m[2])
				cur.Band = bandOf(mhz)
				if m[3] != "" {
					cur.Width = m[3] + " MHz"
				}
			}
		}
	}
	return out
}

// applyIwLink adds `iw dev <iface> link`: signal, rate, standard.
func applyIwLink(w *WiFi, s string) {
	if strings.HasPrefix(strings.TrimSpace(s), "Not connected") || !strings.Contains(s, "Connected to") {
		w.Associated = false
		return
	}
	w.Associated = true
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), ": ")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		switch k {
		case "SSID":
			w.Name = v
		case "freq":
			if mhz, err := strconv.ParseFloat(f[0], 64); err == nil && w.Band == "" {
				w.Band = bandOf(int(mhz))
			}
		case "signal":
			w.RSSI, _ = strconv.Atoi(f[0])
		case "tx bitrate":
			w.RateMbps, _ = strconv.ParseFloat(f[0], 64)
			switch {
			case strings.Contains(v, "EHT"):
				w.PHY = "802.11be"
			case strings.Contains(v, "HE-"):
				w.PHY = "802.11ax"
			case strings.Contains(v, "VHT"):
				w.PHY = "802.11ac"
			case strings.Contains(v, "MCS"):
				w.PHY = "802.11n"
			}
		}
	}
}

func bandOf(mhz int) string {
	switch {
	case mhz >= 2400 && mhz < 2500:
		return "2.4 GHz"
	case mhz >= 5150 && mhz < 5925:
		return "5 GHz"
	case mhz >= 5925 && mhz <= 7125:
		return "6 GHz"
	}
	return ""
}

var ssUsers = regexp.MustCompile(`\("([^"]+)",pid=(\d+)`)

// parseSS reads `ss -Htlnp`. Process names show only for this user's
// processes unless ss runs as root.
//
//	LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=650,fd=3))
func parseSS(s string) []Listener {
	type key struct {
		port    int
		addr    string
		process string
	}
	seen := map[key]*Listener{}
	var order []key
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] != "LISTEN" {
			continue
		}
		i := strings.LastIndex(f[3], ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(f[3][i+1:])
		if err != nil {
			continue
		}
		addr := strings.Trim(f[3][:i], "[]")
		if j := strings.Index(addr, "%"); j >= 0 {
			addr = addr[:j]
		}
		fam := "ipv4"
		switch addr {
		case "0.0.0.0":
			addr = "*"
		case "::":
			addr, fam = "*", "ipv6"
		case "*":
			fam = "both"
		default:
			if strings.Contains(addr, ":") {
				fam = "ipv6"
			}
		}
		if loopback(addr) {
			continue
		}
		var name string
		var pid int
		if m := ssUsers.FindStringSubmatch(strings.Join(f[5:], " ")); m != nil {
			name = m[1]
			pid, _ = strconv.Atoi(m[2])
		}
		k := key{port, addr, name}
		if l, ok := seen[k]; ok {
			if l.Family != fam {
				l.Family = "both"
			}
			continue
		}
		seen[k] = &Listener{Port: port, Address: addr, Family: fam, Process: name, PID: pid}
		order = append(order, k)
	}
	list := make([]Listener, 0, len(order))
	for _, k := range order {
		list = append(list, *seen[k])
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Port < list[j].Port })
	return list
}

// parseSystemctlFirewall reads `systemctl is-active ufw nftables`: one
// state per line. Reading the rules themselves needs root.
func parseSystemctlFirewall(s string, absent bool) Firewall {
	names := []string{"ufw", "nftables"}
	lines := strings.Fields(s)
	var fw Firewall
	for i, n := range names {
		st := "unknown"
		if !absent && i < len(lines) {
			st = lines[i]
		}
		fw.Tools = append(fw.Tools, FirewallTool{Name: n, State: st, Detail: "rules need root"})
	}
	return fw
}

package netinfo

import (
	"bufio"
	"net/netip"
	"sort"
	"strings"

	"github.com/seanmizen/snetwork/internal/oui"
)

// Name sources, strongest first. Your own ssh config wins; an mDNS answer
// is the weakest, because any device can claim any name.
var nameSources = []string{"ssh config", "tailscale", "known_hosts", "dns", "mdns"}

// nameDevices gives devices a vendor and, where a source knows one, a
// friendly name. Then it folds a machine's second address on the same
// subnet into its first entry.
func nameDevices(s *Snapshot, r Raw) {
	byIP := map[string]map[string]string{} // ip -> source -> name
	add := func(src, ip, name string) {
		if name == "" {
			return
		}
		if byIP[ip] == nil {
			byIP[ip] = map[string]string{}
		}
		if _, ok := byIP[ip][src]; !ok {
			byIP[ip][src] = name
		}
	}
	for ip, n := range parseSSHConfig(r.S("ssh_config")) {
		add("ssh config", ip, n)
	}
	for _, p := range s.VPN.Tailscale.Peers {
		if ap, err := netip.ParseAddrPort(p.Direct); err == nil {
			add("tailscale", ap.Addr().String(), p.Name)
		}
	}
	for ip, n := range parseKnownHosts(r.S("known_hosts")) {
		add("known_hosts", ip, n)
	}
	for ip, n := range parseNameLines(r.S("names-dns")) {
		add("dns", ip, n)
	}
	for ip, n := range parseNameLines(r.S("names-mdns")) {
		add("mdns", ip, n)
	}

	for gi := range s.Devices {
		g := &s.Devices[gi]
		if g.AskedVia != "" {
			continue // the router named its own devices
		}
		for di := range g.Devices {
			d := &g.Devices[di]
			if !d.Private {
				d.Vendor = oui.Lookup(d.MAC)
			}
			d.Name, d.NameSource = "", ""
			for _, src := range nameSources {
				if n, ok := byIP[d.IP][src]; ok {
					d.Name, d.NameSource = n, src
					break
				}
			}
		}
		g.Devices = foldByName(g.Devices)
		if g.Count != nil {
			n := len(g.Devices)
			g.Count = &n
		}
	}
}

// foldByName merges entries that share a name: one machine with two links.
func foldByName(list []Device) []Device {
	first := map[string]int{}
	var out []Device
	for _, d := range list {
		if d.Name != "" {
			if i, ok := first[d.Name]; ok {
				out[i].Also = append(out[i].Also, d.IP)
				continue
			}
			first[d.Name] = len(out)
		}
		d.Also = nil
		out = append(out, d)
	}
	return out
}

// parseKnownHosts maps addresses to names. known_hosts stores one host
// key under every name and address used to reach a machine, so entries
// that share a key are one machine. Hashed entries cannot be read.
func parseKnownHosts(s string) map[string]string {
	type group struct{ names, ips []string }
	groups := map[string]*group{}
	var order []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > 0 && strings.HasPrefix(f[0], "@") {
			f = f[1:] // @cert-authority, @revoked
		}
		if len(f) < 3 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], "|") {
			continue
		}
		k := f[1] + " " + f[2]
		g, ok := groups[k]
		if !ok {
			g = &group{}
			groups[k] = g
			order = append(order, k)
		}
		for _, h := range strings.Split(f[0], ",") {
			if strings.HasPrefix(h, "[") {
				h = strings.Trim(strings.SplitN(h, "]", 2)[0], "[")
			}
			if a, err := netip.ParseAddr(h); err == nil {
				if a.Is4() && !a.IsLoopback() {
					g.ips = append(g.ips, a.String())
				}
			} else if h != "" && h != "localhost" {
				g.names = append(g.names, h)
			}
		}
	}
	out := map[string]string{}
	for _, k := range order {
		g := groups[k]
		name := bestName(g.names)
		if name == "" {
			continue
		}
		for _, ip := range g.ips {
			if _, ok := out[ip]; !ok {
				out[ip] = name
			}
		}
	}
	return out
}

// bestName picks the shortest first label: "asus" from "asus",
// "asus.local" and "asus.tail1234.ts.net".
func bestName(names []string) string {
	var labels []string
	for _, n := range names {
		l, _, _ := strings.Cut(n, ".")
		if l != "" {
			labels = append(labels, l)
		}
	}
	sort.SliceStable(labels, func(i, j int) bool { return len(labels[i]) < len(labels[j]) })
	if len(labels) == 0 {
		return ""
	}
	return labels[0]
}

// parseSSHConfig maps a literal IPv4 HostName to the Host alias that uses
// it. Names are not resolved: that would send packets.
func parseSSHConfig(s string) map[string]string {
	out := map[string]string{}
	var hosts []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, _ := strings.Cut(strings.Replace(line, "=", " ", 1), " ")
		v = strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "host":
			hosts = nil
			for _, h := range strings.Fields(v) {
				if !strings.ContainsAny(h, "*?!") {
					hosts = append(hosts, h)
				}
			}
		case "match":
			hosts = nil
		case "hostname":
			if a, err := netip.ParseAddr(v); err == nil && a.Is4() && len(hosts) > 0 {
				if _, ok := out[a.String()]; !ok {
					out[a.String()] = hosts[0]
				}
			}
		}
	}
	return out
}

// parseNameLines reads "ip name" lines, as the active lookups store them.
func parseNameLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if ip, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name != "" {
			out[ip] = strings.TrimSpace(name)
		}
	}
	return out
}

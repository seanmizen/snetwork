package netinfo

import (
	"bufio"
	"net/netip"
	"strconv"
	"strings"
)

// route is one row of `netstat -rn -f inet`.
type route struct {
	prefix  netip.Prefix
	gateway string
	flags   string
	netif   string
	metric  int // Linux only; 0 on macOS
}

// parseRoutes reads `netstat -rn -f inet`. It keeps default and network
// routes and drops host routes, which are ARP entries in all but name.
func parseRoutes(out string) []route {
	var list []route
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] == "Destination" {
			continue
		}
		flags := f[2]
		if strings.Contains(flags, "H") {
			continue
		}
		p, ok := parseDestination(f[0])
		if !ok {
			continue
		}
		list = append(list, route{prefix: p, gateway: f[1], flags: flags, netif: f[3]})
	}
	return list
}

// parseDestination reads netstat's abbreviated destinations: "default",
// "10.9.8/24", "100.64/10", "0/1", "128.0/1", "127", "169.254" and
// "192.168.64". Without a slash, the length is 8 bits per octet given, and
// four octets are a host.
func parseDestination(s string) (netip.Prefix, bool) {
	if s == "default" {
		return netip.MustParsePrefix("0.0.0.0/0"), true
	}
	addr, lenStr, hasLen := strings.Cut(s, "/")
	octets := strings.Split(addr, ".")
	if len(octets) > 4 {
		return netip.Prefix{}, false
	}
	bits := 8 * len(octets)
	if hasLen {
		n, err := strconv.Atoi(lenStr)
		if err != nil || n < 0 || n > 32 {
			return netip.Prefix{}, false
		}
		bits = n
	}
	for len(octets) < 4 {
		octets = append(octets, "0")
	}
	a, err := netip.ParseAddr(strings.Join(octets, "."))
	if err != nil || !a.Is4() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a, bits).Masked(), true
}

// arpEntry is one complete row of `arp -an`.
type arpEntry struct {
	ip    netip.Addr
	mac   string
	netif string
}

// parseARP reads `arp -an`:
//
//	? (10.1.2.1) at 7c:39:53:d0:ed:67 on en0 ifscope [ethernet]
//
// It drops incomplete entries, broadcast and multicast.
func parseARP(out string) []arpEntry {
	var list []arpEntry
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || f[2] != "at" || f[4] != "on" {
			continue
		}
		ip, err := netip.ParseAddr(strings.Trim(f[1], "()"))
		if err != nil || ip.IsMulticast() {
			continue
		}
		mac := normalizeMAC(f[3])
		if mac == "(incomplete)" || mac == "ff:ff:ff:ff:ff:ff" || strings.Count(mac, ":") != 5 {
			continue
		}
		list = append(list, arpEntry{ip: ip, mac: mac, netif: f[5]})
	}
	return list
}

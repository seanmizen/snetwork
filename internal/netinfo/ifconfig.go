package netinfo

import (
	"bufio"
	"net/netip"
	"strconv"
	"strings"
)

// ifconfigEntry is one interface block from ifconfig, before it is merged
// with the service order.
type ifconfigEntry struct {
	name   string
	flags  []string
	mtu    int
	mac    string
	ipv4   []netip.Prefix
	ipv6   []string
	status string // "active", "inactive" or "" when ifconfig prints none
}

// parseIfconfig reads the output of `ifconfig` (all interfaces).
func parseIfconfig(out string) []ifconfigEntry {
	var list []ifconfigEntry
	var cur *ifconfigEntry
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			name, rest, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			list = append(list, ifconfigEntry{name: name})
			cur = &list[len(list)-1]
			if i, j := strings.Index(rest, "<"), strings.Index(rest, ">"); i >= 0 && j > i {
				cur.flags = strings.Split(rest[i+1:j], ",")
			}
			if _, m, ok := strings.Cut(rest, " mtu "); ok {
				cur.mtu, _ = strconv.Atoi(strings.TrimSpace(m))
			}
			continue
		}
		if cur == nil {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "ether":
			cur.mac = normalizeMAC(f[1])
		case "inet":
			if p, ok := inetPrefix(f); ok {
				cur.ipv4 = append(cur.ipv4, p)
			}
		case "inet6":
			addr := f[1]
			if strings.Contains(addr, "%") || strings.HasPrefix(addr, "fe80:") || addr == "::1" {
				continue // link-local and loopback say nothing about reach
			}
			cur.ipv6 = append(cur.ipv6, addr)
		case "status:":
			cur.status = f[1]
		}
	}
	return list
}

// inetPrefix reads "inet 10.1.2.3 netmask 0xffffff00 ..." and the
// point-to-point form "inet A --> B netmask 0xffffffff".
func inetPrefix(f []string) (netip.Prefix, bool) {
	addr, err := netip.ParseAddr(f[1])
	if err != nil {
		return netip.Prefix{}, false
	}
	bits := 32
	for i := 2; i+1 < len(f); i++ {
		if f[i] == "netmask" {
			m, err := strconv.ParseUint(strings.TrimPrefix(f[i+1], "0x"), 16, 32)
			if err == nil {
				bits = maskBits(uint32(m))
			}
		}
	}
	return netip.PrefixFrom(addr, bits), true
}

func maskBits(m uint32) int {
	n := 0
	for m&0x80000000 != 0 {
		n++
		m <<= 1
	}
	return n
}

func (e ifconfigEntry) has(flag string) bool {
	for _, f := range e.flags {
		if f == flag {
			return true
		}
	}
	return false
}

// service is one entry of `networksetup -listnetworkserviceorder`.
type service struct {
	order    int
	name     string
	port     string // hardware port, e.g. "Wi-Fi", "USB 10/100/1000 LAN"
	device   string // "" for a VPN service
	disabled bool
}

// parseServiceOrder reads `networksetup -listnetworkserviceorder`.
//
//	(2) Wi-Fi
//	(Hardware Port: Wi-Fi, Device: en0)
//
// A disabled service is listed as "(*) Name".
func parseServiceOrder(out string) []service {
	var list []service
	var cur *service
	n := 0
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "(Hardware Port:"):
			if cur == nil {
				continue
			}
			body := strings.TrimSuffix(strings.TrimPrefix(line, "("), ")")
			port, dev, _ := strings.Cut(body, ", Device:")
			cur.port = strings.TrimSpace(strings.TrimPrefix(port, "Hardware Port:"))
			cur.device = strings.TrimSpace(dev)
		case strings.HasPrefix(line, "("):
			num, name, ok := strings.Cut(line[1:], ") ")
			if !ok {
				continue
			}
			n++
			s := service{order: n, name: name}
			if num == "*" {
				s.disabled = true
			}
			list = append(list, s)
			cur = &list[len(list)-1]
		}
	}
	return list
}

// normalizeMAC pads each octet to two digits: arp and ifconfig print
// "34:97:f6:27:95:6".
func normalizeMAC(s string) string {
	parts := strings.Split(strings.ToLower(s), ":")
	if len(parts) != 6 {
		return strings.ToLower(s)
	}
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	return strings.Join(parts, ":")
}

// privateMAC reports a locally administered address: bit 1 of the first
// octet. Phones and laptops use one per Wi-Fi network.
func privateMAC(mac string) bool {
	if len(mac) < 2 {
		return false
	}
	b, err := strconv.ParseUint(mac[:2], 16, 8)
	return err == nil && b&0x02 != 0
}

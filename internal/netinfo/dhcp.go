package netinfo

import (
	"strconv"
	"strings"
	"time"
)

// leaseTimeLayout is how `ipconfig getsummary` prints lease times, in local
// time: "11/04/2026 18:20:17".
const leaseTimeLayout = "01/02/2006 15:04:05"

// parseLease reads `ipconfig getsummary <iface>` and returns the IPv4 DHCP
// lease, or false when the interface has no DHCP lease.
func parseLease(iface, out string, loc *time.Location) (Lease, bool) {
	v4 := ipv4Section(out)
	if !strings.Contains(v4, "ConfigMethod : DHCP") {
		return Lease{}, false
	}
	l := Lease{Interface: iface}
	inAddresses := false
	for _, raw := range strings.Split(v4, "\n") {
		line := strings.TrimSpace(raw)
		key, val, _ := strings.Cut(line, " : ")
		switch {
		case strings.HasPrefix(line, "Addresses : <array>"):
			inAddresses = true
		case inAddresses && strings.HasPrefix(line, "}"):
			inAddresses = false
		case inAddresses && l.Address == "" && strings.HasPrefix(line, "0 : "):
			l.Address = strings.TrimPrefix(line, "0 : ")
		case key == "LeaseStartTime":
			l.Start, _ = time.ParseInLocation(leaseTimeLayout, val, loc)
		case key == "LeaseExpirationTime":
			l.Expires, _ = time.ParseInLocation(leaseTimeLayout, val, loc)
		case key == "State" && l.State == "":
			l.State = val
		}
		// DHCP packet options, as in `ipconfig getpacket`.
		name, rest, ok := strings.Cut(line, " (")
		if !ok {
			continue
		}
		_, value, ok := strings.Cut(rest, "): ")
		if !ok {
			continue
		}
		switch name {
		case "server_identifier":
			l.Server = value
		case "router":
			l.Routers = ipList(value)
		case "domain_name_server":
			l.DNS = ipList(value)
		case "domain_name":
			l.Domain = value
		case "lease_time":
			if n, err := strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 32); err == nil {
				l.Length = time.Duration(n) * time.Second
			}
		}
	}
	return l, true
}

// ipv4Section is the text of the "IPv4 : <array>" block, which ends where
// the "IPv6" block or the top-level keys begin.
func ipv4Section(out string) string {
	i := strings.Index(out, "IPv4 : <array>")
	if i < 0 {
		return ""
	}
	rest := out[i:]
	for _, end := range []string{"\n  IPv6 : <array>", "\n  InterfaceType :", "\n  LinkStatusActive :"} {
		if j := strings.Index(rest, end); j >= 0 {
			rest = rest[:j]
		}
	}
	return rest
}

// ipList reads "{10.1.2.1, 10.1.2.2}" or a bare address.
func ipList(s string) []string {
	s = strings.Trim(strings.TrimSpace(s), "{}")
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

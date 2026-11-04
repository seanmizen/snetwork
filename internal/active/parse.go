package active

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

var pingTime = regexp.MustCompile(`time[=<]([0-9.]+) ?ms`)

// ParsePing reads the round-trip time from one `ping -c 1` reply.
func ParsePing(out string) (float64, bool) {
	m := pingTime.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	return f, err == nil
}

var tsPong = regexp.MustCompile(`pong from \S+ \([^)]*\) via (\S+) in ([0-9.]+)ms`)

// ParseTailscalePing reads `tailscale ping -c 1`:
//
//	pong from asus (100.64.0.11) via 10.1.2.3:41641 in 4ms
//	pong from asus (100.64.0.11) via DERP(lhr) in 31ms
func ParseTailscalePing(out string) (path string, ms float64, ok bool) {
	m := tsPong.FindStringSubmatch(out)
	if m == nil {
		return "", 0, false
	}
	ms, _ = strconv.ParseFloat(m[2], 64)
	via := m[1]
	if r, found := strings.CutPrefix(via, "DERP("); found {
		return "relay " + strings.TrimSuffix(r, ")"), ms, true
	}
	return "direct " + via, ms, true
}

// ParseTrace reads `cloudflare.com/cdn-cgi/trace`: key=value lines.
func ParseTrace(out string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	return kv
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// ParseTraceroute reads `traceroute -n` and says whether a hop sits in the
// shared address space of carrier-grade NAT (100.64.0.0/10).
func ParseTraceroute(out string) string {
	hops := 0
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		a, err := netip.ParseAddr(f[1])
		if err != nil {
			continue // "*": no answer from this hop
		}
		hops++
		if cgnat.Contains(a) {
			return "yes (hop " + strconv.Itoa(n) + ": " + a.String() + ")"
		}
	}
	if hops == 0 {
		return "unknown: no hops answered"
	}
	return "no"
}

// ShortName turns a DNS answer into a device name: "minix.lan." -> "minix".
// Reverse-zone answers and empty names give "".
func ShortName(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" || strings.HasSuffix(name, ".arpa") {
		return ""
	}
	label, _, _ := strings.Cut(name, ".")
	return label
}

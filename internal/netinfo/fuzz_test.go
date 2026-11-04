package netinfo

import (
	"testing"
	"time"
)

// FuzzParsers feeds arbitrary text to every parser. None may panic: the
// commands' output changes between macOS releases.
func FuzzParsers(f *testing.F) {
	for _, seed := range []string{
		"? (10.1.2.1) at 0:16:3e:0:0:1 on en0 ifscope [ethernet]",
		"default 10.1.2.1 UGScg en0\n10.1/16 link#1 UCS en0",
		"en0: flags=8863<UP,RUNNING> mtu 1500\n\tinet 10.1.2.3 netmask 0xffffff00\n\tstatus: active",
		"resolver #1\n  nameserver[0] : 10.1.2.1\n  if_index : 11 (en0)\n  order : 1",
		"  IPv4 : <array> {\n ConfigMethod : DHCP\nrouter (ip_mult): {10.1.2.1}\nlease_time (uint32): 0xzz",
		"tcp4 0 0 *.22 *.* LISTEN 0 0 1 1 launchd:1 0 0 0 0 0 1 0 0",
		"(1) Wi-Fi\n(Hardware Port: Wi-Fi, Device: en0)",
		`* (Connected) x VPN (a) "b" [c]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		parseIfconfig(s)
		parseServiceOrder(s)
		parseRoutes(s)
		parseARP(s)
		parseDNS(s)
		parseLease("en0", s, time.UTC)
		parseListening(s)
		parseVPNServices(s)
		parseFirewall(s, s)
		_, _ = parseTailscale([]byte(s))
		parseIPAddr([]byte(s))
		parseIPRoute([]byte(s))
		parseNeigh(s)
		parseSS(s)
		parseIwDev(s)
		parseDhcpcd("eth0", s)
		parseResolvConf(s, s)
		parseKnownHosts(s)
		parseSSHConfig(s)
		b := []byte(s)
		for _, goos := range []string{"darwin", "linux"} {
			out := map[string][]byte{}
			for _, k := range []string{"ifconfig", "services", "routes", "arp", "dns", "vpnlist", "listening",
				"tailscale", "getsummary-en0", "ip-addr", "ip-route", "ip-neigh", "resolv", "ss", "iw-dev",
				"iw-link-wlan0", "known_hosts", "ssh_config", "names-dns"} {
				out[k] = b
			}
			Build(Raw{OS: goos, Out: out}, time.Unix(0, 0))
		}
	})
}

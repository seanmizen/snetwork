package netinfo

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseDestination(t *testing.T) {
	cases := map[string]string{
		"default":     "0.0.0.0/0",
		"10.9.8/24":   "10.9.8.0/24",
		"100.64/10":   "100.64.0.0/10",
		"0/1":         "0.0.0.0/1",
		"128.0/1":     "128.0.0.0/1",
		"127":         "127.0.0.0/8",
		"169.254":     "169.254.0.0/16",
		"192.168.64":  "192.168.64.0/24",
		"10.1.2.3/32": "10.1.2.3/32",
		"10.1.2.3":    "10.1.2.3/32",
	}
	for in, want := range cases {
		p, ok := parseDestination(in)
		if !ok || p.String() != want {
			t.Errorf("parseDestination(%q) = %v, %v; want %s", in, p, ok, want)
		}
	}
	for _, bad := range []string{"fe80::1", "link#11", "10.1.2.3.4", "10/33", ""} {
		if _, ok := parseDestination(bad); ok {
			t.Errorf("parseDestination(%q) accepted", bad)
		}
	}
}

func TestParseRoutesDropsHosts(t *testing.T) {
	out := "Destination        Gateway            Flags               Netif Expire\n" +
		"default            10.1.2.1           UGScg                 en0\n" +
		"10.1.2/24          link#11            UCS                   en0      !\n" +
		"10.1.2.1           00:16:3e:00:00:01  UHLWIir               en0   1193\n"
	got := parseRoutes(out)
	if len(got) != 2 || got[0].prefix.Bits() != 0 || got[1].prefix.String() != "10.1.2.0/24" {
		t.Fatalf("got %+v", got)
	}
}

func TestMAC(t *testing.T) {
	if got := normalizeMAC("34:97:F6:27:95:6"); got != "34:97:f6:27:95:06" {
		t.Errorf("normalizeMAC = %q", got)
	}
	for mac, want := range map[string]bool{
		"02:00:00:00:00:01": true, "ea:76:84:90:d8:56": true, "3a:90:a6:39:a7:31": true,
		"00:16:3e:00:00:01": false, "7c:39:53:d0:ed:67": false, "": false,
	} {
		if got := privateMAC(mac); got != want {
			t.Errorf("privateMAC(%q) = %v", mac, got)
		}
	}
}

func TestParseARP(t *testing.T) {
	out := "? (10.1.2.1) at 0:16:3e:0:0:1 on en0 ifscope [ethernet]\n" +
		"? (10.1.2.2) at (incomplete) on en0 ifscope [ethernet]\n" +
		"? (10.1.2.255) at ff:ff:ff:ff:ff:ff on en0 ifscope [ethernet]\n" +
		"? (224.0.0.251) at 1:0:5e:0:0:fb on en0 ifscope permanent [ethernet]\n"
	got := parseARP(out)
	if len(got) != 1 || got[0].ip.String() != "10.1.2.1" || got[0].mac != "00:16:3e:00:00:01" || got[0].netif != "en0" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseIfconfig(t *testing.T) {
	out := "en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500\n" +
		"\tether 2:0:0:0:0:1\n" +
		"\tinet6 fe80::1%en0 prefixlen 64 secured scopeid 0xb\n" +
		"\tinet 10.1.2.3 netmask 0xffffff00 broadcast 10.1.2.255\n" +
		"\tinet6 2001:db8::5 prefixlen 64 autoconf secured\n" +
		"\tstatus: active\n" +
		"utun7: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1280\n" +
		"\tinet 100.64.0.10 --> 100.64.0.10 netmask 0xffffffff\n"
	got := parseIfconfig(out)
	if len(got) != 2 {
		t.Fatalf("got %d interfaces", len(got))
	}
	en0 := got[0]
	if en0.name != "en0" || en0.mtu != 1500 || en0.mac != "02:00:00:00:00:01" || en0.status != "active" ||
		len(en0.ipv4) != 1 || en0.ipv4[0].String() != "10.1.2.3/24" ||
		!reflect.DeepEqual(en0.ipv6, []string{"2001:db8::5"}) || !en0.has("RUNNING") {
		t.Errorf("en0 = %+v", en0)
	}
	if got[1].ipv4[0].String() != "100.64.0.10/32" {
		t.Errorf("utun7 = %+v", got[1])
	}
}

func TestParseServiceOrder(t *testing.T) {
	out := "An asterisk (*) denotes that a network service is disabled.\n" +
		"(1) USB 10/100/1000 LAN\n(Hardware Port: USB 10/100/1000 LAN, Device: en8)\n\n" +
		"(*) Thunderbolt Bridge\n(Hardware Port: Thunderbolt Bridge, Device: bridge0)\n\n" +
		"(3) Tailscale\n(Hardware Port: io.tailscale.ipn.macos, Device: )\n"
	got := parseServiceOrder(out)
	want := []service{
		{order: 1, name: "USB 10/100/1000 LAN", port: "USB 10/100/1000 LAN", device: "en8"},
		{order: 2, name: "Thunderbolt Bridge", port: "Thunderbolt Bridge", device: "bridge0", disabled: true},
		{order: 3, name: "Tailscale", port: "io.tailscale.ipn.macos", device: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestParseListening(t *testing.T) {
	head := "tcp4       0      0  "
	tail := "  00100 00000006 0000000000000001 00000000 00000800      1      0 000000\n"
	line := func(proto, local, proc string) string {
		return strings.Replace(head, "tcp4", proto, 1) + local + "  *.*  LISTEN  0  0  131072  131072  " + proc + tail
	}
	out := line("tcp4", "*.7000", "ControlCenter:500") +
		line("tcp6", "*.7000", "ControlCenter:500") +
		line("tcp4", "127.0.0.1.631", "cupsd:310") +
		line("tcp6", "::1.631", "cupsd:310") +
		line("tcp4", "10.1.2.3.8080", "Google Chrome He:1136") +
		"udp4       0      0  *.5353  *.*  0 0 1 1 mDNSResponder:1" + tail
	got := parseListening(out)
	want := []Listener{
		{Port: 7000, Address: "*", Family: "both", Process: "ControlCenter", PID: 500},
		{Port: 8080, Address: "10.1.2.3", Family: "ipv4", Process: "Google Chrome He", PID: 1136},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestParseVPNServicesAndFirewall(t *testing.T) {
	out := "Available network connection services in the current set (*=enabled):\n" +
		`* (Connected)      0000 VPN (io.tailscale.ipn.macos) "Tailscale"   [VPN:io.tailscale.ipn.macos]` + "\n" +
		`  (Disconnected)   0000 VPN (com.example.vpn) "Example VPN"   [VPN:com.example.vpn]` + "\n"
	got := parseVPNServices(out)
	want := []VPNService{{"Tailscale", "Connected", true}, {"Example VPN", "Disconnected", false}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	for _, c := range []struct{ global, stealth, app, detail string }{
		{"Firewall is enabled. (State = 1)", "Firewall stealth mode is on", "on", "stealth on"},
		{"Firewall is disabled. (State = 0)", "Firewall stealth mode is off", "off", "stealth off"},
		{"Firewall is blocking all non-essential incoming connections. (State = 2)", "", "block all", ""},
	} {
		fw := parseFirewall(c.global, c.stealth)
		if len(fw.Tools) != 2 || fw.Tools[0].State != c.app || fw.Tools[0].Detail != c.detail || fw.Tools[1].Name != "pf" {
			t.Errorf("%q: got %+v", c.global, fw)
		}
	}
}

func TestParseLeaseIgnoresDHCPv6(t *testing.T) {
	out := "<dictionary> {\n  IPv4 : <array> {\n    0 : <dictionary> {\n" +
		"      Addresses : <array> {\n        0 : 10.1.2.3\n      }\n" +
		"      ConfigMethod : DHCP\n      DHCP : <dictionary> {\n" +
		"        LeaseExpirationTime : 11/04/2026 18:20:17\n        LeaseStartTime : 11/03/2026 18:20:17\n" +
		"router (ip_mult): {10.1.2.1}\ndomain_name_server (ip_mult): {10.1.2.1, 1.1.1.1}\n" +
		"server_identifier (ip): 10.1.2.1\nlease_time (uint32): 0x15180\n        State : BOUND\n" +
		"      }\n    }\n  }\n  IPv6 : <array> {\n    0 : <dictionary> {\n" +
		"      DHCPv6 : <dictionary> {\n  DNS_SERVERS (23) Length 16: fe80::1\n        State : InformComplete\n" +
		"      }\n    }\n  }\n  InterfaceType : WiFi\n}\n"
	l, ok := parseLease("en0", out, time.UTC)
	if !ok {
		t.Fatal("no lease")
	}
	want := Lease{
		Interface: "en0", Address: "10.1.2.3", Server: "10.1.2.1",
		Routers: []string{"10.1.2.1"}, DNS: []string{"10.1.2.1", "1.1.1.1"},
		Length:  24 * time.Hour,
		Start:   time.Date(2026, 11, 3, 18, 20, 17, 0, time.UTC),
		Expires: time.Date(2026, 11, 4, 18, 20, 17, 0, time.UTC),
		State:   "BOUND",
	}
	if !reflect.DeepEqual(l, want) {
		t.Errorf("got  %+v\nwant %+v", l, want)
	}
	if _, ok := parseLease("utun7", "<dictionary> {\n  IPv4 : <array> {\n ConfigMethod : Manual\n", time.UTC); ok {
		t.Error("a manual address is not a lease")
	}
}

func TestParseDNSOrder(t *testing.T) {
	out := "DNS configuration\n\nresolver #1\n  nameserver[0] : 10.1.2.1\n  if_index : 11 (en0)\n  order    : 200000\n\n" +
		"resolver #2\n  search domain[0] : example.ts.net\n  nameserver[0] : 100.100.100.100\n" +
		"  if_index : 29 (utun7)\n  flags    : Supplemental, Request A records\n  order    : 100800\n\n" +
		"resolver #3\n  domain   : local\n  options  : mdns\n  order    : 300000\n\n" +
		"DNS configuration (for scoped queries)\n\nresolver #1\n  nameserver[0] : 9.9.9.9\n"
	d := parseDNS(out)
	if len(d.General) != 2 || d.General[0].Nameservers[0] != "100.100.100.100" ||
		d.General[0].Interface != "utun7" || !reflect.DeepEqual(d.General[0].Flags, []string{"Supplemental"}) {
		t.Errorf("general = %+v", d.General)
	}
	if len(d.Domain) != 1 || d.Domain[0].Domain != "local" || d.Domain[0].Options != "mdns" {
		t.Errorf("domain = %+v", d.Domain)
	}
}

func TestWiFiFromRaw(t *testing.T) {
	w := WiFiFromRaw(WiFiRaw{Interface: "en0", PowerOn: true, RSSI: -60, Noise: -90, Rate: 526, Channel: 112, Band: 2, Width: 3, PHY: 5, Security: 4})
	if !w.Associated || w.Band != "5 GHz" || w.Width != "80 MHz" || w.PHY != "802.11ac" || w.Security != "WPA2 Personal" {
		t.Errorf("got %+v", w)
	}
	if w := WiFiFromRaw(WiFiRaw{Interface: "en0", PowerOn: true}); w.Associated {
		t.Error("channel 0 means not associated")
	}
	if w := WiFiFromRaw(WiFiRaw{Interface: "en0", PowerOn: true, Channel: 1, Security: -1}); w.Security != "unknown (-1)" {
		t.Errorf("security = %q", w.Security)
	}
}

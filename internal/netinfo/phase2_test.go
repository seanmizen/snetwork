package netinfo_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

func TestNames(t *testing.T) {
	s := scenario(t, "home")
	g := s.Devices[0]
	byIP := map[string]netinfo.Device{}
	for _, d := range g.Devices {
		byIP[d.IP] = d
	}
	want := map[string][2]string{
		"10.20.30.1":   {"gw", "ssh config"},
		"10.20.30.103": {"alpha", "known_hosts"},
		"10.20.30.120": {"printer", "ssh config"},
	}
	for ip, w := range want {
		d := byIP[ip]
		if d.Name != w[0] || d.NameSource != w[1] {
			t.Errorf("%s: name %q from %q, want %q from %q", ip, d.Name, d.NameSource, w[0], w[1])
		}
	}
	// alpha's second address folds into its first entry
	if _, ok := byIP["10.20.30.108"]; ok || !reflect.DeepEqual(byIP["10.20.30.103"].Also, []string{"10.20.30.108"}) {
		t.Errorf("alpha = %+v", byIP["10.20.30.103"])
	}
	if *g.Count != len(g.Devices) {
		t.Errorf("count %d, entries %d", *g.Count, len(g.Devices))
	}
	if byIP["10.20.30.1"].Vendor != "Xensource" || byIP["10.20.30.100"].Vendor != "" {
		t.Errorf("vendors: %q, %q (a private address has no vendor)", byIP["10.20.30.1"].Vendor, byIP["10.20.30.100"].Vendor)
	}
}

func TestLinux(t *testing.T) {
	s := scenario(t, "linux")
	if len(s.Problems) > 0 {
		t.Fatalf("problems: %v", s.Problems)
	}
	kinds := map[string]string{}
	for _, i := range s.Interfaces {
		kinds[i.Name] = i.Kind + " " + i.State
	}
	want := map[string]string{
		"lo": "loopback up", "enp1s0": "ethernet up", "wlp2s0": "wifi up",
		"docker0": "bridge no link", "tailscale0": "tunnel up", "veth12ab": "virtual up",
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("interfaces = %v", kinds)
	}
	if len(s.Defaults) != 2 || s.Defaults[0].Interface != "enp1s0" || s.Location.Interface != "enp1s0" {
		t.Errorf("defaults = %+v", s.Defaults)
	}
	via := map[string]string{}
	for _, sn := range s.Subnets {
		via[sn.Prefix] = sn.Via + " " + sn.Router
	}
	if via["10.40.50.0/24"] != "vpn bravo" || via["192.168.50.0/24"] != "direct " || via["172.17.0.0/16"] != "direct " {
		t.Errorf("subnets = %v", via)
	}
	if n := s.Devices[0].Count; n == nil || *n != 2 {
		t.Errorf("devices = %+v", s.Devices[0])
	}
	if d := s.Devices[0].Devices[1]; d.Name != "laptop" || d.NameSource != "tailscale" {
		t.Errorf("laptop = %+v", d)
	}
	if len(s.DHCP) != 2 {
		t.Fatalf("dhcp = %+v", s.DHCP)
	}
	l := s.DHCP[0]
	if l.Server != "192.168.50.1" || l.Length != 2*time.Hour || !l.Expires.Equal(now.Add(time.Hour)) ||
		!reflect.DeepEqual(l.DNS, []string{"192.168.50.1", "1.1.1.1"}) || l.Start.IsZero() {
		t.Errorf("enp1s0 lease = %+v", l)
	}
	if s.DHCP[1].Server != "" || !s.DHCP[1].Expires.Equal(now.Add(30*time.Minute)) {
		t.Errorf("wlp2s0 lease = %+v", s.DHCP[1])
	}
	if len(s.DNS.General) != 2 || s.DNS.General[0].Nameservers[0] != "127.0.0.53" || s.DNS.General[1].Nameservers[0] != "192.168.50.1" {
		t.Errorf("dns = %+v", s.DNS)
	}
	w := s.WiFi
	if w == nil || !w.Associated || w.Name != "example-net" || w.Channel != 36 || w.Band != "5 GHz" ||
		w.Width != "80 MHz" || w.RSSI != -52 || w.RateMbps != 390 || w.PHY != "802.11ac" {
		t.Errorf("wifi = %+v", w)
	}
	if fw := s.Firewall.Tools; fw[0].Name != "ufw" || fw[0].State != "active" || fw[1].State != "inactive" {
		t.Errorf("firewall = %+v", fw)
	}
	wantL := []netinfo.Listener{
		{Port: 22, Address: "*", Family: "both"},
		{Port: 9100, Address: "*", Family: "both", Process: "node_exporter", PID: 901},
		{Port: 41641, Address: "100.64.0.21", Family: "ipv4", Process: "tailscaled", PID: 812},
	}
	if !reflect.DeepEqual(s.Listening, wantL) {
		t.Errorf("listening = %+v", s.Listening)
	}
}

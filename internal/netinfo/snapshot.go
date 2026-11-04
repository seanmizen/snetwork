// Package netinfo turns the output of macOS network commands into a Snapshot.
//
// Every parser here is a pure function of text in, values out, so it can be
// tested against fixture files. Nothing in this package runs a command or
// sends a packet: package collect does the reading.
package netinfo

import "time"

// Snapshot is everything snetwork knows about this machine's network, read
// passively.
type Snapshot struct {
	Taken      time.Time     `json:"taken"`
	Location   Location      `json:"location"`
	Interfaces []Interface   `json:"interfaces"`
	Defaults   []Route       `json:"default_routes"`
	Subnets    []Subnet      `json:"subnets"`
	Devices    []DeviceGroup `json:"devices"`
	DHCP       []Lease       `json:"dhcp"`
	DNS        DNS           `json:"dns"`
	WiFi       *WiFi         `json:"wifi"`
	VPN        VPN           `json:"vpn"`
	Firewall   Firewall      `json:"firewall"`
	Listening  []Listener    `json:"listening"`
	// Problems lists sources that could not be read. The sections that
	// depend on them are empty, not wrong.
	Problems []string `json:"problems,omitempty"`
}

// Location is the one-line answer to "where am I".
type Location struct {
	Kind      string `json:"kind"`  // lan, hotspot, other, offline
	Label     string `json:"label"` // e.g. "LAN of asus", "iPhone hotspot"
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
	// Router is the tailnet peer that routes this LAN for you when you are
	// away from it.
	Router string `json:"router,omitempty"`
}

// Interface is one network interface, or a configured network service whose
// device is not present (an unplugged USB adapter).
type Interface struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"` // "Wi-Fi", "USB 10/100/1000 LAN", "Tailscale"
	Kind    string   `json:"kind"`  // wifi, ethernet, iphone, tunnel, bridge, loopback, other
	State   string   `json:"state"` // up, no link, down, not plugged in
	IPv4    []string `json:"ipv4,omitempty"`
	IPv6    []string `json:"ipv6,omitempty"` // global and unique-local; never link-local
	MAC     string   `json:"mac,omitempty"`
	MTU     int      `json:"mtu,omitempty"`
	Default bool     `json:"default_route"`
	// Order is the position in System Settings' service order (1 first),
	// or 0 for an interface that is not a network service.
	Order int `json:"service_order,omitempty"`
}

// Route is one IPv4 route from the routing table.
type Route struct {
	Prefix    string `json:"prefix"`
	Gateway   string `json:"gateway"`
	Interface string `json:"interface"`
	Flags     string `json:"flags"`
}

// Subnet is a network that this machine can reach, directly or through a VPN.
type Subnet struct {
	Prefix    string `json:"prefix"`
	Via       string `json:"via"` // direct, vpn
	Interface string `json:"interface"`
	// Router is the tailnet peer that routes this subnet. For a direct
	// subnet, it is the peer that would route it when you are away.
	Router string `json:"router,omitempty"`
	Note   string `json:"note,omitempty"` // "tailnet", "all traffic"
}

// DeviceGroup is the devices seen on one subnet.
type DeviceGroup struct {
	Prefix    string `json:"prefix"`
	Via       string `json:"via"`
	Interface string `json:"interface"`
	Router    string `json:"router,omitempty"`
	// Count is nil for a subnet this machine cannot see: one behind a VPN.
	Count   *int     `json:"count"`
	Devices []Device `json:"devices,omitempty"`
}

// Device is one ARP cache entry.
type Device struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Interface string `json:"interface"`
	// Private is true for a locally administered (randomised) hardware
	// address, as phones use for each Wi-Fi network.
	Private bool `json:"private_address"`
	Gateway bool `json:"gateway"`
}

// Lease is the DHCP state of one interface.
type Lease struct {
	Interface string        `json:"interface"`
	Address   string        `json:"address"`
	Server    string        `json:"server"`
	Routers   []string      `json:"routers"`
	DNS       []string      `json:"dns"`
	Domain    string        `json:"domain,omitempty"`
	Length    time.Duration `json:"length_ns"`
	Start     time.Time     `json:"start"`
	Expires   time.Time     `json:"expires"`
	State     string        `json:"state"`
}

// DNS is the resolver configuration.
type DNS struct {
	// General resolvers answer any name, in the order macOS asks them.
	General []Resolver `json:"general"`
	// Domain resolvers answer only names under their domain.
	Domain []Resolver `json:"domain"`
}

// Resolver is one resolver block from scutil --dns.
type Resolver struct {
	Domain      string   `json:"domain,omitempty"`
	Search      []string `json:"search,omitempty"`
	Nameservers []string `json:"nameservers,omitempty"`
	Interface   string   `json:"interface,omitempty"`
	Options     string   `json:"options,omitempty"` // "mdns" for multicast DNS
	Flags       []string `json:"flags,omitempty"`
	Order       int      `json:"order"`
}

// WiFi is the current Wi-Fi link.
type WiFi struct {
	Interface  string  `json:"interface"`
	PowerOn    bool    `json:"power_on"`
	Associated bool    `json:"associated"`
	Name       string  `json:"name"` // "" when macOS hides it
	Band       string  `json:"band,omitempty"`
	Channel    int     `json:"channel,omitempty"`
	Width      string  `json:"width,omitempty"`
	RSSI       int     `json:"rssi_dbm,omitempty"`
	Noise      int     `json:"noise_dbm,omitempty"`
	RateMbps   float64 `json:"rate_mbps,omitempty"`
	PHY        string  `json:"phy,omitempty"`
	Security   string  `json:"security,omitempty"`
	Country    string  `json:"country,omitempty"`
}

// VPN is Tailscale plus any other VPN services and tunnels.
type VPN struct {
	Tailscale Tailscale    `json:"tailscale"`
	Others    []VPNService `json:"others,omitempty"`
	// AllTrafficVia names a tunnel that is not Tailscale and that takes all
	// IPv4 traffic (routes 0/1 and 128.0/1), as UniFi Teleport does.
	AllTrafficVia string `json:"all_traffic_via,omitempty"`
}

// Tailscale is the local Tailscale daemon's view.
type Tailscale struct {
	Installed bool     `json:"installed"`
	State     string   `json:"state,omitempty"` // Running, Stopped, NeedsLogin, ...
	Tailnet   string   `json:"tailnet,omitempty"`
	Suffix    string   `json:"magicdns_suffix,omitempty"`
	Self      Peer     `json:"self"`
	Peers     []Peer   `json:"peers"`
	ExitNode  string   `json:"exit_node,omitempty"`
	Health    []string `json:"health,omitempty"`
}

// Peer is one tailnet node.
type Peer struct {
	Name     string   `json:"name"`
	OS       string   `json:"os,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Online   bool     `json:"online"`
	Routes   []string `json:"routes,omitempty"` // subnets it routes for the tailnet
	Direct   string   `json:"direct,omitempty"` // address of a direct path, if any
	Relay    string   `json:"relay,omitempty"`  // home DERP relay
	ExitNode bool     `json:"exit_node"`
	LastSeen string   `json:"last_seen,omitempty"`
}

// VPNService is a VPN configured in System Settings.
type VPNService struct {
	Name    string `json:"name"`
	State   string `json:"state"` // Connected, Disconnected, ...
	Enabled bool   `json:"enabled"`
}

// Firewall is what can be read about packet filtering without sudo.
type Firewall struct {
	App     string `json:"application_firewall"` // on, off, block all, unknown
	Stealth string `json:"stealth_mode"`         // on, off, unknown
	PF      string `json:"pf"`
}

// Listener is a TCP port that accepts connections from beyond this machine.
type Listener struct {
	Port    int    `json:"port"`
	Address string `json:"address"` // "*" or a specific address
	Family  string `json:"family"`  // ipv4, ipv6, both
	Process string `json:"process,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

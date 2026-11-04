package netinfo

import (
	"bufio"
	"encoding/json"
	"sort"
	"strings"
)

// tsStatus is the part of `tailscale status --json` that snetwork reads.
type tsStatus struct {
	BackendState   string
	MagicDNSSuffix string
	Health         []string
	CurrentTailnet *struct{ Name string }
	Self           *tsPeer
	Peer           map[string]*tsPeer
}

type tsPeer struct {
	HostName       string
	DNSName        string
	OS             string
	TailscaleIPs   []string
	Online         bool
	PrimaryRoutes  []string
	CurAddr        string
	Relay          string
	ExitNode       bool
	ExitNodeOption bool
	LastSeen       string
}

// parseTailscale reads `tailscale status --json`.
func parseTailscale(b []byte) (Tailscale, error) {
	var st tsStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return Tailscale{}, err
	}
	t := Tailscale{Installed: true, State: st.BackendState, Suffix: st.MagicDNSSuffix, Health: st.Health}
	if st.CurrentTailnet != nil {
		t.Tailnet = st.CurrentTailnet.Name
	}
	if st.Self != nil {
		t.Self = st.Self.peer()
	}
	for _, p := range st.Peer {
		peer := p.peer()
		t.Peers = append(t.Peers, peer)
		if p.ExitNode {
			t.ExitNode = peer.Name
		}
	}
	sort.Slice(t.Peers, func(i, j int) bool { return t.Peers[i].Name < t.Peers[j].Name })
	return t, nil
}

// peer names a node by the first label of its MagicDNS name. HostName is
// "localhost" for every iPhone.
func (p *tsPeer) peer() Peer {
	name, _, _ := strings.Cut(p.DNSName, ".")
	if name == "" {
		name = p.HostName
	}
	out := Peer{
		Name: name, OS: p.OS, IPs: p.TailscaleIPs, Online: p.Online,
		Routes: p.PrimaryRoutes, Direct: p.CurAddr, Relay: p.Relay, ExitNode: p.ExitNode,
	}
	if !strings.HasPrefix(p.LastSeen, "0001-") {
		out.LastSeen = p.LastSeen
	}
	return out
}

// parseVPNServices reads `scutil --nc list`. Each VPN line looks like
//
//	(Connected)  B2EA… VPN (io.tailscale.ipn.macos) "Tailscale"  [VPN:…]
//
// with "* " in front when the service is enabled.
func parseVPNServices(out string) []VPNService {
	var list []VPNService
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		open := strings.Index(line, "(")
		close := strings.Index(line, ")")
		q1 := strings.Index(line, `"`)
		q2 := strings.LastIndex(line, `"`)
		if open < 0 || close < open || q1 < 0 || q2 <= q1 || !strings.Contains(line, " VPN ") {
			continue
		}
		list = append(list, VPNService{
			Name:    line[q1+1 : q2],
			State:   line[open+1 : close],
			Enabled: strings.HasPrefix(strings.TrimSpace(line), "*"),
		})
	}
	return list
}

// parseFirewall reads socketfilterfw --getglobalstate and --getstealthmode.
func parseFirewall(global, stealth string) Firewall {
	app := FirewallTool{Name: "macOS app firewall", State: "unknown"}
	switch {
	case strings.Contains(global, "State = 2"), strings.Contains(global, "blocking all"):
		app.State = "block all"
	case strings.Contains(global, "is enabled"):
		app.State = "on"
	case strings.Contains(global, "is disabled"):
		app.State = "off"
	}
	switch {
	case strings.Contains(stealth, "is on"), strings.Contains(stealth, "is enabled"):
		app.Detail = "stealth on"
	case strings.Contains(stealth, "is off"), strings.Contains(stealth, "is disabled"):
		app.Detail = "stealth off"
	}
	return Firewall{Tools: []FirewallTool{app, {Name: "pf", State: "unknown", Detail: "needs root: sudo pfctl -s info"}}}
}

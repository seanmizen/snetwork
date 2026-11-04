package render

import (
	"fmt"
	"strings"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// Block is one piece of output: the headline, one section, or the list of
// problems. Streaming output prints each block once it is Done.
type Block struct {
	Name string
	Text string
}

// Blocks renders a view: "" for the summary, "all", or one section with
// an optional item.
func Blocks(view, item string, s netinfo.Snapshot, st Style) ([]Block, error) {
	var out []Block
	switch view {
	case "", "all":
		out = append(out, Block{"headline", Headline(s, st)})
		for _, name := range Sections {
			if name == "internet" && view == "" && s.Active == nil {
				continue // an active-only section has nothing to say in a passive summary
			}
			var body string
			if view == "" {
				body = summaryBody(name, s, st)
				body = "\n" + st.heading(name, "snetwork "+name) + body
			} else {
				b, _ := fullBody(name, s, "", st)
				body = "\n" + st.heading(name, "") + b
			}
			out = append(out, Block{name, body})
		}
	default:
		body, err := fullBody(view, s, item, st)
		if err != nil {
			return nil, err
		}
		out = append(out, Block{view, st.heading(view, "") + body})
	}
	if p := problems(s, st); p != "" {
		out = append(out, Block{"problems", p})
	}
	return out, nil
}

// Join concatenates blocks.
func Join(blocks []Block) string {
	var b strings.Builder
	for _, bl := range blocks {
		b.WriteString(bl.Text)
	}
	return b.String()
}

// Summary is plain `snetwork`: every section, a few lines each.
func Summary(s netinfo.Snapshot, st Style) string {
	b, _ := Blocks("", "", s, st)
	return Join(b)
}

// All is `snetwork all`: every section in full.
func All(s netinfo.Snapshot, st Style) string {
	b, _ := Blocks("all", "", s, st)
	return Join(b)
}

// Section is `snetwork <name> [item]`: one section in full.
func Section(name, item string, s netinfo.Snapshot, st Style) (string, error) {
	b, err := Blocks(name, item, s, st)
	if err != nil {
		return "", err
	}
	return Join(b), nil
}

// Done reports whether a block has every result it waits for. Without
// --active, and without remote routers to ask, every block is done.
func Done(name string, s netinfo.Snapshot) bool {
	if name == "devices" || name == "headline" {
		for _, g := range s.Devices {
			if g.Pending {
				return name == "headline"
			}
		}
	}
	a := s.Active
	if a == nil {
		return true
	}
	switch name {
	case "devices":
		for _, sw := range a.Sweeps {
			if !sw.Done {
				return false
			}
		}
		return a.Names == nil || a.Names.Done
	case "subnets":
		for _, g := range a.Gateways {
			if !g.Done {
				return false
			}
		}
		return peersDone(a)
	case "vpn":
		return peersDone(a)
	case "dns":
		for _, d := range a.DNS {
			if !d.Done {
				return false
			}
		}
	case "wifi":
		return a.WiFiScan == nil || a.WiFiScan.Done
	case "internet":
		return a.Internet == nil || a.Internet.Done
	}
	return true
}

// AllDone reports whether every block is done.
func AllDone(s netinfo.Snapshot) bool {
	for _, n := range append([]string{"headline"}, Sections...) {
		if !Done(n, s) {
			return false
		}
	}
	return true
}

func peersDone(a *netinfo.Active) bool {
	for _, p := range a.Peers {
		if !p.Done {
			return false
		}
	}
	return true
}

func summaryBody(name string, s netinfo.Snapshot, st Style) string {
	switch name {
	case "interfaces":
		return interfacesSummary(s, st)
	case "subnets":
		return subnetsTable(s, st)
	case "devices":
		return devicesSummary(s, st)
	case "dhcp":
		return dhcpBody(s, "", false, st)
	case "dns":
		return dnsSummary(s, st)
	case "wifi":
		return wifiSummary(s, st)
	case "vpn":
		return vpnSummary(s, st)
	case "internet":
		return internetBody(s, false, st)
	case "firewall":
		return firewallBody(s, false, st)
	case "listening":
		return listeningSummary(s, st)
	}
	return ""
}

func fullBody(name string, s netinfo.Snapshot, item string, st Style) (string, error) {
	takesItem := map[string]bool{"interfaces": true, "devices": true, "dhcp": true, "vpn": true}
	if item != "" && !takesItem[name] {
		return "", ItemError{fmt.Sprintf("%s takes no item (got %q)", name, item)}
	}
	switch name {
	case "interfaces":
		return interfacesFull(s, item, st)
	case "subnets":
		return subnetsTable(s, st) + defaultsBody(s, st), nil
	case "devices":
		return devicesFull(s, item, st)
	case "dhcp":
		if item != "" && !hasLease(s, item) {
			return "", ItemError{fmt.Sprintf("no DHCP lease on %q", item)}
		}
		return dhcpBody(s, item, true, st), nil
	case "dns":
		return dnsFull(s, st), nil
	case "wifi":
		return wifiFull(s, st), nil
	case "vpn":
		return vpnFull(s, item, st)
	case "internet":
		return internetBody(s, true, st), nil
	case "firewall":
		return firewallBody(s, true, st), nil
	case "listening":
		return listeningFull(s, st), nil
	}
	return "", ItemError{fmt.Sprintf("no section %q (sections: %s)", name, strings.Join(Sections, ", "))}
}

// wait is the pending marker.
func (s Style) wait() cell { return cs(roleWait, pendingText) }

// ms formats a round-trip time.
func ms(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f ms", v)
}

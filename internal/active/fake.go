package active

import (
	"context"
	"net/netip"
	"sync"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// Fake answers every probe at once, from fixed, made-up data. Tests use it
// in place of Real, so they send nothing.
type Fake struct {
	Neighbours []byte
	Swept      []netip.Addr
	DNSAsked   []string // the servers ReverseDNS was given
	mu         sync.Mutex
}

func (f *Fake) Sweep(_ context.Context, targets []netip.Addr) error { f.Swept = targets; return nil }
func (f *Fake) RereadNeighbours(context.Context) (string, []byte, error) {
	return "arp", f.Neighbours, nil
}
func (f *Fake) Ping(context.Context, string) (float64, error) { return 3.2, nil }
func (f *Fake) TailscalePing(_ context.Context, peer string) (string, float64, error) {
	return "relay fra", 40, nil
}
func (f *Fake) Resolve(_ context.Context, server, _ string) (string, float64, error) {
	return "192.0.2.80", 12, nil
}
func (f *Fake) ReverseDNS(_ context.Context, server string, ips []string) map[string]string {
	f.mu.Lock()
	f.DNSAsked = append(f.DNSAsked, server)
	f.mu.Unlock()
	return map[string]string{"10.20.30.106": "tv"}
}
func (f *Fake) MDNS(context.Context, []string) map[string]string {
	return map[string]string{"10.20.30.106": "living-room", "10.20.30.107": "archer"}
}
func (f *Fake) WiFiScan(context.Context, int) netinfo.WiFiScan {
	return netinfo.WiFiScan{Networks: 9, SameChannel: 1, Strongest: -40}
}
func (f *Fake) Internet(context.Context) netinfo.Internet {
	return netinfo.Internet{IPv4: "192.0.2.1", CGNAT: "yes (hop 2: 100.64.1.1)", LatencyMs: 21}
}

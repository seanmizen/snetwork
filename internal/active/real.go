package active

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/seanmizen/snetwork/internal/collect"
	"github.com/seanmizen/snetwork/internal/netinfo"
	"golang.org/x/net/dns/dnsmessage"
)

// Real sends real probes. Commands go through Run, so they share the
// passive collector's lookup and error handling.
type Real struct {
	GOOS string
	Run  collect.Runner
}

// Timeouts. Short, because --active should feel quick.
const (
	sweepSettle = 1500 * time.Millisecond // time for ARP replies after a sweep
	lookupWait  = 2 * time.Second
	httpWait    = 4 * time.Second
)

// Sweep sends one UDP datagram to the discard port of each target. The OS
// must resolve each address with ARP first, which fills the ARP cache. No
// root is needed, and nothing answers the datagram itself.
func (r Real) Sweep(ctx context.Context, targets []netip.Addr) error {
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for _, a := range targets {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, 9)))
			if err != nil {
				return
			}
			_, _ = c.Write([]byte{0})
			c.Close()
		}()
	}
	wg.Wait()
	select {
	case <-time.After(sweepSettle):
	case <-ctx.Done():
	}
	return nil
}

// RereadNeighbours reads the ARP cache again after a sweep.
func (r Real) RereadNeighbours(ctx context.Context) (string, []byte, error) {
	if r.GOOS == "linux" {
		out, err := r.Run("ip-neigh", []string{"ip", "-j", "neigh"})
		return "ip-neigh", out, err
	}
	out, err := r.Run("arp", []string{"arp", "-an"})
	return "arp", out, err
}

// Ping sends one ICMP echo with the system ping, which needs no root.
func (r Real) Ping(ctx context.Context, target string) (float64, error) {
	argv := []string{"ping", "-c", "1", "-t", "2", target} // macOS: -t is the whole timeout
	if r.GOOS == "linux" {
		argv = []string{"ping", "-c", "1", "-W", "2", target}
	}
	out, err := r.Run("ping-"+target, argv)
	if ms, ok := ParsePing(string(out)); ok {
		return ms, nil
	}
	_ = err // ping exits non-zero for no reply; the reply text is the answer
	return 0, errors.New("no answer")
}

// TailscalePing pings a peer through Tailscale and reports the path.
func (r Real) TailscalePing(ctx context.Context, peer string) (string, float64, error) {
	out, err := r.Run("tsping-"+peer, []string{"tailscale", "ping", "-c", "1", "--timeout", "3s", peer})
	if path, ms, ok := ParseTailscalePing(string(out)); ok {
		return path, ms, nil
	}
	if err != nil {
		return "", 0, err
	}
	return "", 0, errors.New("no answer")
}

func resolverFor(server string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
		},
	}
}

// Resolve looks up name through one nameserver and times it.
func (r Real) Resolve(ctx context.Context, server, name string) (string, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupWait)
	defer cancel()
	start := time.Now()
	addrs, err := resolverFor(server).LookupHost(ctx, name)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return "", 0, errors.New("no answer")
	}
	return addrs[0], ms, nil
}

// ReverseDNS asks one server for the PTR name of each address. A router
// that hands out addresses often knows the name each device asked for.
func (r Real) ReverseDNS(ctx context.Context, server string, ips []string) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, lookupWait)
	defer cancel()
	res := resolverFor(server)
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, ip := range ips {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			names, err := res.LookupAddr(ctx, ip)
			if err != nil || len(names) == 0 {
				return
			}
			if n := ShortName(names[0]); n != "" {
				mu.Lock()
				out[ip] = n
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// MDNS asks the local network, by multicast, for each address's .local
// name. The queries ask for unicast replies (RFC 6762, section 5.4), so
// one ordinary UDP socket hears the answers.
func (r Real) MDNS(ctx context.Context, ips []string) map[string]string {
	out := map[string]string{}
	if len(ips) == 0 {
		return out
	}
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return out
	}
	defer conn.Close()
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	want := map[string]string{} // reverse name -> ip
	for i, ip := range ips {
		rev := reverseName(ip)
		if rev == "" {
			continue
		}
		want[strings.ToLower(rev)] = ip
		msg, err := mdnsQuery(uint16(i), rev)
		if err == nil {
			_, _ = conn.WriteTo(msg, group)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(sweepSettle))
	buf := make([]byte, 9000)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		var p dnsmessage.Parser
		if _, err := p.Start(buf[:n]); err != nil {
			continue
		}
		_ = p.SkipAllQuestions()
		for {
			h, err := p.AnswerHeader()
			if err != nil {
				break
			}
			if h.Type != dnsmessage.TypePTR {
				_ = p.SkipAnswer()
				continue
			}
			ptr, err := p.PTRResource()
			if err != nil {
				break
			}
			if ip, ok := want[strings.ToLower(h.Name.String())]; ok {
				if name := ShortName(ptr.PTR.String()); name != "" {
					out[ip] = name
				}
			}
		}
	}
	return out
}

func reverseName(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return ""
	}
	b := a.As4()
	return net.IPv4(b[3], b[2], b[1], b[0]).String() + ".in-addr.arpa."
}

func mdnsQuery(id uint16, name string) ([]byte, error) {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	// The top bit of the class asks for a unicast reply.
	if err := b.Question(dnsmessage.Question{Name: n, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET | 1<<15}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// WiFiScan counts the networks in range.
func (r Real) WiFiScan(ctx context.Context, channel int) netinfo.WiFiScan {
	return collect.ScanWiFi(channel)
}

// Internet asks Cloudflare what address it sees, over IPv4 and IPv6, and
// traces the first hops to look for carrier-grade NAT.
func (r Real) Internet(ctx context.Context) netinfo.Internet {
	var in netinfo.Internet
	var wg sync.WaitGroup
	var mu sync.Mutex
	wg.Add(4)
	go func() {
		defer wg.Done()
		start := time.Now()
		c, err := net.DialTimeout("tcp", "1.1.1.1:443", httpWait)
		if err == nil {
			c.Close()
			mu.Lock()
			in.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
			mu.Unlock()
		}
	}()
	trace := func(url string, set func(string)) {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(ctx, httpWait)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("User-Agent", "snetwork")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if ip := ParseTrace(string(b))["ip"]; ip != "" {
			mu.Lock()
			set(ip)
			mu.Unlock()
		}
	}
	go trace("https://1.1.1.1/cdn-cgi/trace", func(ip string) { in.IPv4 = ip })
	go trace("https://[2606:4700:4700::1111]/cdn-cgi/trace", func(ip string) { in.IPv6 = ip })
	go func() {
		defer wg.Done()
		out, err := r.Run("traceroute", []string{"traceroute", "-n", "-m", "4", "-q", "1", "-w", "1", "1.1.1.1"})
		mu.Lock()
		defer mu.Unlock()
		switch {
		case errors.Is(err, collect.ErrNotInstalled):
			in.CGNAT = "unknown: traceroute is not installed"
		case len(out) == 0 && err != nil:
			in.CGNAT = "unknown: " + err.Error()
		default:
			in.CGNAT = ParseTraceroute(string(out))
		}
	}()
	wg.Wait()
	if in.IPv4 == "" && in.IPv6 == "" && in.LatencyMs == 0 {
		in.Err = "no answer from the internet"
	}
	return in
}

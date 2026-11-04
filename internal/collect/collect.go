// Package collect reads this machine's network state into a netinfo.Raw.
//
// Every source is a local command, a local file or the local Tailscale
// daemon. Nothing here sends a packet on the network.
package collect

import (
	"errors"
	"os/exec"
	"strings"
	"sync"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// Runner runs one source and returns its output. key names the source (and
// the fixture file in tests); argv is the command.
type Runner func(key string, argv []string) ([]byte, error)

// WiFiReader reads the Wi-Fi interface. It returns nil when there is none.
type WiFiReader func() (*netinfo.WiFiRaw, error)

const socketfilterfw = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// sources are the commands that need no input. ipconfig getsummary runs
// after ifconfig, once per interface.
var sources = []struct {
	key  string
	argv []string
}{
	{"ifconfig", []string{"ifconfig"}},
	{"services", []string{"networksetup", "-listnetworkserviceorder"}},
	{"routes", []string{"netstat", "-rn", "-f", "inet"}},
	{"arp", []string{"arp", "-an"}},
	{"dns", []string{"scutil", "--dns"}},
	{"vpnlist", []string{"scutil", "--nc", "list"}},
	{"listening", []string{"netstat", "-anv", "-p", "tcp"}},
	{"fw-global", []string{socketfilterfw, "--getglobalstate"}},
	{"fw-stealth", []string{socketfilterfw, "--getstealthmode"}},
	{"tailscale", []string{"tailscale", "status", "--json"}},
}

// ErrNotInstalled is what a Runner returns for a tool that is not on this
// machine. Build reports it as "not installed", not as a problem.
var ErrNotInstalled = errors.New("not installed")

// Collect runs every source at once and returns their output.
func Collect(run Runner, wifi WiFiReader) netinfo.Raw {
	raw := netinfo.Raw{
		Summaries:  map[string]string{},
		Errs:       map[string]string{},
		NotPresent: map[string]bool{},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	fail := func(key string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if errors.Is(err, ErrNotInstalled) {
			raw.NotPresent[key] = true
			return
		}
		raw.Errs[key] = strings.TrimSpace(err.Error())
	}
	set := func(key string, out []byte) {
		mu.Lock()
		defer mu.Unlock()
		s := string(out)
		switch key {
		case "ifconfig":
			raw.Ifconfig = s
		case "services":
			raw.Services = s
		case "routes":
			raw.Routes = s
		case "arp":
			raw.ARP = s
		case "dns":
			raw.DNS = s
		case "vpnlist":
			raw.VPNList = s
		case "listening":
			raw.Listening = s
		case "fw-global":
			raw.FWGlobal = s
		case "fw-stealth":
			raw.FWStealth = s
		case "tailscale":
			raw.Tailscale = out
		default:
			if iface, ok := strings.CutPrefix(key, "getsummary-"); ok {
				raw.Summaries[iface] = s
			}
		}
	}
	runOne := func(key string, argv []string) {
		defer wg.Done()
		out, err := run(key, argv)
		if err != nil {
			fail(key, err)
			return
		}
		set(key, out)
	}

	ifconfigDone := make(chan []byte, 1)
	for _, src := range sources {
		wg.Add(1)
		if src.key == "ifconfig" {
			go func() {
				defer wg.Done()
				out, err := run(src.key, src.argv)
				if err != nil {
					fail(src.key, err)
				} else {
					set(src.key, out)
				}
				ifconfigDone <- out
			}()
			continue
		}
		go runOne(src.key, src.argv)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w, err := wifi()
		if err != nil {
			fail("wifi", err)
			return
		}
		mu.Lock()
		raw.WiFi = w
		mu.Unlock()
	}()

	// One getsummary per interface that can hold a DHCP lease.
	for _, iface := range leaseCandidates(string(<-ifconfigDone)) {
		wg.Add(1)
		go runOne("getsummary-"+iface, []string{"ipconfig", "getsummary", iface})
	}
	wg.Wait()
	return raw
}

// leaseCandidates are interfaces with an IPv4 address that are not
// loopback, tunnels or bridges.
func leaseCandidates(ifconfig string) []string {
	var out []string
	name := ""
	for _, line := range strings.Split(ifconfig, "\n") {
		if line != "" && line[0] != '\t' && line[0] != ' ' {
			name, _, _ = strings.Cut(line, ":")
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "inet ") && name != "" {
			if !strings.HasPrefix(name, "lo") && !strings.HasPrefix(name, "utun") &&
				!strings.HasPrefix(name, "bridge") && !strings.HasPrefix(name, "ipsec") {
				out = append(out, name)
			}
			name = "" // once per interface
		}
	}
	return out
}

// Exec is the real Runner. A command that is not on PATH returns
// ErrNotInstalled. Tailscale's CLI also ships inside its macOS app.
func Exec(key string, argv []string) ([]byte, error) {
	path, err := exec.LookPath(argv[0])
	if err != nil && argv[0] == "tailscale" {
		path, err = exec.LookPath("/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	if err != nil {
		return nil, ErrNotInstalled
	}
	cmd := exec.Command(path, argv[1:]...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, errors.New(firstLine(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return line
}

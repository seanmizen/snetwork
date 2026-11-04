// Package collect reads this machine's network state into a netinfo.Raw.
//
// Every source is a local command, a local file or the local Tailscale
// daemon. Nothing here sends a packet on the network: package active does
// that, and only with --active.
package collect

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// Runner runs one source and returns its output. key names the source (and
// the fixture file in tests); argv is the command. argv[0] "@file" reads
// the file argv[1]; "@sshconfig" reads ~/.ssh/config with its includes.
type Runner func(key string, argv []string) ([]byte, error)

// WiFiReader reads the Wi-Fi interface. It returns nil when there is none.
type WiFiReader func() (*netinfo.WiFiRaw, error)

// ErrNotInstalled is what a Runner returns for a tool or file that is not
// on this machine. Build reports it as absent, not as a problem.
var ErrNotInstalled = errors.New("not installed")

// Source is one thing to read.
type Source struct {
	Key  string
	Argv []string
}

const socketfilterfw = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// common sources, on every OS.
var common = []Source{
	{"tailscale", []string{"tailscale", "status", "--json"}},
	{"known_hosts", []string{"@file", "~/.ssh/known_hosts"}},
	{"ssh_config", []string{"@sshconfig"}},
}

var bySystem = map[string][]Source{
	"darwin": {
		{"ifconfig", []string{"ifconfig"}},
		{"services", []string{"networksetup", "-listnetworkserviceorder"}},
		{"routes", []string{"netstat", "-rn", "-f", "inet"}},
		{"arp", []string{"arp", "-an"}},
		{"dns", []string{"scutil", "--dns"}},
		{"vpnlist", []string{"scutil", "--nc", "list"}},
		{"listening", []string{"netstat", "-anv", "-p", "tcp"}},
		{"fw-global", []string{socketfilterfw, "--getglobalstate"}},
		{"fw-stealth", []string{socketfilterfw, "--getstealthmode"}},
		{"shortcuts", []string{"shortcuts", "list"}},
	},
	"linux": {
		{"ip-addr", []string{"ip", "-j", "addr"}},
		{"ip-route", []string{"ip", "-j", "route", "show", "table", "all"}},
		{"ip-neigh", []string{"ip", "-j", "neigh"}},
		{"resolv", []string{"@file", "/etc/resolv.conf"}},
		{"resolved", []string{"@file", "/run/systemd/resolve/resolv.conf"}},
		{"ss", []string{"ss", "-Htlnp"}},
		{"iw-dev", []string{"iw", "dev"}},
		{"fw-systemctl", []string{"systemctl", "is-active", "ufw", "nftables"}},
	},
}

// WiFiNameShortcut is the Shortcut that snetwork runs, when it exists, to
// read the Wi-Fi name that macOS hides from the terminal.
const WiFiNameShortcut = "Wi-Fi name"

// followUps are sources that depend on another source's output.
var followUps = map[string]func(out string) []Source{
	// one getsummary per interface that can hold a DHCP lease
	"ifconfig": func(out string) []Source {
		var list []Source
		for _, ifc := range leaseCandidates(out) {
			list = append(list, Source{"getsummary-" + ifc, []string{"ipconfig", "getsummary", ifc}})
		}
		return list
	},
	"shortcuts": func(out string) []Source {
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) == WiFiNameShortcut {
				return []Source{{"wifi-name", []string{"shortcuts", "run", WiFiNameShortcut, "--output-path", "/dev/stdout"}}}
			}
		}
		return nil
	},
	"ip-addr": func(out string) []Source {
		var list []Source
		for _, ifc := range dynamicInterfaces(out) {
			list = append(list, Source{"dhcpcd-" + ifc, []string{"dhcpcd", "-U", ifc}})
		}
		return list
	},
	"iw-dev": func(out string) []Source {
		var list []Source
		for _, line := range strings.Split(out, "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "Interface" {
				list = append(list, Source{"iw-link-" + f[1], []string{"iw", "dev", f[1], "link"}})
			}
		}
		return list
	},
}

// Collect reads every source for this OS at once. A follow-up starts as
// soon as the source it depends on is done.
func Collect(goos string, run Runner, wifi WiFiReader) netinfo.Raw {
	raw := netinfo.Raw{
		OS:         goos,
		Out:        map[string][]byte{},
		Errs:       map[string]string{},
		NotPresent: map[string]bool{},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var start func(Source)
	start = func(src Source) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := run(src.Key, src.Argv)
			mu.Lock()
			switch {
			case errors.Is(err, ErrNotInstalled):
				raw.NotPresent[src.Key] = true
			case err != nil:
				raw.Errs[src.Key] = strings.TrimSpace(err.Error())
			default:
				raw.Out[src.Key] = out
			}
			mu.Unlock()
			if f, ok := followUps[src.Key]; ok && err == nil {
				for _, next := range f(string(out)) {
					start(next)
				}
			}
		}()
	}
	for _, src := range append(append([]Source(nil), common...), bySystem[goos]...) {
		start(src)
	}
	if goos == "darwin" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := wifi()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				raw.Errs["wifi"] = err.Error()
				return
			}
			raw.WiFi = w
		}()
	}
	wg.Wait()
	// A missing optional file or tool is not worth a line.
	for _, k := range []string{"known_hosts", "ssh_config", "resolved", "shortcuts", "wifi-name"} {
		delete(raw.Errs, k)
	}
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

var ifname = regexp.MustCompile(`"ifname":"([^"]+)"`)

// dynamicInterfaces are the interfaces in `ip -j addr` with a dynamic
// (DHCP) IPv4 address.
func dynamicInterfaces(out string) []string {
	var list []string
	for _, chunk := range strings.Split(out, `{"ifindex"`)[1:] {
		m := ifname.FindStringSubmatch(chunk)
		if m != nil && strings.Contains(chunk, `"family":"inet","local"`) && strings.Contains(chunk, `"dynamic":true`) {
			list = append(list, m[1])
		}
	}
	return list
}

// Exec is the real Runner. A command that is not on PATH, or a file that
// does not exist, returns ErrNotInstalled. A command that exits non-zero
// but prints output (`systemctl is-active` with one service inactive)
// still counts as read.
func Exec(key string, argv []string) ([]byte, error) {
	switch argv[0] {
	case "@file":
		return readFile(expandHome(argv[1]))
	case "@sshconfig":
		return readSSHConfig()
	}
	path, err := exec.LookPath(argv[0])
	if err != nil && argv[0] == "tailscale" {
		path, err = exec.LookPath("/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	if err != nil {
		return nil, ErrNotInstalled
	}
	out, err := exec.Command(path, argv[1:]...).Output()
	if err != nil {
		if len(out) > 0 {
			return out, nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, errors.New(firstLine(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInstalled
	}
	return b, err
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// readSSHConfig returns ~/.ssh/config with every Include expanded in
// place, one level deep: enough for the usual config.d layout.
func readSSHConfig() ([]byte, error) {
	main, err := readFile(expandHome("~/.ssh/config"))
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, line := range strings.Split(string(main), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.EqualFold(f[0], "include") {
			for _, pat := range f[1:] {
				if !filepath.IsAbs(pat) && !strings.HasPrefix(pat, "~") {
					pat = "~/.ssh/" + pat
				}
				matches, _ := filepath.Glob(expandHome(pat))
				for _, m := range matches {
					if inc, err := os.ReadFile(m); err == nil {
						b.Write(inc)
						b.WriteString("\n")
					}
				}
			}
			continue
		}
		b.WriteString(line + "\n")
	}
	return []byte(b.String()), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

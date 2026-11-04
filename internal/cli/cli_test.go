package cli

import (
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanmizen/snetwork/internal/active"
	"github.com/seanmizen/snetwork/internal/fixture"
	"github.com/seanmizen/snetwork/internal/render"
)

var update = flag.Bool("update", false, "rewrite testdata/golden files")

func TestMain(m *testing.M) {
	time.Local = time.UTC // lease times in golden files must not depend on the machine
	os.Exit(m.Run())
}

var now = time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)

// snetwork runs the CLI in-process against a fixture scenario, with only
// the given env vars set. The scenario "linux" runs as Linux.
func snetwork(t testing.TB, scenario string, env map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	dir := filepath.Join("..", "netinfo", "testdata", scenario)
	goos := "darwin"
	if scenario == "linux" {
		goos = "linux"
	}
	arp, _ := os.ReadFile(filepath.Join(dir, "arp.txt"))
	clock := now
	var out, errOut bytes.Buffer
	code = Main("test", Env{
		Args:      args,
		Stdout:    &out,
		Stderr:    &errOut,
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		GOOS:      goos,
		Run:       fixture.Runner(dir),
		WiFi:      fixture.WiFi(dir),
		Prober:    &active.Fake{Neighbours: arp},
		Now:       func() time.Time { return clock },
		Sleep:     func(_ context.Context, d time.Duration) { clock = clock.Add(d) },
		Terminal:  func(io.Writer) (bool, int) { return false, 0 },
		Frames:    3,
	})
	return out.String(), errOut.String(), code
}

// TestGolden compares full output against testdata/golden/<name>.txt.
// Regenerate with: go test ./internal/cli -update
func TestGolden(t *testing.T) {
	color := map[string]string{"FORCE_COLOR": "1"}
	type tc struct {
		name, scenario string
		env            map[string]string
		args           []string
	}
	cases := []tc{
		{"home", "home", nil, nil},
		{"home_color", "home", color, nil},
		{"home_no_color_env", "home", map[string]string{"FORCE_COLOR": "1", "NO_COLOR": "1"}, nil},
		{"home_all", "home", nil, []string{"all"}},
		{"home_interfaces_en0", "home", nil, []string{"interfaces", "en0"}},
		{"home_devices_subnet", "home", nil, []string{"devices", "10.20.30.0/24"}},
		{"home_vpn_peer", "home", nil, []string{"vpn", "bravo"}},
		{"home_json", "home", nil, []string{"-json"}},
		{"home_dhcp_json_after", "home", nil, []string{"dhcp", "--json"}},
		{"hotspot", "hotspot", nil, nil},
		{"hotspot_subnets", "hotspot", nil, []string{"subnets"}},
		{"hotspot_vpn", "hotspot", nil, []string{"vpn"}},
		{"bare", "bare", nil, nil},
		{"bare_dhcp", "bare", nil, []string{"dhcp"}},
		{"bare_wifi", "bare", nil, []string{"wifi"}},
		{"home_active", "home", nil, []string{"--active"}},
		{"home_active_color", "home", color, []string{"--active"}},
		{"home_devices_active", "home", nil, []string{"devices", "--active"}},
		{"home_devices_remote", "home", nil, []string{"devices", "10.40.50.0/24"}},
		{"home_devices_no_remote", "home", nil, []string{"devices", "-no-remote"}},
		{"home_internet_passive", "home", nil, []string{"internet"}},
		{"home_active_json", "home", nil, []string{"vpn", "--active", "-json"}},
		{"home_watch", "home", nil, []string{"vpn", "--watch=5s"}},
		{"home_via", "home", nil, []string{"--via", "charlie"}},
		{"linux", "linux", nil, nil},
		{"linux_all", "linux", nil, []string{"all"}},
		{"linux_active", "linux", nil, []string{"--active"}},
		{"bare_json", "bare", nil, []string{"all", "-json"}},
		{"help", "home", nil, []string{"-h"}},
		{"help_color", "home", color, []string{"-h"}},
		{"help_sections", "home", nil, []string{"-help", "sections"}},
		{"help_env", "home", nil, []string{"--h", "env"}},
	}
	for _, s := range render.Sections {
		cases = append(cases, tc{"home_" + s, "home", nil, []string{s}})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := snetwork(t, c.scenario, c.env, c.args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			golden := filepath.Join("testdata", "golden", c.name+".txt")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if out != string(want) {
				t.Errorf("output differs from %s\n--- got ---\n%s--- want ---\n%s", golden, out, want)
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"bogus"}, `no section "bogus"`},
		{[]string{"wifi", "en0"}, `wifi takes no item`},
		{[]string{"interfaces", "en99"}, `no interface "en99"`},
		{[]string{"dhcp", "utun7"}, `no DHCP lease on "utun7"`},
		{[]string{"devices", "10.9.9.0/24"}, `no subnet "10.9.9.0/24"`},
		{[]string{"vpn", "charlie"}, `no tailnet peer "charlie"`},
		{[]string{"vpn", "charlie", "-json"}, `no tailnet peer "charlie"`},
		{[]string{"all", "x"}, `all takes no item`},
		{[]string{"a", "b", "c"}, `too many arguments`},
		{[]string{"-bogus"}, `flag provided but not defined: -bogus`},
		{[]string{"-h", "nope"}, `no help topic "nope"`},
		// a flag-looking value after a section is still a flag
		{[]string{"vpn", "--watch", "5s"}, `"5s" is not a section. For an interval, join it with "=": --watch=5s`},
		{[]string{"--active", "30s"}, `--active=30s`},
		{[]string{"--watch=1x"}, `not an interval`},
		{[]string{"--watch=100ms"}, `the shortest interval is 500ms`},
		{[]string{"--watch", "-json"}, `-json and --watch do not mix`},
	}
	for _, c := range cases {
		out, errOut, code := snetwork(t, "home", nil, c.args...)
		if code != exitUsage || out != "" || !strings.Contains(errOut, c.want) || !strings.Contains(errOut, "snetwork -h") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want exit 2 and %q", c.args, code, out, errOut, c.want)
		}
	}
}

func TestRemoteErrors(t *testing.T) {
	for host, want := range map[string]string{
		"delta": "snetwork: delta has no snetwork\n",
		"echo":  "snetwork: ssh to echo failed: Permission denied (publickey).\n",
	} {
		out, errOut, code := snetwork(t, "home", nil, "--via", host)
		if code != exitError || out != "" || errOut != want {
			t.Errorf("--via %s: exit %d, stdout %q, stderr %q", host, code, out, errOut)
		}
	}
}

func TestVersion(t *testing.T) {
	out, _, code := snetwork(t, "home", nil, "-version")
	if code != 0 || out != "snetwork test\n" {
		t.Errorf("exit %d, %q", code, out)
	}
}

// TestPipedOutputHasNoColour: a dump to a file must be plain text.
func TestPipedOutputHasNoColour(t *testing.T) {
	out, _, _ := snetwork(t, "home", nil, "all")
	if strings.Contains(out, "\033[") {
		t.Error("escape codes in piped output")
	}
}

func TestHelpMentionsEveryFlag(t *testing.T) {
	var c config
	fs := newFlagSet(Env{LookupEnv: func(string) (string, bool) { return "", false }}, &c)
	fs.VisitAll(func(f *flag.Flag) {
		if !strings.Contains(helpMain, "-"+f.Name) {
			t.Errorf("help page does not mention -%s", f.Name)
		}
	})
}

func TestHelpMentionsEverySection(t *testing.T) {
	for _, s := range render.Sections {
		if !strings.Contains(helpSections, "{f}"+s+"{/}") {
			t.Errorf("sections help does not mention %s", s)
		}
	}
}

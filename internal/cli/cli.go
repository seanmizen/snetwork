// Package cli is snetwork's command line: flags, sections and output.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/seanmizen/snetwork/internal/active"
	"github.com/seanmizen/snetwork/internal/collect"
	"github.com/seanmizen/snetwork/internal/format"
	"github.com/seanmizen/snetwork/internal/netinfo"
	"github.com/seanmizen/snetwork/internal/render"
	"golang.org/x/term"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1 // couldn't write output, or the --via machine failed
	exitUsage = 2 // bad flag, section or item
)

// usageError is a mistake in how snetwork was invoked.
type usageError struct{ error }

func usagef(f string, a ...any) error { return usageError{fmt.Errorf(f, a...)} }

// Env is everything snetwork needs from the outside world, so tests can
// fake it.
type Env struct {
	Args      []string // without the program name
	Stdout    io.Writer
	Stderr    io.Writer
	LookupEnv func(string) (string, bool)
	GOOS      string
	Run       collect.Runner
	WiFi      collect.WiFiReader
	Prober    active.Prober
	Now       func() time.Time
	Sleep     func(context.Context, time.Duration)
	// Terminal reports whether w is a terminal, and its height in lines.
	Terminal func(w io.Writer) (isTerminal bool, height int)
	// Frames stops --watch after this many frames; 0 runs until Ctrl-C.
	Frames int
	Ctx    context.Context
}

// OSEnv is the real process environment.
func OSEnv(ctx context.Context) Env {
	return Env{
		Args: os.Args[1:], Stdout: os.Stdout, Stderr: os.Stderr, LookupEnv: os.LookupEnv,
		GOOS: runtime.GOOS, Run: collect.Exec, WiFi: collect.ReadWiFi,
		Prober: active.Real{GOOS: runtime.GOOS, Run: collect.Exec},
		Now:    time.Now,
		Sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
		},
		Terminal: func(w io.Writer) (bool, int) {
			f, ok := w.(*os.File)
			if !ok || !term.IsTerminal(int(f.Fd())) {
				return false, 0
			}
			_, h, err := term.GetSize(int(f.Fd()))
			if err != nil {
				return true, 0
			}
			return true, h
		},
		Ctx: ctx,
	}
}

// Main runs snetwork and returns the process exit code.
func Main(version string, env Env) int {
	if env.Ctx == nil {
		env.Ctx = context.Background()
	}
	err := run(version, env)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.As(err, new(usageError)):
		fmt.Fprintf(env.Stderr, "snetwork: %v\nRun 'snetwork -h' for usage.\n", err)
		return exitUsage
	default:
		fmt.Fprintf(env.Stderr, "snetwork: %v\n", err)
		return exitError
	}
}

type config struct {
	noColor   bool
	json      bool
	version   bool
	noRemote  bool
	via       string
	active    durFlag
	watch     durFlag
	helpTopic string // the word after -h, if any
	section   string // "" for the summary
	item      string
}

func newFlagSet(env Env, c *config) *flag.FlagSet {
	noColorEnv, _ := env.LookupEnv("NO_COLOR") // https://no-color.org
	c.active = durFlag{name: "active", def: 30 * time.Second, min: 5 * time.Second}
	c.watch = durFlag{name: "watch", def: 2 * time.Second, min: 500 * time.Millisecond}
	flags := flag.NewFlagSet("snetwork", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // we print our own help and errors
	flags.BoolVar(&c.noColor, "no-color", noColorEnv != "", "")
	flags.BoolVar(&c.json, "json", false, "")
	flags.BoolVar(&c.version, "version", false, "")
	flags.BoolVar(&c.version, "v", false, "")
	flags.BoolVar(&c.noRemote, "no-remote", false, "")
	flags.StringVar(&c.via, "via", "", "")
	flags.Var(&c.active, "active", "")
	flags.Var(&c.watch, "watch", "")
	return flags
}

// parseArgs takes flags before and after the section, so both
// `snetwork -json vpn` and `snetwork vpn -json` work.
func parseArgs(env Env) (config, error) {
	var c config
	flags := newFlagSet(env, &c)
	var words []string
	args := env.Args
	for {
		if err := flags.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				c.helpTopic = flags.Arg(0)
				return c, err
			}
			return c, usageError{err}
		}
		if flags.NArg() == 0 {
			break
		}
		words = append(words, flags.Arg(0))
		args = flags.Args()[1:]
	}
	// `--watch 5s` reads 5s as a section. Say what was meant.
	for _, w := range words {
		if _, err := time.ParseDuration(w); err == nil {
			name := "watch"
			if c.active.on && !c.active.set && !(c.watch.on && !c.watch.set) {
				name = "active"
			}
			return c, usagef("%q is not a section. For an interval, join it with \"=\": --%s=%s", w, name, w)
		}
	}
	switch len(words) {
	case 0:
	case 1:
		c.section = words[0]
	case 2:
		c.section, c.item = words[0], words[1]
	default:
		return c, usagef("too many arguments: %s", strings.Join(words, " "))
	}
	if c.section != "" && c.section != "all" && !isSection(c.section) {
		return c, usagef("no section %q (sections: %s, all)", c.section, strings.Join(render.Sections, ", "))
	}
	if c.section == "all" && c.item != "" {
		return c, usagef("all takes no item (got %q)", c.item)
	}
	if c.json && c.watch.on {
		return c, usagef("-json and --watch do not mix: --watch redraws a screen")
	}
	return c, nil
}

func isSection(s string) bool {
	for _, n := range render.Sections {
		if n == s {
			return true
		}
	}
	return false
}

func run(version string, env Env) error {
	c, err := parseArgs(env)
	if errors.Is(err, flag.ErrHelp) {
		page, ok := helpPages[c.helpTopic]
		if !ok {
			return usagef("no help topic %q (try sections or env)", c.helpTopic)
		}
		color := format.ColorEnabled(env.Stdout, c.noColor, env.LookupEnv)
		fmt.Fprint(env.Stdout, renderHelp(page, version, color))
		return err
	}
	if err != nil {
		return err
	}
	if c.version {
		fmt.Fprintf(env.Stdout, "snetwork %s\n", version)
		return nil
	}
	st := render.Style{Color: format.ColorEnabled(env.Stdout, c.noColor, env.LookupEnv)}
	if c.watch.on {
		return watch(env, c, st)
	}
	l, err := start(env, c)
	if err != nil {
		return err
	}
	return present(env, c, st, l)
}

// start gathers a snapshot and starts the slow work behind it: --active
// probes and questions to remote routers. The live view tracks both.
func start(env Env, c config) (*live, error) {
	var snap netinfo.Snapshot
	var raw netinfo.Raw
	if c.via != "" {
		s, err := viaSnapshot(env, c)
		if err != nil {
			return nil, err
		}
		snap, raw = s, netinfo.Raw{OS: s.OS, Out: map[string][]byte{}}
	} else {
		raw = collect.Collect(env.GOOS, env.Run, env.WiFi)
		snap = netinfo.Build(raw, env.Now())
	}
	l := newLive(snap, raw)
	if c.via == "" && c.active.on {
		l.update(func(s *netinfo.Snapshot, _ *netinfo.Raw) { active.Plan(s) })
		l.job(func() {
			l.mu.Lock()
			s := l.snap
			l.mu.Unlock()
			active.Run(env.Ctx, s, env.Prober, l.update)
		})
	}
	if c.via == "" && !c.noRemote {
		askRouters(env, c, l)
	}
	return l, nil
}

// present writes the result: JSON once everything is done; otherwise a
// live view that redraws in place, or blocks streamed in order.
func present(env Env, c config, st render.Style, l *live) error {
	if c.json {
		l.wait()
		l.mu.Lock()
		defer l.mu.Unlock()
		v, err := jsonView(l.snap, c.section, c.item)
		if err != nil {
			return usageError{err}
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	view := c.section
	if _, err := render.Blocks(view, c.item, l.snapshot(), render.Style{}); err != nil {
		return usageError{err}
	}
	tty, height := env.Terminal(env.Stdout)
	if tty && l.pending() {
		first := l.frame(view, c.item, st)
		if lines := strings.Count(first, "\n"); height == 0 || lines < height {
			return redraw(env, l, view, c.item, st, first)
		}
	}
	return stream(env, l, view, c.item, st)
}

// jsonView is the data behind a view: the whole snapshot for the summary
// and `all`, one section's data otherwise.
func jsonView(s netinfo.Snapshot, section, item string) (any, error) {
	if item != "" {
		// the text renderer checks the item, so text and JSON agree
		if _, err := render.Section(section, item, s, render.Style{}); err != nil {
			return nil, err
		}
	}
	switch section {
	case "", "all":
		return s, nil
	case "interfaces":
		for _, i := range s.Interfaces {
			if item != "" && i.Name == item {
				return i, nil
			}
		}
		return s.Interfaces, nil
	case "subnets":
		return map[string]any{"subnets": s.Subnets, "default_routes": s.Defaults}, nil
	case "devices":
		for _, g := range s.Devices {
			if item != "" && g.Prefix == item {
				return g, nil
			}
		}
		return s.Devices, nil
	case "dhcp":
		for _, l := range s.DHCP {
			if item != "" && l.Interface == item {
				return l, nil
			}
		}
		return s.DHCP, nil
	case "dns":
		return s.DNS, nil
	case "wifi":
		return s.WiFi, nil
	case "vpn":
		for _, p := range s.VPN.Tailscale.Peers {
			if item != "" && p.Name == item {
				return p, nil
			}
		}
		return s.VPN, nil
	case "internet":
		if s.Active == nil {
			return nil, nil
		}
		return s.Active.Internet, nil
	case "firewall":
		return s.Firewall, nil
	case "listening":
		return s.Listening, nil
	}
	return nil, fmt.Errorf("no section %q", section)
}

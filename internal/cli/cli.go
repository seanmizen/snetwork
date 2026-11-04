// Package cli is snetwork's command line: flags, sections and output.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/seanmizen/snetwork/internal/collect"
	"github.com/seanmizen/snetwork/internal/format"
	"github.com/seanmizen/snetwork/internal/netinfo"
	"github.com/seanmizen/snetwork/internal/render"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1 // couldn't write output
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
	Run       collect.Runner
	WiFi      collect.WiFiReader
	Now       func() time.Time
}

// OSEnv is the real process environment.
func OSEnv() Env {
	return Env{
		Args: os.Args[1:], Stdout: os.Stdout, Stderr: os.Stderr, LookupEnv: os.LookupEnv,
		Run: collect.Exec, WiFi: collect.ReadWiFi, Now: time.Now,
	}
}

// Main runs snetwork and returns the process exit code.
func Main(version string, env Env) int {
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
	helpTopic string // the word after -h, if any
	section   string // "" for the summary
	item      string
}

func newFlagSet(env Env, c *config) *flag.FlagSet {
	noColorEnv, _ := env.LookupEnv("NO_COLOR") // https://no-color.org
	flags := flag.NewFlagSet("snetwork", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // we print our own help and errors
	flags.BoolVar(&c.noColor, "no-color", noColorEnv != "", "")
	flags.BoolVar(&c.json, "json", false, "")
	flags.BoolVar(&c.version, "version", false, "")
	flags.BoolVar(&c.version, "v", false, "")
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

	snap := netinfo.Build(collect.Collect(env.Run, env.WiFi), env.Now())

	if c.json {
		v, err := jsonView(snap, c.section, c.item)
		if err != nil {
			return usageError{err}
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}

	st := render.Style{Color: format.ColorEnabled(env.Stdout, c.noColor, env.LookupEnv)}
	var out string
	switch c.section {
	case "":
		out = render.Summary(snap, st)
	case "all":
		out = render.All(snap, st)
	default:
		out, err = render.Section(c.section, c.item, snap, st)
		if err != nil {
			return usageError{err}
		}
	}
	if _, err := io.WriteString(env.Stdout, out); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
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
	case "firewall":
		return s.Firewall, nil
	case "listening":
		return s.Listening, nil
	}
	return nil, fmt.Errorf("no section %q", section)
}

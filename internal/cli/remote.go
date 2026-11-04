package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// sshArgv runs snetwork on another machine. BatchMode: never prompt for a
// password; a missing key fails at once. -no-remote stops that machine
// from asking further machines in turn.
func sshArgv(host string, args ...string) []string {
	argv := []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, "snetwork"}
	return append(append(argv, args...), "-json", "-no-remote")
}

// remote runs snetwork on host and returns its JSON output. There is no
// fallback to raw commands: a machine without snetwork says so.
func remote(env Env, host string, args ...string) ([]byte, error) {
	out, err := env.Run("ssh-"+host, sshArgv(host, args...))
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") || strings.Contains(msg, "No such file") {
			return nil, fmt.Errorf("%s has no snetwork", host)
		}
		return nil, fmt.Errorf("ssh to %s failed: %s", host, msg)
	}
	return out, nil
}

// viaSnapshot is --via: the whole snapshot comes from another machine, and
// this one only draws it.
func viaSnapshot(env Env, c config) (netinfo.Snapshot, error) {
	args := []string{"all"}
	if c.active.on {
		args = append(args, "--active")
	}
	out, err := remote(env, c.via, args...)
	if err != nil {
		return netinfo.Snapshot{}, err
	}
	var s netinfo.Snapshot
	if err := json.Unmarshal(out, &s); err != nil {
		return netinfo.Snapshot{}, fmt.Errorf("%s sent output that is not snetwork JSON: %v", c.via, err)
	}
	return s, nil
}

// askRouters asks the tailnet peer that routes each remote subnet for its
// devices, over ssh. Only a full view asks: `snetwork devices`, `snetwork
// devices <subnet>` and `snetwork all`. Plain `snetwork` never connects.
func askRouters(env Env, c config, l *live) {
	if c.section != "devices" && c.section != "all" {
		return
	}
	l.mu.Lock()
	var todo []netinfo.DeviceGroup
	for i := range l.snap.Devices {
		g := &l.snap.Devices[i]
		if g.Count != nil || g.Router == "" || (c.item != "" && g.Prefix != c.item) {
			continue
		}
		g.Pending, g.AskedVia = true, g.Router
		todo = append(todo, *g)
	}
	l.mu.Unlock()
	for _, g := range todo {
		l.job(func() {
			args := []string{"devices", g.Prefix}
			if c.active.on {
				args = append(args, "--active")
			}
			out, err := remote(env, g.Router, args...)
			var got netinfo.DeviceGroup
			if err == nil {
				if jerr := json.Unmarshal(out, &got); jerr != nil {
					err = fmt.Errorf("%s sent output that is not snetwork JSON", g.Router)
				}
			}
			l.update(func(s *netinfo.Snapshot, _ *netinfo.Raw) {
				for i := range s.Devices {
					d := &s.Devices[i]
					if d.Prefix != g.Prefix {
						continue
					}
					d.Pending = false
					if err != nil {
						d.Note = err.Error()
						continue
					}
					d.Count, d.Devices, d.Swept = got.Count, got.Devices, got.Swept
					if d.Count == nil {
						d.Note = g.Router + " does not sit on " + g.Prefix
					}
				}
			})
		})
	}
}

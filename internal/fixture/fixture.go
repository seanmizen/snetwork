// Package fixture serves a folder of captured command output in place of
// this machine, so tests do not depend on the network they run on.
//
// A folder holds one file per source, named by the source's key
// (ifconfig.txt, tailscale.json, getsummary-en0.txt ...) plus wifi.json.
// A missing file means the tool is not installed.
package fixture

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/seanmizen/snetwork/internal/collect"
	"github.com/seanmizen/snetwork/internal/netinfo"
)

// Runner reads <dir>/<key>.txt, or <key>.json. <key>.err holds the error
// message of a source that fails.
func Runner(dir string) collect.Runner {
	return func(key string, _ []string) ([]byte, error) {
		if b, err := os.ReadFile(filepath.Join(dir, key+".err")); err == nil {
			return nil, errors.New(strings.TrimSpace(string(b)))
		}
		for _, ext := range []string{".txt", ".json"} {
			b, err := os.ReadFile(filepath.Join(dir, key+ext))
			if err == nil {
				return b, nil
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
		}
		return nil, collect.ErrNotInstalled
	}
}

// WiFi reads <dir>/wifi.json. No file means no Wi-Fi interface.
func WiFi(dir string) collect.WiFiReader {
	return func() (*netinfo.WiFiRaw, error) {
		b, err := os.ReadFile(filepath.Join(dir, "wifi.json"))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var w netinfo.WiFiRaw
		if err := json.Unmarshal(b, &w); err != nil {
			return nil, err
		}
		return &w, nil
	}
}

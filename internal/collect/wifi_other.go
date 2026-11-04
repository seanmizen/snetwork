//go:build !(darwin && cgo)

package collect

import (
	"errors"

	"github.com/seanmizen/snetwork/internal/netinfo"
)

// ReadWiFi needs CoreWLAN, so it works only in a macOS build with cgo.
func ReadWiFi() (*netinfo.WiFiRaw, error) {
	return nil, errors.New("this build cannot read Wi-Fi (needs macOS and cgo)")
}

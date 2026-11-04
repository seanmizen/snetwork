// Command snetwork shows where this machine sits on the network: interfaces,
// subnets, devices, DHCP, DNS, Wi-Fi, VPN, firewall and listening ports.
package main

import (
	"os"

	"github.com/seanmizen/snetwork/internal/cli"
)

// version is stamped at build time via -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	os.Exit(cli.Main(version, cli.OSEnv()))
}

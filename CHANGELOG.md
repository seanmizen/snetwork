# Changelog

All notable changes to snetwork. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and snetwork uses
[semantic versioning](https://semver.org/).

## [0.1.1] - 2026-11-04

### Changed

- **A failed ssh names its user.** When snetwork cannot ask a router, the
  message says which user ssh logged in as. For an authentication failure,
  it also says to set the user in `~/.ssh/config`. The usual cause is the
  wrong user: ssh then offers every agent key, and the server stops it with
  "Too many authentication failures".

## [0.1.0] - 2026-11-04

First release.

### Added

- **Passive summary.** `snetwork` reads this machine's own state and sends
  no packets. A headline says where you are (the LAN a tailnet peer routes,
  an iPhone hotspot, or another network), then ten sections: interfaces,
  subnets, devices, dhcp, dns, wifi, vpn, internet, firewall and listening.
- **Sections in full.** `snetwork SECTION [ITEM]` and `snetwork all`, with
  `-json` for every view.
- **`--active[=30s]`.** Sweeps this machine's own subnets (a /24 at most,
  never across a VPN), pings the gateway and tailnet peers, times each DNS
  resolver, scans Wi-Fi, and finds the public addresses and carrier-grade
  NAT. It changes nothing. Pending values show as a purple `…` and fill in
  place; output that is taller than the screen, or not a terminal, prints
  one section at a time.
- **`--watch[=2s]`.** Redraws and marks changes against the first complete
  frame: green, yellow after 10 s, red for a line that is gone. Without
  colour: `+`, `~` and `-`.
- **Remote subnets.** A full view asks the tailnet peer that routes a
  subnet, over ssh, to run snetwork there. `--via HOST` shows another
  machine's view. `-no-remote` turns asking off. Plain `snetwork` never
  connects anywhere.
- **Names and makers.** From ssh config, Tailscale and known_hosts; with
  `--active`, from the router's DNS and mDNS. Makers from the IEEE
  registry, built in. Randomised addresses show as "private address".
- **macOS and Linux.** Wi-Fi through CoreWLAN on macOS (a few
  milliseconds). The Wi-Fi name, which macOS hides from the terminal,
  comes from a Shortcut called "Wi-Fi name" when it exists. Linux reads
  iproute2, ss, iw, dhcpcd, resolv.conf and systemctl.

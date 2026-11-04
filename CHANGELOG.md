# Changelog

All notable changes to snetwork. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and snetwork uses
[semantic versioning](https://semver.org/).

## [Unreleased]

### Added

- **Passive summary.** `snetwork` prints a headline (LAN, iPhone hotspot or
  other network; tailnet state) and nine sections: interfaces, subnets,
  devices, dhcp, dns, wifi, vpn, firewall and listening.
- **Sections in full.** `snetwork SECTION [ITEM]` and `snetwork all`.
- **JSON.** `-json` for the summary, `all` and every section.
- **Wi-Fi through CoreWLAN**, so the summary runs in well under a second.

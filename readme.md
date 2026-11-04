# snetwork - Sean's Network Tool

Where am I on the network, at a glance. `snetwork` prints this machine's
interfaces, subnets, devices, DHCP lease, DNS, Wi-Fi, VPN, firewall and
listening ports. Plain `snetwork` is passive: it sends no packets.
`--active` probes, and `--watch` redraws and marks what changed.

```console
$ snetwork --active
snetwork · LAN of alpha · 10.20.30.102 on Wi-Fi · tailnet up

interfaces                                        snetwork interfaces
  en0    Wi-Fi                10.20.30.102/24  up              ← default route
  utun7  Tailscale            100.64.0.10      up
  en8    USB 10/100/1000 LAN  –                not plugged in
  + 9 more

subnets                                           snetwork subnets
  10.20.30.0/24  direct   en0                gateway 3.2 ms  alpha routes it when you are away
  10.40.50.0/24  via vpn  tailscale → bravo  bravo 40 ms
  100.64.0.0/10  via vpn  tailscale                          tailnet · 2 of 3 peers online

devices                                           snetwork devices
  10.20.30.0/24  10 found  swept 254 addresses
  10.40.50.0/24  –         behind bravo · snetwork devices 10.40.50.0/24 asks it

internet                                          snetwork internet
  public ipv4  192.0.2.1
  public ipv6  none: no IPv6 route out
  cgnat        yes (hop 2: 100.64.1.1): no inbound IPv4
  latency      21 ms to 1.1.1.1
…
```

The addresses above are examples.

- [Install](#install)
- [Usage](#usage)
- [Passive, active, remote](#passive-active-remote)
- [Names and makers](#names-and-makers)
- [Where the data comes from](#where-the-data-comes-from)
- [Development](#development)

## Install

### Homebrew

```bash
brew tap seanmizen/tap
brew install snetwork
```

### From source

On macOS, with Go and the Xcode command line tools (the Wi-Fi reader uses
cgo):

```bash
make build      # bin/snetwork-<os>-<arch>
make install    # copies it to /usr/local/bin/snetwork
```

## Usage

```
snetwork                       the summary: every section, a few lines each
snetwork SECTION [ITEM]        one section in full
snetwork all                   every section in full
```

| Section | Item | Shows |
|---|---|---|
| `interfaces` | name | every interface, the default route first |
| `subnets` | | networks you sit on, those a VPN reaches, default routes |
| `devices` | subnet | devices per subnet, with names and makers |
| `dhcp` | name | lease, server, router and DNS, time left |
| `dns` | | resolvers in the order the system asks them |
| `wifi` | | band, channel, width, signal, rate |
| `vpn` | peer | Tailscale peers and the subnets they route; other VPNs |
| `internet` | | `--active` only: public addresses, carrier-grade NAT, latency |
| `firewall` | | the macOS application firewall and pf; ufw and nftables |
| `listening` | | TCP ports that accept connections from other machines |

| Flag | Effect |
|---|---|
| `--active[=30s]` | Also probe (see below). The interval is for `--watch`. |
| `--watch[=2s]` | Redraw every interval. Changes against the first complete frame show green, then yellow after 10 s; a line that is gone shows red, then dim. Without colour: `+`, `~`, `-`. |
| `--via HOST` | Run snetwork on HOST over ssh and show its answer here. |
| `-no-remote` | Do not ask tailnet routers about the subnets behind them. |
| `-json` | The same data as JSON. |
| `-no-color` | No colour. Colour is also off when output is not a terminal, so `snetwork all > file.txt` is plain text. |
| `-v`, `-version` | Print the version. |
| `-h [TOPIC]` | Help. Topics: `sections`, `env`. |

An interval needs the `=`: `--watch=5s`. `--watch 5s` reads `5s` as a
section, and snetwork says so. Flags go before or after the section, and
`-json` and `--json` both work. `NO_COLOR` and `FORCE_COLOR` work as usual.

## Passive, active, remote

**Passive** (the default) reads local commands, files and the local
Tailscale daemon. It sends nothing on the network and takes well under a
second.

**`--active`** sends probes and changes nothing: no lease renewal, no
reconnect, no tailnet change.

- **Sweep:** one small UDP datagram to each address of each subnet this
  machine sits on, so the ARP cache fills. A /24 at most, and never through
  a VPN: ARP does not cross a router, so a sweep through the tailnet would
  be slow and would miss every device that ignores ping.
- **Pings:** the gateway, and `tailscale ping` to each online peer.
- **DNS:** one lookup through each resolver, timed.
- **Wi-Fi:** a scan of the networks in range (macOS; on Linux a scan needs
  root).
- **Internet:** Cloudflare's trace page over IPv4 and IPv6, a TCP connect
  to 1.1.1.1 for latency, and four traceroute hops to find carrier-grade
  NAT.

Results that are not in yet show as a purple `…` and fill in place. When
the view is taller than the screen, or output is not a terminal, each
section prints once it is complete.

**Remote subnets.** A subnet behind the tailnet is invisible from here, so
a full view (`snetwork devices`, `snetwork devices <subnet>`, `snetwork
all`) asks the tailnet peer that routes it: `ssh <peer> snetwork devices
<subnet> -json`. That peer needs snetwork too; if it does not have it,
snetwork says so. Plain `snetwork` never connects anywhere. ssh uses your
own config, so a peer that needs another user wants a `Host` entry:

```
Host bravo
  User srv
```

## Names and makers

A device's name comes from the first of these that knows it:

1. your ssh config: a `Host` whose `HostName` is the address,
2. Tailscale: a peer's direct address,
3. known_hosts: entries that share a host key are one machine, so an
   address and a name stored under one key match (hashed entries cannot be
   read),
4. with `--active`: the router's DNS (many routers answer with the name a
   device sent in its DHCP request),
5. with `--active`: mDNS.

One machine with two addresses on one subnet (wired and Wi-Fi) shows once.
The full view says where each name came from.

The maker comes from the IEEE registry of address blocks, built into the
binary. A randomised address, as phones use, shows as "private address".

macOS hides the Wi-Fi network name from the terminal. To show it, make a
Shortcut called **Wi-Fi name**: one action, Get Network Details → Network
Name. snetwork runs it when it exists.

## Where the data comes from

| | macOS | Linux |
|---|---|---|
| interfaces | `ifconfig`, `networksetup -listnetworkserviceorder` | `ip -j addr` |
| routes | `netstat -rn` | `ip -j route show table all` |
| devices | `arp -an` | `ip -j neigh` |
| dhcp | `ipconfig getsummary` | `ip -j addr` (time left), `dhcpcd -U` |
| dns | `scutil --dns` | `/etc/resolv.conf`, systemd-resolved's upstream file |
| wifi | CoreWLAN | `iw dev`, `iw dev <if> link` |
| vpn | `tailscale status --json`, `scutil --nc list` | `tailscale status --json` |
| firewall | `socketfilterfw` | `systemctl is-active ufw nftables` |
| listening | `netstat -anv -p tcp` | `ss -Htlnp` |

## Development

```bash
make test       # go vet, go test
make fuzz       # fuzz the parsers and the colour specs
go test ./internal/cli -update   # rewrite the golden files
cd internal/oui && go generate   # download the IEEE registry again
```

Every parser is a pure function of a command's text output. The tests run
against captured output in `internal/netinfo/testdata/<scenario>/`
(home, hotspot, bare, linux), so they do not depend on the machine or
network they run on. The scenarios hold made-up data only. `--active`
tests use `active.Fake`, which sends nothing.

To release: tag `vX.Y.Z` and push the tag. The release workflow builds the
archives on macOS (the darwin builds need cgo) and publishes them. Then
`packaging/update-formula.sh <tap-dir> vX.Y.Z` writes the Homebrew formula.

snetwork started as a copy of [shist](https://github.com/seanmizen/shist):
the CLI skeleton, the coloured help and the colour specs come from there.

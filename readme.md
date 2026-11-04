# snetwork - Sean's Network Tool

Where am I on the network, at a glance. `snetwork` reads this Mac's own
state and prints its interfaces, subnets, devices, DHCP lease, DNS, Wi-Fi,
VPN, firewall and listening ports. It is passive: it sends no packets.

```console
$ snetwork
snetwork · LAN of alpha · 10.20.30.102 on Wi-Fi · tailnet up

interfaces                                        snetwork interfaces
  en0    Wi-Fi                10.20.30.102/24  up              ← default route
  utun7  Tailscale            100.64.0.10      up
  en8    USB 10/100/1000 LAN  –                not plugged in
  + 10 more

subnets                                           snetwork subnets
  10.20.30.0/24  direct   en0                alpha routes it when you are away
  10.40.50.0/24  via vpn  tailscale → bravo
  100.64.0.0/10  via vpn  tailscale          tailnet · 2 of 3 peers online

devices                                           snetwork devices
  10.20.30.0/24  11 seen recently  ARP cache, no scan
  10.40.50.0/24  –                 behind bravo: not visible from here

dhcp                                              snetwork dhcp
  en0   from 10.20.30.1 · 24h lease · 21h 20m left
        router + dns 10.20.30.1 (same box)
…
```

The addresses above are examples.

## Usage

```
snetwork                       the summary: every section, a few lines each
snetwork SECTION [ITEM]        one section in full
snetwork all                   every section in full
```

Sections: `interfaces [NAME]`, `subnets`, `devices [SUBNET]`, `dhcp [NAME]`,
`dns`, `wifi`, `vpn [PEER]`, `firewall`, `listening`. `snetwork -h sections`
describes each one.

| Flag | Effect |
|---|---|
| `-json` | The same data as JSON: the whole snapshot, or one section |
| `-no-color` | No colour. Colour is also off when output is not a terminal, so `snetwork all > file.txt` is plain text. |
| `-v`, `-version` | Print the version |
| `-h [TOPIC]` | Help. Topics: `sections`, `env` |

Flags go before or after the section, and `-json` and `--json` both work.
`NO_COLOR` and `FORCE_COLOR` work as in shist.

## Where the data comes from

All local, nothing on the network:

| Section | Source |
|---|---|
| interfaces | `ifconfig`, `networksetup -listnetworkserviceorder` |
| subnets | interface addresses, `netstat -rn`, Tailscale's routes |
| devices | `arp -an`: what this Mac has talked to lately. It is not a scan. |
| dhcp | `ipconfig getsummary` |
| dns | `scutil --dns` |
| wifi | CoreWLAN, in a few milliseconds. `system_profiler` takes about 3 s. |
| vpn | `tailscale status --json`, `scutil --nc list` |
| firewall | `socketfilterfw`. pf needs root, so snetwork does not read it. |
| listening | `netstat -anv -p tcp` |

macOS hides the Wi-Fi network name unless your terminal has Location
Services permission. snetwork says so; every other Wi-Fi field shows without
it.

The headline names a LAN by the Tailscale peer that routes it ("LAN of
alpha"), so no configuration file is needed.

## Install

Build from source on a Mac (Go 1.27 and the Xcode command line tools):

```bash
make build      # bin/snetwork-darwin-arm64
make install    # copies it to /usr/local/bin/snetwork
```

## Development

```bash
make test       # go vet, go test
make fuzz       # fuzz the parsers and the colour specs
go test ./internal/cli -update   # rewrite the golden files
```

Every parser is a pure function of a command's text output. The tests run
against captured output in `internal/netinfo/testdata/<scenario>/`, so they
do not depend on the machine or network they run on. The scenarios hold
made-up addresses only.

snetwork started as a copy of [shist](https://github.com/seanmizen/shist): the
CLI skeleton, the coloured help and the colour specs come from there.

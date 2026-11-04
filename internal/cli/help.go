package cli

import (
	"regexp"
	"strings"

	"github.com/seanmizen/snetwork/internal/format"
)

// Help pages, by the topic after -h. {version} is the version, and {spec}
// tags are colours: a colour spec (e.g. {dim}) or a name from helpStyles;
// {/} resets. Every flag must appear on the main page
// (TestHelpMentionsEveryFlag), and every section on the sections page
// (TestHelpMentionsEverySection).
var helpPages = map[string]string{"": helpMain, "sections": helpSections, "env": helpEnv}

var helpStyles = map[string]string{"h": "bold yellow", "f": "green", "a": "cyan", "/": "reset"}

const helpMain = `{bold}snetwork {version}{/} {dim}·{/} where am I on the network, at a glance

{h}USAGE{/}
  snetwork                       the summary: every section, a few lines each
  snetwork {a}SECTION{/} {a}[ITEM]{/}       one section in full {dim}· see{/} snetwork -h sections
  snetwork all                   every section in full

{h}PROBES{/}  {dim}plain snetwork sends no packets{/}
  {f}--active{/}{a}[=30s]{/}            also probe: sweep this LAN (a /24 at most), ping the
                         gateway and tailnet peers, time DNS, scan Wi-Fi, ask the
                         internet. Changes nothing. The interval is for --watch.
  {f}--watch{/}{a}[=2s]{/}              redraw every interval and mark changes: green new,
                         yellow older, red gone. The interval needs the "=".

{h}OTHER MACHINES{/}  {dim}over ssh; they need snetwork too{/}
  {f}--via{/} {a}HOST{/}             run on HOST and show its answer here
  {f}-no-remote{/}             do not ask tailnet routers about the subnets behind them
                         {dim}(full views ask; plain snetwork never does){/}

{h}OUTPUT{/}
  {f}-json{/}                  the same data as JSON
  {f}-no-color{/}              no colour {dim}(also off when output is not a terminal){/}

{h}OTHER{/}
  {f}-v, -version{/}           print the version
  {f}-h, -help{/} {a}[TOPIC]{/}      this help {dim}· topics:{/} sections{dim},{/} env

{h}EXAMPLES{/}
  snetwork --active              {dim}# everything, probed; pending values show as …{/}
  snetwork vpn --watch           {dim}# tailnet peers, redrawn every 2 s{/}
  snetwork devices 10.0.1.0/24   {dim}# asks the tailnet peer that routes that subnet{/}
  snetwork all > network.txt     {dim}# everything, no colour{/}

{dim}Flags go before or after the section, and -json and --json both work.{/}
`

const helpSections = `{h}SECTIONS{/}  {dim}snetwork SECTION [ITEM]{/}
  {f}interfaces{/} {a}[NAME]{/}   every interface, the default route first
  {f}subnets{/}             networks you sit on, and those a VPN reaches; default routes
  {f}devices{/} {a}[SUBNET]{/}    devices per subnet, with names and makers; a subnet behind
                      the tailnet is asked of the peer that routes it
  {f}dhcp{/} {a}[NAME]{/}         lease, server, router and DNS, and time left
  {f}dns{/}                 resolvers in the order the system asks them
  {f}wifi{/}                band, channel, width, signal, rate; --active: networks nearby
  {f}vpn{/} {a}[PEER]{/}          Tailscale peers and routes; other VPNs
  {f}internet{/}            --active only: public addresses, carrier-grade NAT, latency
  {f}firewall{/}            the macOS application firewall and pf; ufw and nftables
  {f}listening{/}           TCP ports that accept connections from other machines
  {f}all{/}                 every section in full

{h}NAMES{/}  {dim}strongest first{/}
  your ssh config {dim}(Host with a HostName address){/}, Tailscale, known_hosts,
  and with --active the router's DNS, then mDNS. One machine with two
  addresses on one subnet shows once.

{h}NOTES{/}
  {dim}devices{/}   without --active: what this machine talked to lately, not a scan.
  {dim}wifi{/}      macOS hides the network name from the terminal. A Shortcut
            called "Wi-Fi name" (Get Network Details → Network Name) shows it.
  {dim}firewall{/}  rules need root, so snetwork does not read them.
`

const helpEnv = `{h}ENVIRONMENT{/}
  {a}NO_COLOR{/}       any value disables colour {dim}(no-color.org){/}
  {a}FORCE_COLOR{/}    any value but 0 forces colour, even when piped

{h}EXIT STATUS{/}
  {f}0{/} ok  {dim}·{/}  {f}1{/} couldn't write output, or --via failed  {dim}·{/}  {f}2{/} bad usage
`

var helpTag = regexp.MustCompile(`\{[a-z0-9#/ ]+\}`)

// renderHelp fills in the version and turns {spec} tags into colour, or
// drops them when color is false.
func renderHelp(page, version string, color bool) string {
	page = strings.ReplaceAll(page, "{version}", version)
	return helpTag.ReplaceAllStringFunc(page, func(tag string) string {
		if !color {
			return ""
		}
		spec := tag[1 : len(tag)-1]
		if s, ok := helpStyles[spec]; ok {
			spec = s
		}
		return format.SGR(spec)
	})
}

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

{h}OUTPUT{/}
  {f}-json{/}                  the same data as JSON
  {f}-no-color{/}              no colour {dim}(also off when output is not a terminal){/}

{h}OTHER{/}
  {f}-v, -version{/}           print the version
  {f}-h, -help{/} {a}[TOPIC]{/}      this help {dim}· topics:{/} sections{dim},{/} env

{h}EXAMPLES{/}
  snetwork vpn                   {dim}# tailnet peers, and which subnets they route{/}
  snetwork interfaces en0
  snetwork all > network.txt     {dim}# everything, no colour{/}
  snetwork dhcp -json | jq .

{dim}Passive: snetwork reads this machine's own state and sends no packets.
Flags go before or after the section, and -json and --json both work.{/}
`

const helpSections = `{h}SECTIONS{/}  {dim}snetwork SECTION [ITEM]{/}
  {f}interfaces{/} {a}[NAME]{/}   every interface, the default route first
  {f}subnets{/}             networks you sit on, and those a VPN reaches; default routes
  {f}devices{/} {a}[SUBNET]{/}    devices in the ARP cache, per subnet you sit on
  {f}dhcp{/} {a}[NAME]{/}         lease, server, router and DNS, and time left
  {f}dns{/}                 resolvers in the order macOS asks them
  {f}wifi{/}                band, channel, width, signal, noise, rate
  {f}vpn{/} {a}[PEER]{/}          Tailscale peers and routes; other VPNs
  {f}firewall{/}            the macOS application firewall
  {f}listening{/}           TCP ports that accept connections from other machines
  {f}all{/}                 every section in full

{h}NOTES{/}
  {dim}devices{/}   counts what this Mac has talked to lately. It is not a scan,
            and it cannot see subnets behind a VPN.
  {dim}wifi{/}      macOS hides the network name unless your terminal has
            Location Services permission. Everything else shows without it.
  {dim}firewall{/}  pf needs root, so snetwork does not read it.
`

const helpEnv = `{h}ENVIRONMENT{/}
  {a}NO_COLOR{/}       any value disables colour {dim}(no-color.org){/}
  {a}FORCE_COLOR{/}    any value but 0 forces colour, even when piped

{h}EXIT STATUS{/}
  {f}0{/} ok  {dim}·{/}  {f}1{/} couldn't write output  {dim}·{/}  {f}2{/} bad usage
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

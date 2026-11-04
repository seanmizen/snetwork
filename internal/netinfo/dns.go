package netinfo

import (
	"bufio"
	"sort"
	"strconv"
	"strings"
)

// parseDNS reads `scutil --dns`. It uses the first section ("DNS
// configuration") and ignores the scoped copy that follows it.
//
//	resolver #1
//	  search domain[0] : example.ts.net
//	  nameserver[0] : 100.100.100.100
//	  if_index : 29 (utun7)
//	  flags    : Supplemental, Request A records
//	  order    : 100800
func parseDNS(out string) DNS {
	if i := strings.Index(out, "DNS configuration (for scoped queries)"); i >= 0 {
		out = out[:i]
	}
	var all []Resolver
	var cur *Resolver
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "resolver #") {
			all = append(all, Resolver{})
			cur = &all[len(all)-1]
			continue
		}
		if cur == nil {
			continue
		}
		key, val, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case key == "domain":
			cur.Domain = strings.TrimSuffix(val, ".")
		case strings.HasPrefix(key, "search domain["):
			cur.Search = append(cur.Search, val)
		case strings.HasPrefix(key, "nameserver["):
			cur.Nameservers = append(cur.Nameservers, val)
		case key == "if_index":
			if _, name, ok := strings.Cut(val, "("); ok {
				cur.Interface = strings.TrimSuffix(name, ")")
			}
		case key == "options":
			cur.Options = val
		case key == "flags":
			for _, f := range strings.Split(val, ",") {
				if f = strings.TrimSpace(f); f != "" && !strings.HasPrefix(f, "Request ") {
					cur.Flags = append(cur.Flags, f)
				}
			}
		case key == "order":
			cur.Order, _ = strconv.Atoi(val)
		}
	}
	var d DNS
	for _, r := range all {
		if r.Domain == "" {
			d.General = append(d.General, r)
		} else {
			d.Domain = append(d.Domain, r)
		}
	}
	sort.SliceStable(d.General, func(i, j int) bool { return d.General[i].Order < d.General[j].Order })
	sort.SliceStable(d.Domain, func(i, j int) bool { return d.Domain[i].Order < d.Domain[j].Order })
	return d
}

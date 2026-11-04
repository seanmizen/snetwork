package netinfo

import (
	"bufio"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// netstatTrailing is the number of columns after process:pid in
// `netstat -anv` on macOS: state options gencnt flags flags1 usecnt rtncnt fltrs.
const netstatTrailing = 8

// parseListening reads `netstat -anv -p tcp` and returns the ports that
// listen beyond loopback, one entry per port and process.
//
//	tcp4  0  0  *.4100  *.*  LISTEN  0 0 131072 131072  bun:61666  00100 …
//
// The process name can hold spaces ("Google Chrome He:1136"), so it is
// everything between the 10th column and the last eight.
func parseListening(out string) []Listener {
	type key struct {
		port    int
		addr    string
		process string
	}
	seen := map[key]*Listener{}
	var order []key
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10+1+netstatTrailing || f[5] != "LISTEN" || !strings.HasPrefix(f[0], "tcp") {
			continue
		}
		addr, port, ok := splitLocal(f[3])
		if !ok || loopback(addr) {
			continue
		}
		proc := strings.Join(f[10:len(f)-netstatTrailing], " ")
		name, pid := proc, 0
		if i := strings.LastIndex(proc, ":"); i >= 0 {
			if n, err := strconv.Atoi(proc[i+1:]); err == nil {
				name, pid = proc[:i], n
			}
		}
		fam := "ipv4"
		if f[0] == "tcp6" {
			fam = "ipv6"
		} else if f[0] == "tcp46" {
			fam = "both"
		}
		k := key{port, addr, name}
		if l, ok := seen[k]; ok {
			if l.Family != fam {
				l.Family = "both"
			}
			continue
		}
		seen[k] = &Listener{Port: port, Address: addr, Family: fam, Process: name, PID: pid}
		order = append(order, k)
	}
	list := make([]Listener, 0, len(order))
	for _, k := range order {
		list = append(list, *seen[k])
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Port < list[j].Port })
	return list
}

// splitLocal splits netstat's "addr.port": "*.4100", "127.0.0.1.631",
// "::1.631", "fe80::1%lo0.631".
func splitLocal(s string) (addr string, port int, ok bool) {
	i := strings.LastIndex(s, ".")
	if i < 0 {
		return "", 0, false
	}
	p, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, false
	}
	return s[:i], p, true
}

func loopback(addr string) bool {
	if addr == "*" {
		return false
	}
	if strings.HasSuffix(addr, "%lo0") {
		return true
	}
	a, err := netip.ParseAddr(addr)
	return err == nil && a.IsLoopback()
}

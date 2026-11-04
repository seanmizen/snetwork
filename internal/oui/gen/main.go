// Command gen downloads the IEEE address-block registries and writes
// oui.txt.gz: one "PREFIX<tab>Vendor" line per block, sorted.
//
// Run from internal/oui with `go generate`, or with -dir to read CSV files
// downloaded earlier (oui.csv, mam.csv, oui36.csv).
package main

import (
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var registries = []struct{ file, url string }{
	{"oui.csv", "https://standards-oui.ieee.org/oui/oui.csv"},
	{"mam.csv", "https://standards-oui.ieee.org/oui28/mam.csv"},
	{"oui36.csv", "https://standards-oui.ieee.org/oui36/oui36.csv"},
}

func main() {
	dir := flag.String("dir", "", "read the CSV files from this folder instead of downloading them")
	out := flag.String("o", "oui.txt.gz", "output file")
	flag.Parse()

	lines := map[string]string{}
	for _, reg := range registries {
		var r io.ReadCloser
		if *dir != "" {
			f, err := os.Open(filepath.Join(*dir, reg.file))
			check(err)
			r = f
		} else {
			req, err := http.NewRequest("GET", reg.url, nil)
			check(err)
			req.Header.Set("User-Agent", "snetwork-oui-generator") // the IEEE refuses Go's default agent
			resp, err := http.DefaultClient.Do(req)
			check(err)
			if resp.StatusCode != 200 {
				check(fmt.Errorf("%s: %s", reg.url, resp.Status))
			}
			r = resp.Body
		}
		rows, err := csv.NewReader(r).ReadAll()
		r.Close()
		check(err)
		for _, row := range rows[1:] { // Registry,Assignment,Organization Name,Organization Address
			if len(row) >= 3 && row[2] != "Private" && row[2] != "IEEE Registration Authority" {
				lines[strings.ToUpper(row[1])] = short(row[2])
			}
		}
	}
	keys := make([]string, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	f, err := os.Create(*out)
	check(err)
	zw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	zw.ModTime = zw.ModTime.UTC() // zero: the same input gives the same bytes
	for _, k := range keys {
		fmt.Fprintf(zw, "%s\t%s\n", k, lines[k])
	}
	check(zw.Close())
	check(f.Close())
	fmt.Fprintf(os.Stderr, "wrote %s: %d blocks\n", *out, len(keys))
}

var (
	suffix = regexp.MustCompile(`(?i)[ ,.]+(co\.?,? ?ltd|co|ltd|limited|inc|incorporated|corp|corporation|llc|gmbh|ag|sa|s\.a|b\.v|bv|ab|oy|as|a/s|pte|pty|plc|srl|s\.r\.l|spa|kg|technologies|technology|tech|electronics|international|holdings|group|company|corporate)\.?$`)
	space  = regexp.MustCompile(`\s+`)
)

// short trims a registry name to what people call the company:
// "TP-LINK TECHNOLOGIES CO.,LTD." -> "TP-LINK", "Apple, Inc." -> "Apple".
func short(name string) string {
	s := space.ReplaceAllString(strings.TrimSpace(name), " ")
	for {
		t := strings.TrimSpace(suffix.ReplaceAllString(s, ""))
		t = strings.TrimRight(t, " ,.")
		if t == s || t == "" {
			break
		}
		s = t
	}
	if r := []rune(s); len(r) > 28 {
		s = strings.TrimSpace(string(r[:28])) + "…"
	}
	return s
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

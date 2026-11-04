// Package format turns colour specs into ANSI escapes and decides when to colour.
package format

import (
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// ColorEnabled reports whether output to w should be coloured.
// noColor (--no-color / NO_COLOR) wins; then FORCE_COLOR; otherwise colour
// is on only when w is a terminal that understands ANSI.
func ColorEnabled(w io.Writer, noColor bool, lookupEnv func(string) (string, bool)) bool {
	if noColor {
		return false
	}
	if v, ok := lookupEnv("FORCE_COLOR"); ok && v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return true
	}
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return false
	}
	if runtime.GOOS != "windows" {
		return true
	}
	// heuristics for modern Windows terminals
	get := func(k string) string { v, _ := lookupEnv(k); return v }
	return get("WT_SESSION") != "" ||
		strings.Contains(strings.ToLower(get("TERM_PROGRAM")), "vscode") ||
		get("ANSICON") != "" ||
		get("ConEmuANSI") == "ON"
}

var (
	named = map[string]int{ // 8-colour palette; +30 fg, +40 bg, +90/+100 bright
		"black": 0, "red": 1, "green": 2, "yellow": 3,
		"blue": 4, "magenta": 5, "cyan": 6, "white": 7,
	}
	attrs = map[string]string{
		"bold": "1", "dim": "2", "italic": "3", "ul": "4", "underline": "4",
		"blink": "5", "reverse": "7", "strike": "9",
	}
)

// SGR converts a colour spec to an ANSI escape sequence, as %C(spec) does.
// It returns "" for a spec it doesn't understand.
func SGR(spec string) string { return sgr(spec) }

// sgr converts a git-style colour spec to an ANSI "CSI … m" sequence.
// A spec is space-separated words: attributes (bold, dim, italic, ul, blink,
// reverse, strike) and up to two colours, foreground then background. A
// colour is a name (red, brightred, default), a 256-colour number (0-255),
// or a 24-bit hex value (#rrggbb or rrggbb). "reset" clears everything.
// Unknown words make the whole spec produce no colour, as in 1.0.
func sgr(spec string) string {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "reset" {
		return "\033[0m"
	}
	var codes []string
	colors := 0
	for _, w := range strings.Fields(spec) {
		if a, ok := attrs[w]; ok {
			codes = append(codes, a)
			continue
		}
		c, ok := colorCode(w, colors == 1)
		if !ok || colors == 2 {
			return ""
		}
		codes = append(codes, c)
		colors++
	}
	if len(codes) == 0 {
		return ""
	}
	return "\033[" + strings.Join(codes, ";") + "m"
}

func colorCode(w string, bg bool) (string, bool) {
	base, bright := 30, 90
	if bg {
		base, bright = 40, 100
	}
	if w == "default" {
		return strconv.Itoa(base + 9), true
	}
	if n, ok := named[w]; ok {
		return strconv.Itoa(base + n), true
	}
	if n, ok := named[strings.TrimPrefix(w, "bright")]; ok && strings.HasPrefix(w, "bright") {
		return strconv.Itoa(bright + n), true
	}
	ext := "38"
	if bg {
		ext = "48"
	}
	if hex := strings.TrimPrefix(w, "#"); len(hex) == 6 {
		if v, err := strconv.ParseUint(hex, 16, 32); err == nil {
			return ext + ";2;" + strconv.Itoa(int(v>>16)) + ";" +
				strconv.Itoa(int(v>>8&0xff)) + ";" + strconv.Itoa(int(v&0xff)), true
		}
	}
	if n, err := strconv.Atoi(w); err == nil && n >= 0 && n <= 255 && len(w) <= 3 {
		return ext + ";5;" + w, true
	}
	return "", false
}

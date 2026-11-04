// Package render turns a netinfo.Snapshot into text: the summary, one
// section in full, or every section. It is pure: the same snapshot and
// style always give the same text.
package render

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/seanmizen/snetwork/internal/format"
)

// Style says how to draw.
type Style struct {
	Color bool
}

// Colour roles. One place, so the later --watch colours (green, yellow,
// red) and the --active pending colour (purple) cannot clash with these.
const (
	roleTitle = "bold yellow"
	roleHint  = "dim"
	roleName  = "cyan"
	roleOK    = "green"
	roleBad   = "red"
	roleWarn  = "yellow"
)

func (s Style) paint(spec, text string) string {
	if !s.Color || text == "" || spec == "" {
		return text
	}
	return format.SGR(spec) + text + format.SGR("reset")
}

// cell is one table cell: text plus a colour role.
type cell struct {
	text string
	spec string
}

func c(text string) cell               { return cell{text: text} }
func cs(spec, text string) cell        { return cell{text: text, spec: spec} }
func cf(spec, f string, a ...any) cell { return cell{text: fmt.Sprintf(f, a...), spec: spec} }

// table lines up columns by visible width, then paints. The last column is
// not padded, so lines carry no trailing spaces.
func (s Style) table(indent string, rows [][]cell) string {
	var b strings.Builder
	for _, line := range s.tableLines(indent, rows) {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// tableLines is table, one string per row without the newline, so callers
// can put detail lines between aligned rows.
func (s Style) tableLines(indent string, rows [][]cell) []string {
	widths := map[int]int{}
	for _, r := range rows {
		for i, cl := range r {
			if n := utf8.RuneCountInString(cl.text); n > widths[i] {
				widths[i] = n
			}
		}
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		for len(r) > 0 && r[len(r)-1].text == "" {
			r = r[:len(r)-1]
		}
		var b strings.Builder
		b.WriteString(indent)
		for i, cl := range r {
			b.WriteString(s.paint(cl.spec, cl.text))
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cl.text)+2))
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

// heading is a section title with the command that expands it, right of it.
func (s Style) heading(title, hint string) string {
	const col = 50
	pad := col - utf8.RuneCountInString(title)
	if pad < 2 {
		pad = 2
	}
	if hint == "" {
		return s.paint(roleTitle, title) + "\n"
	}
	return s.paint(roleTitle, title) + strings.Repeat(" ", pad) + s.paint(roleHint, hint) + "\n"
}

// human formats a duration the short way: "24h", "21h 10m", "3d 4h", "45s".
func human(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	if days < 2 { // "24h" and "36h" read better than "1d" and "1d 12h"
		days = 0
	}
	h := int((d - time.Duration(days)*24*time.Hour) / time.Hour)
	m := int(d % time.Hour / time.Minute)
	sec := int(d % time.Minute / time.Second)
	switch {
	case days > 0 && h > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", sec)
}

func dash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

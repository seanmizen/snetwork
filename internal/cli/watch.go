package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/seanmizen/snetwork/internal/active"
	"github.com/seanmizen/snetwork/internal/collect"
	"github.com/seanmizen/snetwork/internal/format"
	"github.com/seanmizen/snetwork/internal/netinfo"
	"github.com/seanmizen/snetwork/internal/render"
)

// fresh is how long a change stays green, and a gone line red.
const fresh = 10 * time.Second

// Watch colours. Green and red mean a change; yellow, an older change that
// still differs; dim, a line gone for a while.
const (
	colNew  = "green"
	colOld  = "yellow"
	colGone = "red"
	colDead = "dim"
)

// tracker compares each frame with the first complete one, line by line.
// A line's key is its section, its indent and its first word, so a value
// that changes keeps its key: "en0 … up" and "en0 … no link" are one line.
type tracker struct {
	base      map[string]string    // key -> text in the first frame
	baseOrder []string             // keys in first-frame order
	last      map[string]string    // key -> text in the previous frame
	changed   map[string]time.Time // key -> when its text last changed
	gone      map[string]time.Time // key -> when it disappeared
}

type keyed struct{ key, plain, colored string }

// keyLines pairs each plain line with its coloured twin and a key.
func keyLines(plain, colored string) []keyed {
	p := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
	c := strings.Split(strings.TrimSuffix(colored, "\n"), "\n")
	section := "headline"
	seen := map[string]int{}
	out := make([]keyed, 0, len(p))
	for i, line := range p {
		trimmed := strings.TrimLeft(line, " ")
		first, _, _ := strings.Cut(trimmed, " ")
		indent := len(line) - len(trimmed)
		if indent == 0 && isSection(first) {
			section = first
		}
		k := fmt.Sprintf("%s|%d|%s", section, indent, first)
		seen[k]++
		if seen[k] > 1 {
			k += fmt.Sprintf("#%d", seen[k])
		}
		col := line
		if i < len(c) {
			col = c[i]
		}
		out = append(out, keyed{k, line, col})
	}
	return out
}

func newTracker(lines []keyed, now time.Time) *tracker {
	t := &tracker{base: map[string]string{}, last: map[string]string{}, changed: map[string]time.Time{}, gone: map[string]time.Time{}}
	for _, l := range lines {
		t.base[l.key] = l.plain
		t.baseOrder = append(t.baseOrder, l.key)
		t.last[l.key] = l.plain
	}
	return t
}

// mark draws one frame with its changes marked.
func (t *tracker) mark(lines []keyed, now time.Time, color bool) string {
	present := map[string]bool{}
	sectionEnd := map[string]int{} // section -> index of its last line
	for i, l := range lines {
		present[l.key] = true
		sectionEnd[sectionOf(l.key)] = i
		if prev, ok := t.last[l.key]; !ok || prev != l.plain {
			t.changed[l.key] = now
		}
		t.last[l.key] = l.plain
		delete(t.gone, l.key)
	}
	for _, k := range t.baseOrder {
		if !present[k] {
			if _, ok := t.gone[k]; !ok {
				t.gone[k] = now
			}
			delete(t.last, k)
		}
	}
	paint := func(spec, s string) string {
		if !color {
			return s
		}
		return format.SGR(spec) + s + format.SGR("reset")
	}
	symbol := func(sym, s string) string {
		if color {
			return s
		}
		if strings.HasPrefix(s, " ") {
			return sym + s[1:]
		}
		return sym + " " + s
	}
	goneAfter := map[int][]string{}
	for _, k := range t.baseOrder {
		since, ok := t.gone[k]
		if !ok {
			continue
		}
		at, ok := sectionEnd[sectionOf(k)]
		if !ok {
			at = len(lines) - 1
		}
		text := t.base[k] + "  gone"
		if now.Sub(since) < fresh {
			text = paint(colGone, text)
		} else {
			text = paint(colDead, text)
		}
		goneAfter[at] = append(goneAfter[at], symbol("-", text))
	}
	var b strings.Builder
	for i, l := range lines {
		base, inBase := t.base[l.key]
		switch {
		case inBase && base == l.plain:
			b.WriteString(l.colored)
		default:
			spec := colOld
			if now.Sub(t.changed[l.key]) < fresh {
				spec = colNew
			}
			sym := "~"
			if !inBase {
				sym = "+"
			}
			b.WriteString(symbol(sym, paint(spec, l.plain)))
		}
		b.WriteString("\n")
		for _, g := range goneAfter[i] {
			b.WriteString(g + "\n")
		}
	}
	return b.String()
}

func sectionOf(key string) string {
	s, _, _ := strings.Cut(key, "|")
	return s
}

// watch redraws the view every interval. With --active, the probes run
// again every active interval; between runs the last results stay on
// screen. Changes are marked against the first complete frame.
func watch(env Env, c config, st render.Style) error {
	view := c.section
	every := c.watch.interval()
	var (
		tr          *tracker
		prevLines   int
		activeState *live // the latest --active run, if any
		lastActive  time.Time
		frames      int
	)
	tty, _ := env.Terminal(env.Stdout)
	for {
		var snap netinfo.Snapshot
		if c.via != "" {
			s, err := viaSnapshot(env, c)
			if err != nil {
				return err
			}
			snap = s
		} else {
			raw := collect.Collect(env.GOOS, env.Run, env.WiFi)
			if activeState != nil {
				as := activeState.snapshot()
				activeState.mu.Lock()
				for _, k := range []string{"names-dns", "names-mdns"} {
					raw.Out[k] = activeState.raw.Out[k]
				}
				activeState.mu.Unlock()
				snap = netinfo.Build(raw, env.Now())
				snap.Active = as.Active
			} else {
				snap = netinfo.Build(raw, env.Now())
			}
			if c.active.on && (activeState == nil || env.Now().Sub(lastActive) >= c.active.interval()) {
				lastActive = env.Now()
				s := snap
				active.Plan(&s)
				if activeState == nil {
					snap.Active = s.Active
				}
				activeState = newLive(s, raw)
				as := activeState
				go active.Run(env.Ctx, s, env.Prober, as.update)
			}
		}
		if _, err := render.Blocks(view, c.item, snap, render.Style{}); err != nil {
			return usageError{err}
		}
		plainBlocks, _ := render.Blocks(view, c.item, snap, render.Style{})
		colBlocks, _ := render.Blocks(view, c.item, snap, st)
		lines := keyLines(render.Join(plainBlocks), render.Join(colBlocks))
		now := env.Now()
		var body string
		switch {
		case tr == nil && render.AllDone(snap):
			tr = newTracker(lines, now)
			fallthrough
		case tr != nil:
			body = tr.mark(lines, now, st.Color)
		default:
			body = render.Join(colBlocks) // the first sweep is still running
		}
		header := fmt.Sprintf("every %s · %s · ctrl-c stops", every, now.Format("15:04:05"))
		frame := st.Paint(roleHintSpec, header) + "\n" + body
		var out strings.Builder
		_, h := env.Terminal(env.Stdout)
		switch {
		case !tty:
			if frames > 0 {
				out.WriteString("\n")
			}
		case prevLines > 0 && (h == 0 || prevLines < h):
			fmt.Fprintf(&out, "\033[%dA\033[J", prevLines)
		default:
			out.WriteString("\033[H\033[2J")
		}
		out.WriteString(frame)
		if _, err := io.WriteString(env.Stdout, out.String()); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
		prevLines = strings.Count(frame, "\n")
		frames++
		if env.Frames > 0 && frames >= env.Frames {
			return nil
		}
		env.Sleep(env.Ctx, every)
		if env.Ctx.Err() != nil {
			return nil
		}
	}
}

// roleHintSpec matches render's hint colour, for the watch header.
const roleHintSpec = "dim"

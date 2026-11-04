package cli

import (
	"strings"
	"testing"
	"time"
)

func frame(lines ...string) []keyed {
	text := strings.Join(lines, "\n") + "\n"
	return keyLines(text, text)
}

func TestTrackerMarks(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)
	base := frame("vpn", "  alpha  ✓ online", "  bravo  ✓ online", "  phone  ✗ offline")
	tr := newTracker(base, t0)

	// first frame again: nothing marked
	if got := tr.mark(base, t0, false); got != "vpn\n  alpha  ✓ online\n  bravo  ✓ online\n  phone  ✗ offline\n" {
		t.Errorf("unchanged frame:\n%s", got)
	}
	// bravo changes, phone goes, charlie arrives
	f2 := frame("vpn", "  alpha  ✓ online", "  bravo  ✗ offline", "  charlie  ✓ online")
	got := tr.mark(f2, t0.Add(2*time.Second), false)
	want := "vpn\n  alpha  ✓ online\n~ bravo  ✗ offline\n+ charlie  ✓ online\n- phone  ✗ offline  gone\n"
	if got != want {
		t.Errorf("changed frame, no colour:\ngot\n%swant\n%s", got, want)
	}
	// colour: green while fresh, yellow after 10 s; gone red, then dim
	col := tr.mark(f2, t0.Add(5*time.Second), true)
	if !strings.Contains(col, "\033[32m  bravo  ✗ offline") || !strings.Contains(col, "\033[31m  phone  ✗ offline  gone") {
		t.Errorf("fresh colours:\n%q", col)
	}
	col = tr.mark(f2, t0.Add(13*time.Second), true)
	if !strings.Contains(col, "\033[33m  bravo  ✗ offline") || !strings.Contains(col, "\033[2m  phone  ✗ offline  gone") {
		t.Errorf("older colours:\n%q", col)
	}
	// bravo changes again: green again
	f3 := frame("vpn", "  alpha  ✓ online", "  bravo  ✓ online 40 ms", "  charlie  ✓ online")
	if col := tr.mark(f3, t0.Add(14*time.Second), true); !strings.Contains(col, "\033[32m  bravo  ✓ online 40 ms") {
		t.Errorf("a new change is green again:\n%q", col)
	}
	// back to the first value: no mark
	if got := tr.mark(base, t0.Add(20*time.Second), false); got != "vpn\n  alpha  ✓ online\n  bravo  ✓ online\n  phone  ✗ offline\n" {
		t.Errorf("back to the start:\n%s", got)
	}
}

func TestKeyLinesKeepKeysAcrossValues(t *testing.T) {
	a := frame("interfaces", "  en0  Wi-Fi  10.1.2.3/24  up")
	b := frame("interfaces", "  en0  Wi-Fi  –  no link")
	if a[1].key != b[1].key || a[1].key != "interfaces|2|en0" {
		t.Errorf("keys %q and %q", a[1].key, b[1].key)
	}
}

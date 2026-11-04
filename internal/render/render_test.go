package render

import (
	"testing"
	"time"
)

func TestHuman(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:                "45s",
		5 * time.Minute:                 "5m",
		24 * time.Hour:                  "24h",
		36*time.Hour + 30*time.Minute:   "36h 30m",
		21*time.Hour + 10*time.Minute:   "21h 10m",
		3*24*time.Hour + 4*time.Hour:    "3d 4h",
		-(time.Hour + 5*time.Minute):    "1h 5m",
		2*24*time.Hour + 20*time.Minute: "2d",
	}
	for d, want := range cases {
		if got := human(d); got != want {
			t.Errorf("human(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestWhen(t *testing.T) {
	now := time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)
	cases := map[time.Time]string{
		time.Date(2026, 11, 3, 18, 20, 0, 0, time.UTC): "18:20 today",
		time.Date(2026, 11, 4, 18, 20, 0, 0, time.UTC): "18:20 tomorrow",
		time.Date(2026, 11, 2, 9, 5, 0, 0, time.UTC):   "09:05 yesterday",
		time.Date(2026, 12, 1, 9, 5, 0, 0, time.UTC):   "2026-12-01 09:05",
	}
	for in, want := range cases {
		if got := when(in, now); got != want {
			t.Errorf("when(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestTableAlignsByVisibleWidth(t *testing.T) {
	st := Style{Color: true}
	got := st.table("", [][]cell{{cs("red", "a"), c("x")}, {c("ccc"), c("y")}})
	want := "\033[31ma\033[0m    x\nccc  y\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

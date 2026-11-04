package format

import (
	"bytes"
	"strings"
	"testing"
)

func TestSGR(t *testing.T) {
	cases := map[string]string{
		"red":            "\033[31m",
		"RED":            "\033[31m",
		"reset":          "\033[0m",
		"bold":           "\033[1m",
		"bold red":       "\033[1;31m",
		"dim":            "\033[2m",
		"red blue":       "\033[31;44m",
		"brightred":      "\033[91m",
		"default":        "\033[39m",
		"214":            "\033[38;5;214m",
		"0":              "\033[38;5;0m",
		"#fed7b0":        "\033[38;2;254;215;176m",
		"fed7b0":         "\033[38;2;254;215;176m",
		"ul #000000 214": "\033[4;38;2;0;0;0;48;5;214m",
		"256":            "", // out of range
		"red blue green": "", // three colours
		"bogus":          "", // unknown word: no colour, as in 1.0
		"":               "",
	}
	for spec, want := range cases {
		if got := sgr(spec); got != want {
			t.Errorf("sgr(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestColorEnabled(t *testing.T) {
	env := func(kv ...string) func(string) (string, bool) {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	var buf bytes.Buffer // not a terminal
	cases := []struct {
		name    string
		noColor bool
		env     func(string) (string, bool)
		want    bool
	}{
		{"pipe", false, env(), false},
		{"FORCE_COLOR", false, env("FORCE_COLOR", "1"), true},
		{"FORCE_COLOR=0", false, env("FORCE_COLOR", "0"), false},
		{"FORCE_COLOR empty", false, env("FORCE_COLOR", ""), false},
		{"no-color beats FORCE_COLOR", true, env("FORCE_COLOR", "1"), false},
	}
	for _, c := range cases {
		if got := ColorEnabled(&buf, c.noColor, c.env); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func FuzzSGR(f *testing.F) {
	f.Add("bold #fed7b0 214")
	f.Fuzz(func(t *testing.T, spec string) {
		if s := sgr(spec); s != "" && (!strings.HasPrefix(s, "\033[") || !strings.HasSuffix(s, "m")) {
			t.Fatalf("sgr(%q) = %q", spec, s)
		}
	})
}

// Run with: go test -bench=. -benchmem ./internal/format

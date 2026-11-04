package cli

import (
	"fmt"
	"time"
)

// durFlag is a switch that can take an interval: --watch, --watch=5s.
// A value needs the "=": `--watch 5s` reads 5s as a section, and run
// turns that into a clear error.
type durFlag struct {
	on   bool
	d    time.Duration
	def  time.Duration
	min  time.Duration
	name string
	set  bool // the value came with "="
}

func (f *durFlag) String() string {
	if f == nil || !f.on {
		return "false"
	}
	return f.interval().String()
}

func (f *durFlag) IsBoolFlag() bool { return true }

func (f *durFlag) Set(v string) error {
	switch v {
	case "true":
		f.on = true
		return nil
	case "false":
		f.on = false
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("--%s=%s: not an interval (try --%s=%s)", f.name, v, f.name, f.def)
	}
	if d < f.min {
		return fmt.Errorf("--%s=%s: the shortest interval is %s", f.name, v, f.min)
	}
	f.on, f.d, f.set = true, d, true
	return nil
}

func (f *durFlag) interval() time.Duration {
	if f.set {
		return f.d
	}
	return f.def
}

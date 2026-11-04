package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/seanmizen/snetwork/internal/netinfo"
	"github.com/seanmizen/snetwork/internal/render"
)

// live is a snapshot that background jobs fill in. Every change goes
// through update, under the lock, and wakes the screen.
type live struct {
	mu     sync.Mutex
	snap   netinfo.Snapshot
	raw    netinfo.Raw
	jobs   sync.WaitGroup
	wake   chan struct{}
	finish chan struct{}
	once   sync.Once
}

func newLive(s netinfo.Snapshot, r netinfo.Raw) *live {
	if r.Out == nil {
		r.Out = map[string][]byte{}
	}
	return &live{snap: s, raw: r, wake: make(chan struct{}, 1), finish: make(chan struct{})}
}

func (l *live) update(f func(*netinfo.Snapshot, *netinfo.Raw)) {
	l.mu.Lock()
	f(&l.snap, &l.raw)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// job runs f in the background; wait returns when every job is done.
func (l *live) job(f func()) {
	l.jobs.Add(1)
	go func() {
		defer l.jobs.Done()
		f()
	}()
}

func (l *live) wait() { l.jobs.Wait() }

// done closes when every job has finished.
func (l *live) done() <-chan struct{} {
	l.once.Do(func() {
		go func() {
			l.jobs.Wait()
			close(l.finish)
			select {
			case l.wake <- struct{}{}:
			default:
			}
		}()
	})
	return l.finish
}

// snapshot returns a deep copy, safe to read while jobs run.
func (l *live) snapshot() netinfo.Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return clone(l.snap)
}

func clone(s netinfo.Snapshot) netinfo.Snapshot {
	b, _ := json.Marshal(s)
	var out netinfo.Snapshot
	_ = json.Unmarshal(b, &out)
	out.Taken = s.Taken
	return out
}

func (l *live) pending() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !render.AllDone(l.snap)
}

func (l *live) frame(view, item string, st render.Style) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out, _ := render.Blocks(view, item, l.snap, st)
	return render.Join(out)
}

// redraw draws the whole view, then draws it again in place each time a
// result arrives: pending values show as a purple "…" until they fill in.
func redraw(env Env, l *live, view, item string, st render.Style, first string) error {
	if _, err := io.WriteString(env.Stdout, first); err != nil {
		return err
	}
	prev := strings.Count(first, "\n")
	done := l.done()
	tick := time.NewTicker(80 * time.Millisecond) // at most ~12 redraws a second
	defer tick.Stop()
	finished := false
	for !finished {
		select {
		case <-done:
			finished = true
		case <-env.Ctx.Done():
			return env.Ctx.Err()
		case <-tick.C:
			select {
			case <-l.wake:
			default:
				continue
			}
		}
		frame := l.frame(view, item, st)
		_, h := env.Terminal(env.Stdout)
		var b strings.Builder
		if prev > 0 && (h == 0 || prev < h) {
			fmt.Fprintf(&b, "\033[%dA\033[J", prev) // up, clear to the end
		} else {
			b.WriteString("\033[H\033[2J") // taller than the screen: start again
		}
		b.WriteString(frame)
		if _, err := io.WriteString(env.Stdout, b.String()); err != nil {
			return err
		}
		prev = strings.Count(frame, "\n")
	}
	return nil
}

// stream prints each block once it is complete, in order. It is the
// output for a pipe, a file, or a view taller than the screen.
func stream(env Env, l *live, view, item string, st render.Style) error {
	printed := 0
	done := l.done()
	for {
		l.mu.Lock()
		blocks, err := render.Blocks(view, item, l.snap, st)
		var out strings.Builder
		for printed < len(blocks) && render.Done(blocks[printed].Name, l.snap) {
			out.WriteString(blocks[printed].Text)
			printed++
		}
		total := len(blocks)
		l.mu.Unlock()
		if err != nil {
			return usageError{err}
		}
		if _, err := io.WriteString(env.Stdout, out.String()); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
		if printed >= total {
			return nil
		}
		select {
		case <-l.wake:
		case <-done:
		case <-env.Ctx.Done():
			return env.Ctx.Err()
		}
	}
}

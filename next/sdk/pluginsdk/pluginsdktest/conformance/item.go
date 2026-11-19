package conformance

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// itemTB is the testing.TB of one item (a case, or a suite-wide item) of
// one rule. It names the item and the rule in what it reports, and a fatal
// failure ends that item only. In record mode it keeps failures instead of
// reporting them: for exceptions, and for the self-test.
type itemTB struct {
	testing.TB
	item, rule string
	record     bool

	mu       sync.Mutex
	failures []string
	skipped  bool
}

// runItem runs fn on its own goroutine, so a Fatal or Skip ends fn alone.
func runItem(parent testing.TB, item, rule string, record bool, fn func(testing.TB)) *itemTB {
	t := &itemTB{TB: parent, item: item, rule: rule, record: record}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(t)
	}()
	<-done
	return t
}

func (t *itemTB) fail(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures = append(t.failures, msg)
	if !t.record {
		t.TB.Errorf("%s: %s\n\trule: %s", t.item, msg, t.rule)
	}
}

func (t *itemTB) Helper()                        {}
func (t *itemTB) Error(args ...any)              { t.fail(strings.TrimSuffix(fmt.Sprintln(args...), "\n")) }
func (t *itemTB) Errorf(format string, a ...any) { t.fail(fmt.Sprintf(format, a...)) }
func (t *itemTB) Fail()                          { t.fail("failed") }
func (t *itemTB) Fatal(args ...any)              { t.Error(args...); runtime.Goexit() }
func (t *itemTB) Fatalf(format string, a ...any) { t.Errorf(format, a...); runtime.Goexit() }
func (t *itemTB) FailNow()                       { t.Fail(); runtime.Goexit() }
func (t *itemTB) Log(args ...any)                { t.TB.Log(append([]any{t.item + ":"}, args...)...) }
func (t *itemTB) Logf(format string, a ...any) {
	t.TB.Logf("%s: "+format, append([]any{t.item}, a...)...)
}
func (t *itemTB) Skip(args ...any)              { t.Log(args...); t.SkipNow() }
func (t *itemTB) Skipf(format string, a ...any) { t.Logf(format, a...); t.SkipNow() }

func (t *itemTB) SkipNow() {
	t.mu.Lock()
	t.skipped = true
	t.mu.Unlock()
	runtime.Goexit()
}

func (t *itemTB) Skipped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.skipped
}

func (t *itemTB) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.failures) > 0
}

// judge runs one item of rule c, applying a declared exception: the item
// then runs in record mode, its failures are logged as excused, and it
// fails when it passes without the exception.
func (s *suite) judge(t testing.TB, c check, it item) {
	e, excused := s.exceptions[itemKey{c.name, it.name}]
	got := runItem(t, it.name, c.rule, excused, it.fn)
	switch {
	case !excused || got.Skipped():
	case !got.Failed():
		t.Errorf("%s: the exception %q is no longer needed, the rule passes: remove it\n\trule: %s", it.name, e.Reason, c.rule)
	default:
		t.Logf("%s: excused (%s): %s", it.name, e.Reason, strings.Join(got.failures, "; "))
	}
}

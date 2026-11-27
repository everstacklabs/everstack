package logger

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// countingHook records every entry logrus hands to hooks.
type countingHook struct {
	fired []*logrus.Entry
}

func (h *countingHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h *countingHook) Fire(e *logrus.Entry) error {
	h.fired = append(h.fired, e)
	return nil
}

// atLevel points the package logger at a buffer and a counting hook for one
// test, at the given level, and restores it afterwards.
func atLevel(t *testing.T, level logrus.Level) (*bytes.Buffer, *countingHook) {
	t.Helper()
	l := (*logrus.Logger)(log)
	prevOut, prevLevel, prevHooks := l.Out, l.GetLevel(), l.ReplaceHooks(make(logrus.LevelHooks))
	out, hook := &bytes.Buffer{}, &countingHook{}
	l.SetOutput(out)
	l.SetLevel(level)
	l.AddHook(hook)
	t.Cleanup(func() {
		l.SetOutput(prevOut)
		l.SetLevel(prevLevel)
		l.ReplaceHooks(prevHooks)
	})
	return out, hook
}

// A call below the active level must do none of the caller work. The caller
// field is the visible trace of that work: it is written onto the entry before
// logrus is asked to log anything.
func TestSuppressedLevelsSkipCallerLookup(t *testing.T) {
	out, hook := atLevel(t, logrus.WarnLevel)

	e := WithFields("request", "r1")
	e.Debug("debug line")
	e.Debugf("debug %s", "line")
	e.Info("info line")
	e.Infof("info %s", "line")
	e.Trace("trace line")
	e.Log(logrus.InfoLevel, "info via Log")
	Debug("package debug")
	Infof("package %s", "info")

	if out.Len() != 0 {
		t.Fatalf("suppressed levels wrote output: %q", out.String())
	}
	if len(hook.fired) != 0 {
		t.Fatalf("suppressed levels reached %d hook calls", len(hook.fired))
	}
	if e.base != nil {
		t.Fatalf("a suppressed call still built a logrus entry: %v", e.base.Data)
	}
	for n := e.pending; n != nil; n = n.prev {
		if n.key == "caller" {
			t.Fatalf("a suppressed call still looked up and attached its caller")
		}
	}
}

// At or above the active level nothing changes: the line is written, hooks
// fire, and it carries the file and line of the call site.
func TestEnabledLevelsStillLogWithCaller(t *testing.T) {
	out, hook := atLevel(t, logrus.WarnLevel)

	WithFields("request", "r1").Warn("warn line")
	Errorf("error %s", "line")

	if got := len(hook.fired); got != 2 {
		t.Fatalf("hook fired %d times, want 2", got)
	}
	for _, fired := range hook.fired {
		caller, _ := fired.Data["caller"].(string)
		if !strings.Contains(caller, "level_test.go:") {
			t.Fatalf("entry %q has caller %q, want this test file", fired.Message, caller)
		}
	}
	if !strings.Contains(out.String(), "warn line") || !strings.Contains(out.String(), "error line") {
		t.Fatalf("enabled lines missing from output: %q", out.String())
	}
}

// Raising the level makes the same calls log again, so the check follows the
// live level rather than one captured at start-up.
func TestLevelCheckFollowsTheLiveLevel(t *testing.T) {
	out, hook := atLevel(t, logrus.WarnLevel)
	Info("before")
	if len(hook.fired) != 0 {
		t.Fatalf("info logged at warn level")
	}
	SetLevel(logrus.DebugLevel)
	Info("after")
	Debug("after debug")
	if len(hook.fired) != 2 || !strings.Contains(out.String(), "after debug") {
		t.Fatalf("info and debug did not log after the level was lowered: %d hook calls, %q", len(hook.fired), out.String())
	}
}

// OnError keeps its meaning: nothing without an error, the error attached
// when there is one.
func TestOnErrorIsUnchanged(t *testing.T) {
	_, hook := atLevel(t, logrus.WarnLevel)

	OnError(nil).Warn("no error, no line")
	if len(hook.fired) != 0 {
		t.Fatalf("OnError(nil) logged")
	}
	boom := errors.New("boom")
	OnError(boom).Warn("failed")
	if len(hook.fired) != 1 || hook.fired[0].Data[logrus.ErrorKey] != boom {
		t.Fatalf("OnError(err) did not log the error: %+v", hook.fired)
	}
}

// BenchmarkSuppressedDebug is the cost of a log call the level filters out,
// which is most log calls on the request path in production.
func BenchmarkSuppressedDebug(b *testing.B) {
	l := (*logrus.Logger)(log)
	prevOut, prevLevel := l.Out, l.GetLevel()
	l.SetOutput(io.Discard)
	l.SetLevel(logrus.WarnLevel)
	defer func() { l.SetOutput(prevOut); l.SetLevel(prevLevel) }()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		WithFields("correlation_id", "chat_1", "model", "m").Debug("suppressed")
	}
}

package logger

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// here returns "file:line" of the line `offset` lines below its call.
func here(offset int) string {
	_, file, line, _ := runtime.Caller(1)
	return fmt.Sprintf("%s:%d", file, line+offset)
}

// Fields are applied lazily, so the result has to be exactly what applying
// them one by one to a logrus entry gives: the same keys, the latest value for
// a key set twice, and logrus's own handling of values it refuses.
func TestFieldsMatchApplyingThemToLogrusOneByOne(t *testing.T) {
	_, hook := atLevel(t, logrus.InfoLevel)
	boom := errors.New("boom")
	fn := func() {}

	cases := []struct {
		name  string
		build func() *Entry
		want  func(*logrus.Entry) *logrus.Entry
	}{
		{"operational chain",
			func() *Entry {
				return WithCategory(CategoryOperational).WithLogEvent(EventRateLimitUpdated).WithPayload(`{"a":1}`).
					SetFields("provider", "openai", "model", "m", "remaining", 7)
			},
			func(e *logrus.Entry) *logrus.Entry {
				return e.WithField("log_category", CategoryOperational).WithField("log_event", EventRateLimitUpdated).
					WithField("log_payload", `{"a":1}`).WithFields(logrus.Fields{"provider": "openai", "model": "m", "remaining": 7})
			}},
		{"a key set twice keeps the latest value",
			func() *Entry { return WithFields("k", 1).WithField("k", 2).SetFields("k", 3, "other", "x") },
			func(e *logrus.Entry) *logrus.Entry {
				return e.WithField("k", 1).WithField("k", 2).WithFields(logrus.Fields{"k": 3, "other": "x"})
			}},
		{"a map of fields and an error",
			func() *Entry { return New().WithFields(logrus.Fields{"a": 1, "b": "two"}).WithError(boom) },
			func(e *logrus.Entry) *logrus.Entry {
				return e.WithFields(logrus.Fields{"a": 1, "b": "two"}).WithError(boom)
			}},
		{"an odd number of fields",
			func() *Entry { return WithFields("a", 1, "dangling") },
			func(e *logrus.Entry) *logrus.Entry { return e.WithFields(logrus.Fields{"oddFields": 3}) }},
		{"the deprecated id helpers",
			func() *Entry { return LogWithFields("id-1", "a", 1) },
			func(e *logrus.Entry) *logrus.Entry { return e.WithField(idKey, "id-1").WithField("a", 1) }},
		{"a value logrus refuses",
			func() *Entry { return WithFields("ok", 1).WithField("callback", fn) },
			func(e *logrus.Entry) *logrus.Entry { return e.WithField("ok", 1).WithField("callback", fn) }},
		{"no fields at all",
			func() *Entry { return New() },
			func(e *logrus.Entry) *logrus.Entry { return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hook.fired = nil
			want := tc.want(logrus.NewEntry((*logrus.Logger)(log)))
			tc.build().Info("line")
			want.Info("line")
			if len(hook.fired) != 2 {
				t.Fatalf("hook fired %d times, want 2", len(hook.fired))
			}
			got, ref := hook.fired[0].Data, hook.fired[1].Data
			if _, ok := got["caller"]; !ok {
				t.Fatalf("no caller on the line: %v", got)
			}
			delete(got, "caller")
			if !reflect.DeepEqual(got, ref) {
				t.Fatalf("fields differ\n got: %v\nwant: %v", got, ref)
			}
		})
	}
}

// The caller is cached by call site. Every form of call has to report the
// line it was made from, the first time and from the cache.
func TestCallerIsTheCallSiteFirstTimeAndCached(t *testing.T) {
	_, hook := atLevel(t, logrus.InfoLevel)

	var want []string
	for i := 0; i < 3; i++ {
		want = append(want, here(1))
		WithFields("i", i).Info("method")
	}
	for i := 0; i < 3; i++ {
		want = append(want, here(1))
		Infof("package function %d", i)
	}
	want = append(want, here(1))
	New().Log(logrus.WarnLevel, "via Log")
	want = append(want, here(1))
	Logf(logrus.ErrorLevel, "via %s", "Logf")
	func() {
		want = append(want, here(1))
		Warn("from a closure")
	}()

	if len(hook.fired) != len(want) {
		t.Fatalf("hook fired %d times, want %d", len(hook.fired), len(want))
	}
	for i, fired := range hook.fired {
		if got := fired.Data["caller"]; got != want[i] {
			t.Errorf("line %d (%q): caller = %v, want %s", i, fired.Message, got, want[i])
		}
	}
}

// An entry kept and logged twice carries its earlier fields into the second
// line, as it always has, and each line gets its own caller.
func TestAnEntryLoggedTwiceKeepsItsFields(t *testing.T) {
	_, hook := atLevel(t, logrus.InfoLevel)

	e := WithFields("request", "r1")
	first := here(1)
	e.WithField("step", 1).Info("one")
	second := here(1)
	e.WithField("step", 2).WithField("extra", true).Info("two")

	if len(hook.fired) != 2 {
		t.Fatalf("hook fired %d times, want 2", len(hook.fired))
	}
	if got := hook.fired[0].Data; !reflect.DeepEqual(got, logrus.Fields{"request": "r1", "step": 1, "caller": first}) {
		t.Fatalf("first line fields = %v", got)
	}
	if got := hook.fired[1].Data; !reflect.DeepEqual(got, logrus.Fields{"request": "r1", "step": 2, "extra": true, "caller": second}) {
		t.Fatalf("second line fields = %v", got)
	}
}

// A line dropped by the level leaves its fields on the entry for a later line
// that is written.
func TestFieldsSurviveASuppressedLine(t *testing.T) {
	_, hook := atLevel(t, logrus.WarnLevel)

	e := WithFields("request", "r1")
	e.WithField("detail", "x").Debug("dropped")
	e.Warn("kept")

	if len(hook.fired) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(hook.fired))
	}
	got := hook.fired[0].Data
	if got["request"] != "r1" || got["detail"] != "x" {
		t.Fatalf("fields lost across a suppressed line: %v", got)
	}
}

func TestWithTimeSetsTheLineTime(t *testing.T) {
	_, hook := atLevel(t, logrus.InfoLevel)
	at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	WithFields("before", 1).WithTime(at).WithField("after", 2).Info("timed")

	if len(hook.fired) != 1 || !hook.fired[0].Time.Equal(at) {
		t.Fatalf("time not applied: %+v", hook.fired)
	}
	if got := hook.fired[0].Data; got["before"] != 1 || got["after"] != 2 {
		t.Fatalf("fields around WithTime lost: %v", got)
	}
}

// BenchmarkOperationalLine is one customer-facing log line the way the chat
// path writes them: category, event, payload and a few fields.
func BenchmarkOperationalLine(b *testing.B) {
	for _, level := range []logrus.Level{logrus.WarnLevel, logrus.InfoLevel} {
		b.Run("level="+level.String(), func(b *testing.B) {
			l := (*logrus.Logger)(log)
			prevOut, prevLevel := l.Out, l.GetLevel()
			l.SetOutput(io.Discard)
			l.SetLevel(level)
			defer func() { l.SetOutput(prevOut); l.SetLevel(prevLevel) }()

			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				WithCategory(CategoryOperational).
					WithLogEvent(EventProviderResponseReceived).
					WithPayload(`{"provider":{"name":"openai"}}`).
					SetFields("correlation_id", "chat_1", "provider", "openai", "model", "gpt-4o-mini", "status", 200).
					Info("provider responded")
			}
		})
	}
}

// An entry that keeps getting fields without writing a line stays bounded.
func TestPendingFieldsAreBounded(t *testing.T) {
	_, hook := atLevel(t, logrus.WarnLevel)
	e := WithFields("request", "r1")
	for i := 0; i < 1000; i++ {
		e.WithField("step", i).Debug("dropped")
	}
	if e.pending != nil && e.pending.count > maxPendingFields {
		t.Fatalf("pending chain grew to %d", e.pending.count)
	}
	e.Warn("kept")
	if len(hook.fired) != 1 || hook.fired[0].Data["step"] != 999 || hook.fired[0].Data["request"] != "r1" {
		t.Fatalf("fields lost when folding: %+v", hook.fired)
	}
}

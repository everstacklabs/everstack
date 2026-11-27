package logger

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Log category constants for OTEL filtering
const (
	CategoryOperational = "operational" // Customer-facing operational logs (forwarded to OTEL)
	CategorySystem      = "system"      // Internal system/startup logs (stdout only, not forwarded to OTEL)
)

// Log event constants for fast indexed filtering in ClickHouse
const (
	EventGatewayRequestReceived   = "gateway.request.received"
	EventGatewayRequestCompleted  = "gateway.request.completed"
	EventGatewayError             = "gateway.error"
	EventProviderRequestIssued    = "provider.request.issued"
	EventProviderResponseReceived = "provider.response.received"
	EventProviderError            = "provider.error"
	EventCommandStarted           = "command.started"
	EventCommandCompleted         = "command.completed"
	EventRateLimitUpdated         = "rate_limit.updated"
	EventFallbackTriggered        = "fallback.triggered"
	EventKeyRotationAttempted     = "key_rotation.attempted"

	// Function execution events
	EventFunctionExecutionStarted   = "function.execution.started"
	EventFunctionExecutionCompleted = "function.execution.completed"
	EventFunctionExecutionError     = "function.execution.error"

	// Workflow execution events
	EventWorkflowNodeCompleted = "workflow.node.completed"
	EventWorkflowNodeError     = "workflow.node.error"
	EventWorkflowExecutionDone = "workflow.execution.done"
)

// Entry collects the fields of one log line and emits it.
//
// Fields are kept as a chain of small nodes and only turned into a logrus
// entry when a line is written. logrus copies an entry's whole field map on
// every WithField, so the usual chain (category, event, payload, a handful of
// fields, the caller) used to build and discard six or more maps per line, and
// did so even for a line the level then dropped. A chat request logs about a
// dozen lines; at warn level, where none of them are written, building their
// fields was still 4% of the gateway's CPU and 10% of what it allocated.
//
// Like the entry it replaces, an Entry is mutated by its With* methods and is
// not safe for concurrent use.
type Entry struct {
	lg        *logrus.Logger
	base      *logrus.Entry // fields already applied by an earlier line; nil until one is written
	pending   *fieldNode    // fields added since, newest first
	isOnError bool
	err       error
}

// fieldNode is one pending field. The chain is persistent (a node is never
// changed once linked), so adding a field is a single pointer store.
type fieldNode struct {
	key   string
	value interface{}
	prev  *fieldNode
	count int // nodes in the chain ending at this one
}

// maxPendingFields bounds the chain on an entry that keeps getting fields
// without writing a line, as the field map it replaces was bounded by its keys.
const maxPendingFields = 32

func (e *Entry) push(key string, value interface{}) {
	count := 1
	if e.pending != nil {
		count = e.pending.count + 1
	}
	e.pending = &fieldNode{key: key, value: value, prev: e.pending, count: count}
	if count > maxPendingFields {
		e.entry()
	}
}

// entry returns the logrus entry carrying every field added so far.
func (e *Entry) entry() *logrus.Entry {
	base := e.base
	if base == nil {
		lg := e.lg
		if lg == nil {
			lg = (*logrus.Logger)(log)
		}
		base = logrus.NewEntry(lg)
	}
	if e.pending != nil {
		fields := make(logrus.Fields, e.pending.count)
		for n := e.pending; n != nil; n = n.prev {
			// Newest first: a key set twice keeps its latest value.
			if _, set := fields[n.key]; !set {
				fields[n.key] = n.value
			}
		}
		base = base.WithFields(fields)
		e.pending = nil
	}
	e.base = base
	return base
}

var idKey = "logID"

// SetIDKey key of id in logentry
func SetIDKey(key string) {
	idKey = key
}

// Deprecated: Log creates a new entry with an id
func Log(id string) *Entry {
	return New().WithField(idKey, id)
}

// Deprecated: LogWithFields creates a new entry with an id and the given fields
func LogWithFields(id string, fields ...interface{}) *Entry {
	e := Log(id)
	return e.SetFields(fields...)
}

// New instantiates a new entry
func New() *Entry {
	return &Entry{lg: (*logrus.Logger)(log)}
}

func OnError(err error) *Entry {
	e := New()
	return e.OnError(err)
}

func WithError(err error) *Entry {
	e := New()
	return e.WithError(err)
}

// WithFields creates a new entry without an id and the given fields
func WithFields(fields ...interface{}) *Entry {
	return New().SetFields(fields...)
}

// WithCategory adds a log category field for OTEL filtering
// Use CategoryOperational for customer-facing logs, CategorySystem for internal logs
func WithCategory(category string) *Entry {
	return New().WithField("log_category", category)
}

// WithCategory adds a log category field for OTEL filtering
func (e *Entry) WithCategory(category string) *Entry {
	return e.WithField("log_category", category)
}

// WithLogEvent sets the log event type for fast indexed filtering
// Use the Event* constants for consistency
func WithLogEvent(event string) *Entry {
	return New().WithField("log_event", event)
}

// WithLogEvent sets the log event type for fast indexed filtering
func (e *Entry) WithLogEvent(event string) *Entry {
	return e.WithField("log_event", event)
}

// WithPayload attaches a structured payload to the log
// Use NewPayload() builder to construct the payload
func (e *Entry) WithPayload(payload string) *Entry {
	return e.WithField("log_payload", payload)
}

// OnError sets the error. The log will only be printed if err is not nil
func (e *Entry) OnError(err error) *Entry {
	e.err = err
	e.isOnError = true
	return e
}

// SetFields sets the given key, value pairs on the entry. An odd number of
// arguments records the count under "oddFields" instead.
func (e *Entry) SetFields(fields ...interface{}) *Entry {
	if len(fields)%2 != 0 {
		e.push("oddFields", len(fields))
		return e
	}
	for i := 0; i < len(fields); i += 2 {
		e.push(fields[i].(string), fields[i+1])
	}
	return e
}

func (e *Entry) WithField(key string, value interface{}) *Entry {
	e.push(key, value)
	return e
}

func (e *Entry) WithFields(fields logrus.Fields) *Entry {
	for key, value := range fields {
		e.push(key, value)
	}
	return e
}

func (e *Entry) WithError(err error) *Entry {
	e.push(logrus.ErrorKey, err)
	return e
}

func (e *Entry) WithTime(t time.Time) *Entry {
	e.base = e.entry().WithTime(t)
	return e
}

func Debug(args ...interface{}) {
	e := New()
	e.log(logrus.DebugLevel, func(le *logrus.Entry) { le.Debug(args...) })
}

func (e *Entry) Debug(args ...interface{}) {
	e.log(logrus.DebugLevel, func(le *logrus.Entry) { le.Debug(args...) })
}

func Debugln(args ...interface{}) {
	e := New()
	e.log(logrus.DebugLevel, func(le *logrus.Entry) { le.Debugln(args...) })
}

func (e *Entry) Debugln(args ...interface{}) {
	e.log(logrus.DebugLevel, func(le *logrus.Entry) { le.Debugln(args...) })
}

func Debugf(format string, args ...interface{}) {
	e := New()
	e.log(logrus.DebugLevel, func(le *logrus.Entry) { le.Debugf(format, args...) })
}

func (e *Entry) Debugf(format string, args ...interface{}) {
	e.log(logrus.DebugLevel, func(le *logrus.Entry) { le.Debugf(format, args...) })
}

func Info(args ...interface{}) {
	e := New()
	e.log(logrus.InfoLevel, func(le *logrus.Entry) { le.Info(args...) })
}

func (e *Entry) Info(args ...interface{}) {
	e.log(logrus.InfoLevel, func(le *logrus.Entry) { le.Info(args...) })
}

func Infoln(args ...interface{}) {
	e := New()
	e.log(logrus.InfoLevel, func(le *logrus.Entry) { le.Infoln(args...) })
}

func (e *Entry) Infoln(args ...interface{}) {
	e.log(logrus.InfoLevel, func(le *logrus.Entry) { le.Infoln(args...) })
}

func Infof(format string, args ...interface{}) {
	e := New()
	e.log(logrus.InfoLevel, func(le *logrus.Entry) { le.Infof(format, args...) })
}

func (e *Entry) Infof(format string, args ...interface{}) {
	e.log(logrus.InfoLevel, func(le *logrus.Entry) { le.Infof(format, args...) })
}

func Trace(args ...interface{}) {
	e := New()
	e.log(logrus.TraceLevel, func(le *logrus.Entry) { le.Trace(args...) })
}

func (e *Entry) Trace(args ...interface{}) {
	e.log(logrus.TraceLevel, func(le *logrus.Entry) { le.Trace(args...) })
}

func Traceln(args ...interface{}) {
	e := New()
	e.log(logrus.TraceLevel, func(le *logrus.Entry) { le.Traceln(args...) })
}

func (e *Entry) Traceln(args ...interface{}) {
	e.log(logrus.TraceLevel, func(le *logrus.Entry) { le.Traceln(args...) })
}

func Tracef(format string, args ...interface{}) {
	e := New()
	e.log(logrus.TraceLevel, func(le *logrus.Entry) { le.Tracef(format, args...) })
}

func (e *Entry) Tracef(format string, args ...interface{}) {
	e.log(logrus.TraceLevel, func(le *logrus.Entry) { le.Tracef(format, args...) })
}

func Warn(args ...interface{}) {
	e := New()
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warn(args...) })
}

func (e *Entry) Warn(args ...interface{}) {
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warn(args...) })
}

func Warnln(args ...interface{}) {
	e := New()
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warnln(args...) })
}

func (e *Entry) Warnln(args ...interface{}) {
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warnln(args...) })
}

func Warnf(format string, args ...interface{}) {
	e := New()
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warnf(format, args...) })
}

func (e *Entry) Warnf(format string, args ...interface{}) {
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warnf(format, args...) })
}

func Warning(args ...interface{}) {
	e := New()
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warning(args...) })
}

func (e *Entry) Warning(args ...interface{}) {
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warning(args...) })
}

func Warningln(args ...interface{}) {
	e := New()
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warningln(args...) })
}

func (e *Entry) Warningln(args ...interface{}) {
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warningln(args...) })
}

func Warningf(format string, args ...interface{}) {
	e := New()
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warningf(format, args...) })
}

func (e *Entry) Warningf(format string, args ...interface{}) {
	e.log(logrus.WarnLevel, func(le *logrus.Entry) { le.Warningf(format, args...) })
}

func Error(args ...interface{}) {
	e := New()
	e.log(logrus.ErrorLevel, func(le *logrus.Entry) { le.Error(args...) })
}

func (e *Entry) Error(args ...interface{}) {
	e.log(logrus.ErrorLevel, func(le *logrus.Entry) { le.Error(args...) })
}

func Errorln(args ...interface{}) {
	e := New()
	e.log(logrus.ErrorLevel, func(le *logrus.Entry) { le.Errorln(args...) })
}

func (e *Entry) Errorln(args ...interface{}) {
	e.log(logrus.ErrorLevel, func(le *logrus.Entry) { le.Errorln(args...) })
}

func Errorf(format string, args ...interface{}) {
	e := New()
	e.log(logrus.ErrorLevel, func(le *logrus.Entry) { le.Errorf(format, args...) })
}

func (e *Entry) Errorf(format string, args ...interface{}) {
	e.log(logrus.ErrorLevel, func(le *logrus.Entry) { le.Errorf(format, args...) })
}

func Fatal(args ...interface{}) {
	e := New()
	e.log(logrus.FatalLevel, func(le *logrus.Entry) { le.Fatal(args...) })
}

func (e *Entry) Fatal(args ...interface{}) {
	e.log(logrus.FatalLevel, func(le *logrus.Entry) { le.Fatal(args...) })
}

func Fatalln(args ...interface{}) {
	e := New()
	e.log(logrus.FatalLevel, func(le *logrus.Entry) { le.Fatalln(args...) })
}

func (e *Entry) Fatalln(args ...interface{}) {
	e.log(logrus.FatalLevel, func(le *logrus.Entry) { le.Fatalln(args...) })
}

func Fatalf(format string, args ...interface{}) {
	e := New()
	e.log(logrus.FatalLevel, func(le *logrus.Entry) { le.Fatalf(format, args...) })
}

func (e *Entry) Fatalf(format string, args ...interface{}) {
	e.log(logrus.FatalLevel, func(le *logrus.Entry) { le.Fatalf(format, args...) })
}

func Panic(args ...interface{}) {
	e := New()
	e.log(logrus.PanicLevel, func(le *logrus.Entry) { le.Panic(args...) })
}

func (e *Entry) Panic(args ...interface{}) {
	e.log(logrus.PanicLevel, func(le *logrus.Entry) { le.Panic(args...) })
}

func Panicln(args ...interface{}) {
	e := New()
	e.log(logrus.PanicLevel, func(le *logrus.Entry) { le.Panicln(args...) })
}

func (e *Entry) Panicln(args ...interface{}) {
	e.log(logrus.PanicLevel, func(le *logrus.Entry) { le.Panic(args...) })
}

func Panicf(format string, args ...interface{}) {
	e := New()
	e.log(logrus.PanicLevel, func(le *logrus.Entry) { le.Panicf(format, args...) })
}

func (e *Entry) Panicf(format string, args ...interface{}) {
	e.log(logrus.PanicLevel, func(le *logrus.Entry) { le.Panicf(format, args...) })
}

func (e *Entry) Log(level logrus.Level, args ...interface{}) {
	e.log(level, func(le *logrus.Entry) { le.Log(level, args...) })
}

func Logf(level logrus.Level, format string, args ...interface{}) {
	e := New()
	e.log(level, func(le *logrus.Entry) { le.Logf(level, format, args...) })
}

func (e *Entry) Logf(level logrus.Level, format string, args ...interface{}) {
	e.log(level, func(le *logrus.Entry) { le.Logf(level, format, args...) })
}

func Logln(level logrus.Level, args ...interface{}) {
	e := New()
	e.log(level, func(le *logrus.Entry) { le.Logln(level, args...) })
}

func (e *Entry) Logln(level logrus.Level, args ...interface{}) {
	e.log(level, func(le *logrus.Entry) { le.Logln(level, args...) })
}

// IsLevelEnabled reports whether a line at level would be logged. Use it to
// skip building an expensive field or payload for a line the level filters
// out; the logging calls themselves already cost little when suppressed.
func IsLevelEnabled(level logrus.Level) bool {
	return (*logrus.Logger)(log).IsLevelEnabled(level)
}

// log emits the entry at level through logFn, after attaching the caller.
//
// An entry below the logger's level is dropped by logrus before any formatter
// or hook sees it, so it is dropped here first, before the caller is looked up
// or a single field map is built. Fatal and Panic always go through, because
// logrus exits or panics for them whether or not the level is enabled.
func (e *Entry) log(level logrus.Level, logFn func(*logrus.Entry)) {
	lg := e.lg
	if lg == nil {
		lg = (*logrus.Logger)(log)
	}
	if level > logrus.FatalLevel && !lg.IsLevelEnabled(level) {
		return
	}
	if e.isOnError {
		if e.err == nil {
			return
		}
		e.push(logrus.ErrorKey, e.err)
	}
	if caller, ok := callerOf(3); ok {
		e.push("caller", caller)
	}
	logFn(e.entry())
}

// callerCache maps a call site's program counter to its "file:line" string.
// Finding the caller is one stack walk either way; resolving it to a file and
// line and formatting the string is the expensive part, and a process only has
// as many distinct log call sites as it has log statements.
var callerCache sync.Map // uintptr -> string

// callerOf returns "file:line" for the frame runtime.Caller(skip) would report
// if it were called here: 0 is callerOf itself, 1 its caller, and so on.
func callerOf(skip int) (string, bool) {
	var pc [1]uintptr
	// Callers counts itself as frame 0, so callerOf is frame 1.
	if runtime.Callers(skip+1, pc[:]) < 1 {
		return "", false
	}
	if cached, ok := callerCache.Load(pc[0]); ok {
		return cached.(string), true
	}
	frame, _ := runtime.CallersFrames(pc[:]).Next()
	if frame.PC == 0 {
		return "", false
	}
	caller := fmt.Sprintf("%s:%d", frame.File, frame.Line)
	callerCache.Store(pc[0], caller)
	return caller, true
}

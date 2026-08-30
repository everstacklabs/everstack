package logger

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type logger logrus.Logger

var log *logger = (*logger)(logrus.StandardLogger())

func SetOutput(out io.Writer) {
	(*logrus.Logger)(log).SetOutput(out)
}

func SetFormatter(formatter logrus.Formatter) {
	// logger doesn't color code debug logs
	if formatter == nil {
		// formatter = &logrus.TextFormatter{
		// 	FullTimestamp: true,
		// 	CallerPrettyfier: func(frame *runtime.Frame) (function string, file string) {
		// 		return frame.Function, fmt.Sprintf("%s:%d", frame.File, frame.Line)
		// 	},
		// }
		formatter = NewDefaultBracketFormatter()
	}
	(*logrus.Logger)(log).SetFormatter(formatter)
}

func SetLevel(level logrus.Level) {
	(*logrus.Logger)(log).SetLevel(level)
}

func SetGlobal() {
	logrus.SetFormatter(log.Formatter)
	logrus.SetLevel(log.Level)
	logrus.SetReportCaller(log.ReportCaller)
	logrus.SetOutput(log.Out)
	log = (*logger)(logrus.StandardLogger())
}

// BracketFormatter renders logs like:
// 2018-10-19T15:26:27.858-0400 [INFO ] message key=value
type BracketFormatter struct {
	TimestampFormat string
	PadLevel        bool
	UppercaseLevel  bool
}

func NewDefaultBracketFormatter() *BracketFormatter {
	return &BracketFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000-0700",
		PadLevel:        true,
		UppercaseLevel:  true,
	}
}

func (f *BracketFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var buf bytes.Buffer

	// Timestamp
	layout := f.TimestampFormat
	if layout == "" {
		layout = time.RFC3339
	}
	buf.WriteString(entry.Time.Format(layout))
	buf.WriteByte(' ')

	// Level
	lvl := entry.Level.String()
	if f.UppercaseLevel {
		lvl = strings.ToUpper(lvl)
	}
	if f.PadLevel && len(lvl) < 5 {
		lvl = lvl + strings.Repeat(" ", 5-len(lvl))
	}

	// Apply color based on level
	switch entry.Level {
	case logrus.TraceLevel:
		buf.WriteString("\x1b[36m") // teal
	case logrus.DebugLevel:
		buf.WriteString("\x1b[34m") // blue
	case logrus.InfoLevel:
		buf.WriteString("\x1b[32m") // green
	case logrus.WarnLevel:
		buf.WriteString("\x1b[33m") // yellow
	case logrus.ErrorLevel:
		buf.WriteString("\x1b[31m") // red
	case logrus.FatalLevel:
		buf.WriteString("\x1b[31m") // red
	case logrus.PanicLevel:
		buf.WriteString("\x1b[31m") // red
	}

	buf.WriteString("[")
	buf.WriteString(lvl)
	buf.WriteString("]")
	buf.WriteString("\x1b[0m") // reset color
	buf.WriteString(" ")

	// Message (optional component prefix)
	if comp, ok := entry.Data["component"].(string); ok && comp != "" {
		buf.WriteString(comp)
		buf.WriteString(": ")
	}
	buf.WriteString(entry.Message)

	// Fields (omit caller; stable order)
	keys := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		if k == "component" || k == "caller" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Color only the keys with fixed colors
	colors := []string{"\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[34m", "\x1b[35m", "\x1b[36m"}
	for i, k := range keys {
		buf.WriteByte(' ')
		buf.WriteString(colors[i%len(colors)])
		buf.WriteString(k)
		buf.WriteString("\x1b[0m")
		buf.WriteByte('=')
		buf.WriteString(fmt.Sprint(entry.Data[k]))
	}

	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

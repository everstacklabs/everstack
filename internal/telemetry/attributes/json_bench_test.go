package attributes

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// sizedPayload builds a synthetic message payload of approximately sizeBytes
// for cost modeling across typical (2KB), large (100KB) and outlier (1MB)
// trace payloads.
func sizedPayload(sizeBytes int) []gw.Message {
	unit := "lorem ipsum dolor sit amet <tag> & \"quoted\" — café 🎉 "
	reps := sizeBytes/len(unit) + 1
	return []gw.Message{
		gw.NewMessage(gw.RoleSystem, gw.Text("You are helpful.")),
		gw.NewMessage(gw.RoleUser, gw.Text(strings.Repeat(unit, reps))),
	}
}

// BenchmarkMarshalBySize measures per-request marshal cost by payload size.
// This is the raw input to the trace-blob perf calculation.
func BenchmarkMarshalBySize(b *testing.B) {
	for _, size := range []int{2 * 1024, 100 * 1024, 1024 * 1024} {
		msgs := sizedPayload(size)
		raw, err := json.Marshal(msgs)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(strings.ReplaceAll(humanBytes(len(raw)), " ", ""), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for i := 0; i < b.N; i++ {
				if _, err := json.Marshal(msgs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkGzipBySize measures one-shot gzip cost (the blob upload path) at
// DefaultCompression and BestSpeed, by payload size.
func BenchmarkGzipBySize(b *testing.B) {
	for _, size := range []int{2 * 1024, 100 * 1024, 1024 * 1024} {
		msgs := sizedPayload(size)
		raw, err := json.Marshal(msgs)
		if err != nil {
			b.Fatal(err)
		}
		for _, level := range []int{gzip.DefaultCompression, gzip.BestSpeed} {
			b.Run(strings.ReplaceAll(humanBytes(len(raw)), " ", "")+levelName(level), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(raw)))
				for i := 0; i < b.N; i++ {
					var buf bytes.Buffer
					w, err := gzip.NewWriterLevel(&buf, level)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := w.Write(raw); err != nil {
						b.Fatal(err)
					}
					if err := w.Close(); err != nil {
						b.Fatal(err)
					}
					if buf.Len() == 0 {
						b.Fatal("empty gzip output")
					}
				}
			})
		}
	}
}

// BenchmarkAnalyzeMessages measures the per-request message-walk cost.
func BenchmarkAnalyzeMessages(b *testing.B) {
	for _, size := range []int{2 * 1024, 100 * 1024} {
		msgs := sizedPayload(size)
		b.Run(strings.ReplaceAll(humanBytes(size), " ", ""), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = AnalyzeMessages(msgs)
			}
		})
	}
}

func humanBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%dMB", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%dKB", n/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func levelName(level int) string {
	if level == gzip.BestSpeed {
		return "-bestspeed"
	}
	return "-default"
}


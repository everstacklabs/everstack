package memory

import (
	"testing"
	"time"
)

func TestSelectInterval(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"30 minutes → minute", 30 * time.Minute, "minute"},
		{"exactly 2h → minute", 2 * time.Hour, "minute"},
		{"3h → hour", 3 * time.Hour, "hour"},
		{"24h → hour", 24 * time.Hour, "hour"},
		{"48h → hour", 48 * time.Hour, "hour"},
		{"49h → day", 49 * time.Hour, "day"},
		{"7 days → day", 7 * 24 * time.Hour, "day"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectInterval(tt.duration)
			if got != tt.want {
				t.Fatalf("selectInterval(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestSortBuckets_AlreadySorted(t *testing.T) {
	now := time.Now()
	buckets := []AnalyticsBucket{
		{Timestamp: now.Add(-2 * time.Hour)},
		{Timestamp: now.Add(-1 * time.Hour)},
		{Timestamp: now},
	}
	sortBuckets(buckets)

	for i := 1; i < len(buckets); i++ {
		if buckets[i].Timestamp.Before(buckets[i-1].Timestamp) {
			t.Fatalf("bucket %d is before bucket %d after sorting", i, i-1)
		}
	}
}

func TestSortBuckets_Reverse(t *testing.T) {
	now := time.Now()
	buckets := []AnalyticsBucket{
		{Timestamp: now},
		{Timestamp: now.Add(-1 * time.Hour)},
		{Timestamp: now.Add(-2 * time.Hour)},
	}
	sortBuckets(buckets)

	for i := 1; i < len(buckets); i++ {
		if buckets[i].Timestamp.Before(buckets[i-1].Timestamp) {
			t.Fatalf("bucket %d is before bucket %d after sorting", i, i-1)
		}
	}
}

func TestSortBuckets_Single(t *testing.T) {
	buckets := []AnalyticsBucket{
		{Timestamp: time.Now()},
	}
	sortBuckets(buckets) // Should not panic
	if len(buckets) != 1 {
		t.Fatal("expected 1 bucket")
	}
}

func TestSortBuckets_Empty(t *testing.T) {
	var buckets []AnalyticsBucket
	sortBuckets(buckets) // Should not panic
	if len(buckets) != 0 {
		t.Fatal("expected 0 buckets")
	}
}

func TestSortBuckets_DuplicateTimestamps(t *testing.T) {
	now := time.Now()
	buckets := []AnalyticsBucket{
		{Timestamp: now, QueryCount: 1},
		{Timestamp: now, QueryCount: 2},
		{Timestamp: now.Add(-1 * time.Hour), QueryCount: 3},
	}
	sortBuckets(buckets)

	// First bucket should be the earliest
	if !buckets[0].Timestamp.Before(buckets[1].Timestamp) && buckets[0].Timestamp != buckets[1].Timestamp {
		t.Fatal("sorting failed with duplicate timestamps")
	}
	// The earliest timestamp should come first
	if buckets[0].QueryCount != 3 {
		t.Fatalf("expected earliest bucket first, got QueryCount=%d", buckets[0].QueryCount)
	}
}

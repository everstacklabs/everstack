package utils

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// ParseTimestamp converts a string timestamp to protobuf timestamp
func ParseTimestamp(ts string) *timestamppb.Timestamp {
	if ts == "" {
		return nil
	}

	// Try RFC3339 format first (ISO 8601)
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return timestamppb.New(t)
	}

	// Try RFC3339Nano format (with nanoseconds)
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return timestamppb.New(t)
	}

	// Try PostgreSQL timestamp format: "2006-01-02 15:04:05.999999 -07:00"
	if t, err := time.Parse("2006-01-02 15:04:05.999999 -07:00", ts); err == nil {
		return timestamppb.New(t)
	}

	// Try PostgreSQL timestamp without microseconds: "2006-01-02 15:04:05 -07:00"
	if t, err := time.Parse("2006-01-02 15:04:05 -07:00", ts); err == nil {
		return timestamppb.New(t)
	}

	// Try parsing without timezone
	if t, err := time.Parse("2006-01-02 15:04:05.999999", ts); err == nil {
		return timestamppb.New(t)
	}

	return nil
}

// ParseTimestampProto is an alias for ParseTimestamp.
func ParseTimestampProto(ts string) *timestamppb.Timestamp {
	return ParseTimestamp(ts)
}

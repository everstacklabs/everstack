package contextkeys

import (
	"context"
	"net/http"
	"testing"
)

func TestAgentHopParsing(t *testing.T) {
	for _, tc := range []struct {
		values []string
		want   int
	}{{nil, 0}, {[]string{"0"}, 0}, {[]string{"1"}, 1}, {[]string{"16"}, 16}, {[]string{"17"}, 16}, {[]string{"-1"}, 16}, {[]string{""}, 16}, {[]string{"x"}, 16}, {[]string{"1", "0"}, 16}} {
		h := http.Header{}
		for _, v := range tc.values {
			h.Add(AgentHopHeader, v)
		}
		if got := AgentHop(ReadAgentHop(context.Background(), h)); got != tc.want {
			t.Fatalf("%v => %d want %d", tc.values, got, tc.want)
		}
	}
}

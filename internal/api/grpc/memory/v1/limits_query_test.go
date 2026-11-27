package v1

import "testing"

func TestClampTopK(t *testing.T) {
	cases := map[int]int{-3: 5, 0: 5, 1: 1, 50: 50, maxQueryTopK: maxQueryTopK, maxQueryTopK + 1: maxQueryTopK, 1 << 30: maxQueryTopK}
	for in, want := range cases {
		if got := clampTopK(in); got != want {
			t.Errorf("clampTopK(%d) = %d, want %d", in, got, want)
		}
	}
}

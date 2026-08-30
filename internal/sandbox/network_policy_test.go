package sandbox

import (
	"errors"
	"fmt"
	"testing"
)

func TestNormalizeNetworkAllowCIDRs(t *testing.T) {
	t.Parallel()

	got, err := NormalizeNetworkAllowCIDRs([]string{" 10.0.0.1/8 ", "192.168.1.2/24", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NormalizeNetworkAllowCIDRs: %v", err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.0/24"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNormalizeNetworkAllowCIDRsRejectsInvalid(t *testing.T) {
	t.Parallel()

	for _, cidrs := range [][]string{
		{""},
		{"not-a-cidr"},
		{"10.0.0.1"},
		{"2001:db8::/32"},
	} {
		if _, err := NormalizeNetworkAllowCIDRs(cidrs); !errors.Is(err, ErrNetworkAllowCIDRsInvalid) {
			t.Fatalf("NormalizeNetworkAllowCIDRs(%v) error = %v, want ErrNetworkAllowCIDRsInvalid", cidrs, err)
		}
	}
}

func TestNormalizeNetworkAllowCIDRsLimit(t *testing.T) {
	t.Parallel()

	cidrs := make([]string, 0, MaxNetworkAllowCIDRs+1)
	for i := 0; i < MaxNetworkAllowCIDRs+1; i++ {
		cidrs = append(cidrs, fmt.Sprintf("10.%d.0.0/16", i))
	}
	if _, err := NormalizeNetworkAllowCIDRs(cidrs); !errors.Is(err, ErrNetworkAllowCIDRsInvalid) {
		t.Fatalf("expected limit error, got %v", err)
	}
}

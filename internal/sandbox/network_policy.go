package sandbox

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

const MaxNetworkAllowCIDRs = 10

var ErrNetworkAllowCIDRsInvalid = errors.New("network_allow_cidrs is invalid")

func NormalizeNetworkAllowCIDRs(cidrs []string) ([]string, error) {
	if len(cidrs) == 0 {
		return nil, nil
	}
	if len(cidrs) > MaxNetworkAllowCIDRs {
		return nil, fmt.Errorf("%w: maximum of %d entries exceeded", ErrNetworkAllowCIDRsInvalid, MaxNetworkAllowCIDRs)
	}

	seen := make(map[string]struct{}, len(cidrs))
	out := make([]string, 0, len(cidrs))
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("%w: empty CIDR", ErrNetworkAllowCIDRsInvalid)
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not a CIDR", ErrNetworkAllowCIDRsInvalid, raw)
		}
		if !prefix.Addr().Is4() {
			return nil, fmt.Errorf("%w: %q is not an IPv4 CIDR", ErrNetworkAllowCIDRsInvalid, raw)
		}
		canonical := prefix.Masked().String()
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out, nil
}

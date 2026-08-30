package firecracker

import (
	"errors"
	"sync"
	"testing"
)

// resetSubnetAllocator wipes the global allocator between tests so
// state from one case can't leak into another. Lives only in test
// code because production never wants to reset mid-run.
func resetSubnetAllocator() {
	subnetAllocMu.Lock()
	subnetInUse = map[int]struct{}{}
	subnetCursor = 0
	subnetAllocMu.Unlock()
}

func TestAllocateSubnet_ReleaseRoundtrip(t *testing.T) {
	resetSubnetAllocator()

	first, err := AllocateSubnet()
	if err != nil {
		t.Fatalf("first allocate: %v", err)
	}
	second, err := AllocateSubnet()
	if err != nil {
		t.Fatalf("second allocate: %v", err)
	}
	if first == second {
		t.Fatalf("allocator returned duplicate subnet %d", first)
	}

	ReleaseSubnet(first)
	third, err := AllocateSubnet()
	if err != nil {
		t.Fatalf("third allocate: %v", err)
	}
	if third == second {
		t.Fatalf("post-release allocation %d collides with still-held %d", third, second)
	}
}

func TestAllocateSubnet_Exhaustion(t *testing.T) {
	resetSubnetAllocator()

	// Fill the pool.
	for i := 0; i < 254; i++ {
		if _, err := AllocateSubnet(); err != nil {
			t.Fatalf("unexpected error at iteration %d: %v", i, err)
		}
	}
	if _, err := AllocateSubnet(); !errors.Is(err, ErrSubnetsExhausted) {
		t.Fatalf("expected ErrSubnetsExhausted, got %v", err)
	}
}

func TestReserveSubnet_DetectsCollision(t *testing.T) {
	resetSubnetAllocator()

	if err := ReserveSubnet(42); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := ReserveSubnet(42); err == nil {
		t.Fatalf("expected error reserving already-claimed subnet, got nil")
	}
}

func TestSubnetFromHostIP(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"10.0.42.1", 42, false},
		{"10.0.1.1", 1, false},
		{"10.0.254.1", 254, false},
		{"10.1.0.1", 0, true},
		{"192.168.1.1", 0, true},
		{"not-an-ip", 0, true},
	}
	for _, tc := range cases {
		got, err := subnetFromHostIP(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %d", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestParseAssignedSubnets guards the host-scan parser that fixes the
// cross-DaemonSet subnet collision (dev + prod fcagent on one node both
// picking 10.0.1.0/30 → second VM's DNS proxy fails to bind 10.0.1.1:53).
func TestParseAssignedSubnets(t *testing.T) {
	// Real `ip -4 -o addr show` shape from the node.
	out := "" +
		"1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever preferred_lft forever\n" +
		"2: ens3    inet 203.0.113.10/24 scope global ens3\\       valid_lft forever\n" +
		"366: tap-f3a110d902a    inet 10.0.1.1/30 scope global tap-f3a110d902a\\       valid_lft forever\n" +
		"401: tap-abc123    inet 10.0.7.1/30 scope global tap-abc123\\       valid_lft forever\n"
	got := parseAssignedSubnets(out)
	if len(got) != 2 {
		t.Fatalf("expected 2 assigned subnets, got %d (%v)", len(got), got)
	}
	for _, want := range []int{1, 7} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing expected subnet %d", want)
		}
	}
	// Must NOT match the host ens3 IP, loopback, or a guest .2 address.
	if _, ok := got[59]; ok {
		t.Errorf("wrongly matched non-tap address (37.59.x)")
	}
	// Guest addresses (.2) must not be counted.
	guest := parseAssignedSubnets("9: tap-x    inet 10.0.3.2/30 scope global tap-x\n")
	if len(guest) != 0 {
		t.Errorf("guest .2 address must not count as an assigned host subnet: %v", guest)
	}
}

// TestAllocateSubnet_SkipsHostTaken reproduces the exact prod-vs-dev
// collision: dev holds 10.0.1.0/30 on the shared host, so prod's
// allocator must skip n=1 instead of colliding.
func TestAllocateSubnet_SkipsHostTaken(t *testing.T) {
	resetSubnetAllocator()
	// Simulate the peer DaemonSet already holding subnet 1 on the host.
	subnetAllocMu.Lock()
	got, err := allocateLocked(map[int]struct{}{1: {}})
	subnetAllocMu.Unlock()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if got == 1 {
		t.Fatalf("allocator picked host-taken subnet 1 (would collide with peer DaemonSet)")
	}
	if got != 2 {
		t.Fatalf("expected next free subnet 2, got %d", got)
	}
}

func TestAllocateSubnet_ConcurrentNoDuplicates(t *testing.T) {
	resetSubnetAllocator()

	var wg sync.WaitGroup
	const n = 50
	results := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s, err := AllocateSubnet()
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				return
			}
			results[idx] = s
		}(i)
	}
	wg.Wait()

	seen := map[int]struct{}{}
	for _, s := range results {
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate subnet %d under concurrent allocate", s)
		}
		seen[s] = struct{}{}
	}
}

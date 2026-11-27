package main

import "testing"

func TestResolveListenAddr(t *testing.T) {
	cases := []struct {
		name         string
		addr, nodeIP string
		want         string
		wantWildcard bool
	}{
		{"port only pins to node ip", ":9090", "192.0.2.10", "192.0.2.10:9090", false},
		{"ipv4 wildcard pins to node ip", "0.0.0.0:9090", "192.0.2.10", "192.0.2.10:9090", false},
		{"ipv6 wildcard pins to node ip", "[::]:9090", "192.0.2.10", "192.0.2.10:9090", false},
		{"ipv6 node ip is bracketed", ":9090", "2001:db8::1", "[2001:db8::1]:9090", false},
		{"explicit host is kept", "127.0.0.1:9090", "192.0.2.10", "127.0.0.1:9090", false},
		{"explicit other host is kept", "10.1.1.1:9090", "192.0.2.10", "10.1.1.1:9090", false},
		{"no node ip stays wildcard", ":9090", "", ":9090", true},
		{"no node ip ipv4 wildcard stays wildcard", "0.0.0.0:9090", "", "0.0.0.0:9090", true},
		{"whitespace node ip is ignored", ":9090", "  ", ":9090", true},
		{"unparseable addr is returned as is", "garbage", "192.0.2.10", "garbage", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, wildcard := resolveListenAddr(tc.addr, tc.nodeIP)
			if got != tc.want || wildcard != tc.wantWildcard {
				t.Fatalf("resolveListenAddr(%q, %q) = (%q, %v), want (%q, %v)",
					tc.addr, tc.nodeIP, got, wildcard, tc.want, tc.wantWildcard)
			}
		})
	}
}

func TestListenPorts(t *testing.T) {
	got := listenPorts("192.0.2.10:9090", ":9091", "garbage", "host:notaport")
	if len(got) != 2 || got[0] != 9090 || got[1] != 9091 {
		t.Fatalf("listenPorts = %v, want [9090 9091]", got)
	}
}

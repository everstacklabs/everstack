package egress

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startDNS serves handler on addr over net ("udp" or "tcp").
func startDNS(t *testing.T, network string, handler dns.HandlerFunc) string {
	t.Helper()
	srv := &dns.Server{Net: network, Handler: handler}
	switch network {
	case "tcp":
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv.Listener = l
	case "udp":
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv.PacketConn = pc
	}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	if srv.Listener != nil {
		return srv.Listener.Addr().String()
	}
	return srv.PacketConn.LocalAddr().String()
}

func answer(rcode int, truncated bool) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetRcode(r, rcode)
		m.Truncated = truncated
		_ = w.WriteMsg(m)
	}
}

func query() *dns.Msg {
	q := new(dns.Msg)
	q.SetQuestion("everstack.ai.", dns.TypeA)
	return q
}

// The prod OVH node on 2026-10-16: UDP 53 to the upstream is dead, TCP
// works. The proxy must still answer instead of returning SERVFAIL.
func TestExchangeUpstream_FallsBackToTCPWhenUDPFails(t *testing.T) {
	addr := startDNS(t, "tcp", answer(dns.RcodeSuccess, false)) // nothing on UDP at this port
	resp, transport, err := ExchangeUpstream(query(), addr)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if transport != "tcp" || resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("got transport=%q rcode=%d, want tcp/NOERROR", transport, resp.Rcode)
	}
}

func TestExchangeUpstream_PrefersUDP(t *testing.T) {
	addr := startDNS(t, "udp", answer(dns.RcodeNameError, false))
	resp, transport, err := ExchangeUpstream(query(), addr)
	if err != nil || transport != "udp" || resp.Rcode != dns.RcodeNameError {
		t.Fatalf("got (%v, %q, %v), want the UDP NXDOMAIN answer", resp, transport, err)
	}
}

// A truncated UDP answer is retried over TCP; if TCP is unreachable the
// truncated answer is returned rather than nothing.
func TestExchangeUpstream_TruncatedUDP(t *testing.T) {
	addr := startDNS(t, "udp", answer(dns.RcodeSuccess, true))
	resp, transport, err := ExchangeUpstream(query(), addr)
	if err != nil || transport != "udp" || !resp.Truncated {
		t.Fatalf("no TCP listener: got (%v, %q, %v), want the truncated UDP answer", resp, transport, err)
	}
}

func TestExchangeUpstream_BothFail(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listening on either transport
	if _, _, err := ExchangeUpstream(query(), addr); err == nil {
		t.Fatal("want an error when neither transport answers")
	}
}

// After a UDP failure the upstream goes straight to TCP, so a node that
// cannot use UDP 53 pays the UDP timeout once a minute, not per query.
func TestExchangeUpstream_RemembersDeadUDP(t *testing.T) {
	addr := startDNS(t, "tcp", answer(dns.RcodeSuccess, false))
	t.Cleanup(func() { udpDeadUntil.Delete(addr) })
	if _, _, err := ExchangeUpstream(query(), addr); err != nil {
		t.Fatal(err)
	}
	if !udpLooksDead(addr) {
		t.Fatal("a failed UDP exchange must mark the upstream's UDP dead")
	}
	_, transport, err := ExchangeUpstream(query(), addr)
	if err != nil || transport != "tcp" {
		t.Fatalf("got (%q, %v), want a TCP answer", transport, err)
	}
}

func TestExchangeUpstream_UDPSuccessClearsDeadMark(t *testing.T) {
	addr := startDNS(t, "udp", answer(dns.RcodeSuccess, false))
	t.Cleanup(func() { udpDeadUntil.Delete(addr) })
	udpDeadUntil.Store(addr, time.Now().Add(-time.Second)) // expired mark
	if _, transport, err := ExchangeUpstream(query(), addr); err != nil || transport != "udp" {
		t.Fatalf("got (%q, %v), want UDP once the mark expired", transport, err)
	}
	if _, ok := udpDeadUntil.Load(addr); ok {
		t.Fatal("a UDP success must clear the dead mark")
	}
}

package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/ssh"
)

type fakeConnMeta struct {
	ssh.ConnMetadata
	user string
}

func (f fakeConnMeta) User() string { return f.user }
func (f fakeConnMeta) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 50000}
}

func newPausedTestProxy(t *testing.T, paused ...string) (*Proxy, sqlmock.Sqlmock, *[]string) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	p := NewProxy(ProxyConfig{KeyStore: NewKeyStore(sqlx.NewDb(raw, "sqlmock"))})
	set := map[string]bool{}
	for _, id := range paused {
		set[id] = true
	}
	var asked []string
	p.SetTenantActiveFn(func(_ context.Context, tenantID string) bool {
		asked = append(asked, tenantID)
		return !set[tenantID]
	})
	return p, mock, &asked
}

func testPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func expectKeyLookup(mock sqlmock.Sqlmock, tenantID string) {
	mock.ExpectQuery(`FROM user_ssh_keys`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "tenant_id", "name", "public_key", "fingerprint", "key_type", "last_used_at", "created_at",
		}).AddRow(int64(1), "user-1", tenantID, "laptop", "ssh-ed25519 AAAA", "SHA256:x", "ssh-ed25519", nil, "2026-09-30"))
}

func TestPublicKeyAuthDeniedForPausedInstance(t *testing.T) {
	p, mock, asked := newPausedTestProxy(t, "inst-paused")
	expectKeyLookup(mock, "inst-paused")

	_, err := p.publicKeyCallback(fakeConnMeta{user: "sbx-1"}, testPublicKey(t))
	if !errors.Is(err, errInstancePaused) {
		t.Fatalf("err = %v, want errInstancePaused", err)
	}
	if len(*asked) == 0 || (*asked)[0] != "inst-paused" {
		t.Fatalf("gate asked %v", *asked)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicKeyAuthPassesGateForActiveInstance(t *testing.T) {
	p, mock, asked := newPausedTestProxy(t, "inst-other")
	expectKeyLookup(mock, "inst-active")

	// No sandbox manager is configured, so the lookup after the gate fails
	// with the generic error: the active tenant got past the pause check.
	_, err := p.publicKeyCallback(fakeConnMeta{user: "sbx-1"}, testPublicKey(t))
	if err == nil || errors.Is(err, errInstancePaused) {
		t.Fatalf("err = %v, want generic auth failure", err)
	}
	if len(*asked) != 1 || (*asked)[0] != "inst-active" {
		t.Fatalf("gate asked %v", *asked)
	}
}

func expectTokenLookup(mock sqlmock.Sqlmock) {
	now := time.Now()
	mock.ExpectQuery(`FROM sandbox_ssh_tokens`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "tenant_id", "instance_id", "sandbox_id", "token_prefix",
			"created_by", "expires_at", "revoked_at", "last_used_at", "last_used_ip", "created_at",
		}).AddRow("sshtok_1", "org-1", "tenant-1", "inst-1", "sbx-1", "ssht_abc",
			"user-1", now.Add(time.Hour), nil, nil, nil, now))
}

func TestTokenAuthDeniedForPausedInstance(t *testing.T) {
	// Either id being paused denies: in cloud the instance id is the owner.
	for _, paused := range []string{"tenant-1", "inst-1"} {
		t.Run(paused, func(t *testing.T) {
			p, mock, _ := newPausedTestProxy(t, paused)
			expectTokenLookup(mock)
			_, err := p.tokenPermissions(context.Background(), fakeConnMeta{user: "sbx-1"}, "ssht_rawtoken", "")
			if !errors.Is(err, errInstancePaused) {
				t.Fatalf("err = %v, want errInstancePaused", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTokenAuthPassesGateForActiveInstance(t *testing.T) {
	p, mock, asked := newPausedTestProxy(t, "someone-else")
	expectTokenLookup(mock)
	_, err := p.tokenPermissions(context.Background(), fakeConnMeta{user: "sbx-1"}, "ssht_rawtoken", "")
	if err == nil || errors.Is(err, errInstancePaused) {
		t.Fatalf("err = %v, want generic auth failure past the gate", err)
	}
	if len(*asked) != 2 {
		t.Fatalf("gate asked %v, want tenant and instance ids", *asked)
	}
}

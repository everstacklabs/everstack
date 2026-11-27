package v1

import (
	"regexp"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	sshpkg "github.com/everstacklabs/everstack/internal/ssh"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

func TestDeleteSSHKeyScopesDeletionToTenant(t *testing.T) {
	query := regexp.QuoteMeta(`
		DELETE FROM user_ssh_keys
		WHERE id = $1 AND tenant_id = $2`)

	t.Run("foreign key is not found", func(t *testing.T) {
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer rawDB.Close()
		s := &Server{sshKeyStore: sshpkg.NewKeyStore(sqlx.NewDb(rawDB, "sqlmock"))}
		mock.ExpectExec(query).
			WithArgs(int64(7), "tenant-b").
			WillReturnResult(sqlmock.NewResult(0, 0))

		_, err = s.DeleteSSHKey(sessionPrincipal("tenant-b"), connect.NewRequest(&agentspb.DeleteSSHKeyRequest{KeyId: 7}))
		wantNotFound(t, "DeleteSSHKey", err)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("SQL expectations: %v", err)
		}
	})

	t.Run("owner deletes the key", func(t *testing.T) {
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer rawDB.Close()
		s := &Server{sshKeyStore: sshpkg.NewKeyStore(sqlx.NewDb(rawDB, "sqlmock"))}
		mock.ExpectExec(query).
			WithArgs(int64(7), ownerTenant).
			WillReturnResult(sqlmock.NewResult(0, 1))

		resp, err := s.DeleteSSHKey(sessionPrincipal(ownerTenant), connect.NewRequest(&agentspb.DeleteSSHKeyRequest{KeyId: 7}))
		if err != nil {
			t.Fatalf("owner DeleteSSHKey: %v", err)
		}
		if !resp.Msg.GetSuccess() {
			t.Fatal("owner DeleteSSHKey success = false")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("SQL expectations: %v", err)
		}
	})
}

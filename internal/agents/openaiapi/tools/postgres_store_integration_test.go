package tools

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

func TestPostgresStoreClaimsGrantsAndApprovals(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN is not set")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := "test_openai_tools_" + regexp.MustCompile(`-`).ReplaceAllString(uuid.NewString(), "")
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	if _, err = db.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, path := range []string{
		"../../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql",
		"../../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql",
		"../../../database/migrations/sql/postgres/openai_agent_tools_20260911010000/up.sql",
		"../../../database/migrations/sql/postgres/openai_agents_usage_20260911160000/up.sql",
	} {
		migration, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatalf("apply production migration %s: %v", path, err)
		}
	}

	ctx := context.Background()
	owner := Owner{OrganizationID: uuid.NewString(), InstanceID: uuid.NewString(), PrincipalID: "user"}
	sid := uuid.NewString()
	connectionID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO openai_agent_connections
 (id,organization_id,instance_id,name,provider,credential_ref)
 VALUES ($1,$2,$3,'test','openai_agents','encrypted-test-fixture')`, connectionID, owner.OrganizationID, owner.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO openai_agent_sessions
 (id,organization_id,instance_id,provider,connection_id,model,environment_type,status,readiness,cleanup_status,idempotency_key,request_fingerprint,created_by)
 VALUES ($1,$2,$3,'openai_agents',$4,'test-model','none','running','pending','active',$5,'fingerprint',$6)`, sid, owner.OrganizationID, owner.InstanceID, connectionID, uuid.NewString(), owner.PrincipalID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO openai_agent_tool_calls (organization_id,instance_id,session_id,turn_id,call_id,tool_name,status,result_json) VALUES ($1,$2,$3,'legacy-turn','legacy-call','legacy-tool','completed','{}')`, owner.OrganizationID, owner.InstanceID, sid); err != nil {
		t.Fatal(err)
	}
	deliveryMigration, err := os.ReadFile("../../../database/migrations/sql/postgres/openai_agent_tool_result_delivery_20260912000000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(deliveryMigration)); err != nil {
		t.Fatal(err)
	}
	var deliveryStatus string
	if err = db.Get(&deliveryStatus, `SELECT delivery_status FROM openai_agent_tool_calls WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id='legacy-turn' AND call_id='legacy-call'`, owner.OrganizationID, owner.InstanceID, sid); err != nil || deliveryStatus != "uncertain" {
		t.Fatalf("legacy delivery status=%q err=%v", deliveryStatus, err)
	}
	store, _ := NewPostgresStore(db, time.Minute)
	grant := Grant{Kind: "function", ResourceID: "fn", Name: "run", UpstreamName: "function__run"}
	if err = store.BindSession(ctx, owner, sid, []Grant{grant}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GrantForCall(ctx, owner, sid, grant.UpstreamName); err != nil {
		t.Fatal(err)
	}
	foreign := owner
	foreign.InstanceID = uuid.NewString()
	if _, err = store.GrantForCall(ctx, foreign, sid, grant.UpstreamName); err == nil {
		t.Fatal("foreign owner read grant")
	}
	in := Invocation{Owner: owner, SessionID: sid, TurnID: "turn", CallID: "call", Name: grant.UpstreamName}
	if prior, acquired, err := store.BeginCall(ctx, owner, in); err != nil || !acquired || prior != nil {
		t.Fatalf("first claim prior=%v acquired=%v err=%v", prior, acquired, err)
	}
	if prior, acquired, err := store.BeginCall(ctx, owner, in); err != nil || acquired || prior != nil {
		t.Fatalf("duplicate claim prior=%v acquired=%v err=%v", prior, acquired, err)
	}
	want := Result{TurnID: "turn", CallID: "call", Success: true, Output: `{"ok":true}`, State: "completed"}
	if err = store.CompleteCall(ctx, owner, in, want); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&deliveryStatus, `SELECT delivery_status FROM openai_agent_tool_calls WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5`, callKey(owner, in)...); err != nil || deliveryStatus != "pending" {
		t.Fatalf("new delivery status=%q err=%v", deliveryStatus, err)
	}
	prior, acquired, err := store.BeginCall(ctx, owner, in)
	if err != nil || acquired || prior == nil || prior.Output != want.Output {
		t.Fatalf("saved result=%v acquired=%v err=%v", prior, acquired, err)
	}
	claims := make(chan bool, 2)
	db2, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if _, err = db2.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	db2.SetMaxOpenConns(1)
	storeB, _ := NewPostgresStore(db2, time.Minute)
	for i, token := range []string{"holder-a", "holder-b"} {
		claimStore := store
		if i == 1 {
			claimStore = storeB
		}
		go func(token string, claimStore *PostgresStore) {
			claimed, _, claimErr := claimStore.ClaimDelivery(ctx, owner, in, token, time.Minute)
			if claimErr != nil {
				t.Errorf("claim %s: %v", token, claimErr)
			}
			claims <- claimed
		}(token, claimStore)
	}
	claimedCount := 0
	for range 2 {
		if <-claims {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("concurrent delivery claims=%d, want 1", claimedCount)
	}
	var firstToken string
	if err = db.Get(&firstToken, `SELECT delivery_token FROM openai_agent_tool_calls WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5`, callKey(owner, in)...); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE openai_agent_tool_calls SET delivery_claimed_until=NOW()-INTERVAL '1 second' WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5`, callKey(owner, in)...); err != nil {
		t.Fatal(err)
	}
	claimed, recovery, err := store.ClaimDelivery(ctx, owner, in, "holder-new", time.Minute)
	if err != nil || !claimed || recovery != "expired" {
		t.Fatalf("takeover claimed=%v recovery=%q err=%v", claimed, recovery, err)
	}
	if err = store.CompleteDelivery(ctx, owner, in, firstToken); err == nil {
		t.Fatal("stale holder completed replacement claim")
	}
	if err = store.CompleteDelivery(ctx, owner, in, "holder-new"); err != nil {
		t.Fatal(err)
	}

	gate, _ := NewPostgresApprovalGate(db, time.Minute)
	approvalIn := Invocation{Owner: owner, SessionID: sid, TurnID: "turn2", CallID: "call2", Name: "sensitive", Arguments: json.RawMessage(`{"x":1}`)}
	if decision, err := gate.Check(ctx, owner, grant, approvalIn); err != nil || decision != Pending {
		t.Fatalf("decision=%s err=%v", decision, err)
	}
	reviews, err := gate.ListPending(ctx, owner)
	if err != nil || len(reviews) != 1 {
		t.Fatalf("reviews=%v err=%v", reviews, err)
	}
	if err = gate.Resolve(ctx, owner, reviews[0].ID, Approved); err != nil {
		t.Fatal(err)
	}
	if decision, err := gate.Check(ctx, owner, grant, approvalIn); err != nil || decision != Approved {
		t.Fatalf("decision=%s err=%v", decision, err)
	}
}

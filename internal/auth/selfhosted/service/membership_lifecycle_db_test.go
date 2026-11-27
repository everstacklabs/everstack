package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
)

// Membership lifecycle on a self-hosted gateway: who may become an owner,
// and when. Real Postgres; requires EVS_SESSION_TEST_DSN.

const lifecyclePassword = "Correct-Horse-Battery-9"

type lifecycleEnv struct {
	*sessionlivetest.Env
	auth *SelfHostedAuthService
	orgs *OrganizationService
}

// newLifecycleEnv returns a fresh self-hosted database with no users and no
// organizations, as a newly installed instance has.
func newLifecycleEnv(t *testing.T) *lifecycleEnv {
	t.Helper()
	e := sessionlivetest.NewCE(t)
	// The organization columns the repository reads arrived in a later
	// migration than the one NewCE applies.
	ddl, err := os.ReadFile("../../../database/migrations/sql/postgres/organizations_billing_columns_20260501234812/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(t, `SET search_path TO everstack; `+string(ddl))
	e.Exec(t, `DELETE FROM everstack.users`)
	cfg := &domain.InternalConfig{Session: domain.SessionConfig{CookieName: "everstack_session", MaxAge: time.Hour}}
	orgRepo := repository.NewOrganizationRepository(e.DB)
	auth := NewSelfHostedAuthService(cfg,
		repository.NewUserRepository(e.DB),
		repository.NewSessionRepository(e.DB),
		repository.NewCredentialsRepository(e.DB),
		repository.NewInvitationRepository(e.DB),
		repository.NewMagicLinkRepository(e.DB),
		orgRepo,
		nil,
	)
	return &lifecycleEnv{Env: e, auth: auth, orgs: NewOrganizationService(orgRepo, nil)}
}

// addPasswordUser creates a user who can sign in with lifecyclePassword and
// belongs to no organization.
func (e *lifecycleEnv) addPasswordUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.Exec(t, `INSERT INTO everstack.users (id, external_id, email) VALUES ($1, $2, $3)`, id, "local:"+id.String(), email)
	hash, err := HashPassword(lifecyclePassword)
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(t, `INSERT INTO everstack.user_credentials (user_id, password_hash) VALUES ($1, $2)`, id, hash)
	return id
}

func (e *lifecycleEnv) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.DB.Get(&n, q, args...); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func (e *lifecycleEnv) owners(t *testing.T, orgID uuid.UUID) int {
	t.Helper()
	return e.count(t, `SELECT COUNT(*) FROM everstack.organization_members WHERE organization_id = $1 AND role = 'owner'`, orgID)
}

func (e *lifecycleEnv) memberships(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	return e.count(t, `SELECT COUNT(*) FROM everstack.organization_members WHERE user_id = $1`, userID)
}

func (e *lifecycleEnv) sessions(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	return e.count(t, `SELECT COUNT(*) FROM everstack.sessions WHERE user_id = $1`, userID)
}

// registerOwner runs first-run registration and returns the owner and the
// organization it created.
func (e *lifecycleEnv) registerOwner(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	u, _, err := e.auth.Register(context.Background(), "owner@example.test", lifecyclePassword, nil, nil, nil)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(u.Organizations) != 1 || u.Organizations[0].Role != domain.RoleOwner {
		t.Fatalf("Register organizations = %+v, want one owner membership", u.Organizations)
	}
	return u.User.ID, u.Organizations[0].ID
}

// A member removed from the instance's organization keeps their password.
// Signing in again must not put them back into any organization, let alone
// as an owner.
func TestRemovedMemberSigningInAgainGetsNoMembership(t *testing.T) {
	ctx := context.Background()
	for name, remove := range map[string]func(e *lifecycleEnv, ownerID, orgID, memberID uuid.UUID) error{
		"team removal": func(e *lifecycleEnv, ownerID, orgID, memberID uuid.UUID) error {
			return e.auth.RemoveTeamMember(ctx, ownerID, memberID, []uuid.UUID{orgID})
		},
		"organization removal": func(e *lifecycleEnv, ownerID, orgID, memberID uuid.UUID) error {
			return e.orgs.RemoveMember(ctx, ownerID, orgID, memberID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLifecycleEnv(t)
			ownerID, orgID := e.registerOwner(t)
			memberID := e.addPasswordUser(t, "member@example.test")
			if err := e.orgs.orgRepo.AddMemberSimple(ctx, orgID, memberID, domain.RoleMember); err != nil {
				t.Fatal(err)
			}
			if _, _, err := e.auth.Login(ctx, "member@example.test", lifecyclePassword, nil, nil); err != nil {
				t.Fatalf("member login: %v", err)
			}

			if err := remove(e, ownerID, orgID, memberID); err != nil {
				t.Fatalf("remove: %v", err)
			}
			// Removal ends every session the member held, so nothing they
			// already have keeps working.
			if n := e.sessions(t, memberID); n != 0 {
				t.Errorf("removed member still has %d session(s)", n)
				e.Exec(t, `DELETE FROM everstack.sessions WHERE user_id = $1`, memberID)
			}

			// The password still checks out, but there is nothing to sign in
			// to: the login is refused and no session is minted.
			u, session, err := e.auth.Login(ctx, "member@example.test", lifecyclePassword, nil, nil)
			if !errors.Is(err, ErrNoOrganization) {
				t.Fatalf("login after removal: user=%+v err=%v, want ErrNoOrganization", u, err)
			}
			if session != nil || e.sessions(t, memberID) != 0 {
				t.Fatalf("login after removal minted a session")
			}
			if n := e.memberships(t, memberID); n != 0 {
				t.Fatalf("login after removal created %d membership(s)", n)
			}
			if n := e.owners(t, orgID); n != 1 {
				t.Fatalf("owners = %d, want the original 1", n)
			}
		})
	}
}

// An instance whose only user has no organization (registration that failed
// to create one) bootstraps it on sign-in. Concurrent sign-ins by that user
// create exactly one organization, and every one of them succeeds.
func TestFirstRunBootstrapMakesExactlyOneOwner(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	e.addPasswordUser(t, "first@example.test")
	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = e.auth.Login(ctx, "first@example.test", lifecyclePassword, nil, nil)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("login %d: %v", i, err)
		}
	}
	if got := e.count(t, `SELECT COUNT(*) FROM everstack.organizations`); got != 1 {
		t.Fatalf("organizations = %d, want 1", got)
	}
	if got := e.count(t, `SELECT COUNT(*) FROM everstack.organization_members WHERE role = 'owner'`); got != 1 {
		t.Fatalf("owners = %d, want 1", got)
	}
}

// "No organization" is not "fresh instance". Once an instance has had more
// than one user, losing every organization must not hand ownership to
// whoever signs in first, least of all its earliest (long-removed) user.
func TestNoBootstrapOnceTheInstanceHasOtherUsers(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	e.registerOwner(t)
	early := e.addPasswordUser(t, "early@example.test")
	e.Exec(t, `UPDATE everstack.users SET created_at = '2000-01-01' WHERE id = $1`, early)
	e.Exec(t, `DELETE FROM everstack.organizations`)

	for _, email := range []string{"early@example.test", "owner@example.test"} {
		if _, _, err := e.auth.Login(ctx, email, lifecyclePassword, nil, nil); !errors.Is(err, ErrNoOrganization) {
			t.Fatalf("%s: err = %v, want ErrNoOrganization", email, err)
		}
	}
	if got := e.count(t, `SELECT COUNT(*) FROM everstack.organizations`); got != 0 {
		t.Fatalf("sign-in bootstrapped %d organization(s) on a used instance", got)
	}
}

// Registration is the first-run path. Two people registering at once on a
// fresh instance must not both become owners: one registers, the other is
// told the instance already has an owner and leaves no account behind.
func TestConcurrentFirstRegistrationMakesExactlyOneOwner(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = e.auth.Register(ctx, fmt.Sprintf("first%d@example.test", i), lifecyclePassword, nil, nil, nil)
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInstanceHasOwner):
		default:
			t.Fatalf("register %d: %v", i, err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d registrations succeeded, want 1", ok)
	}
	if got := e.count(t, `SELECT COUNT(*) FROM everstack.users`); got != 1 {
		t.Fatalf("users = %d, want only the registered owner", got)
	}
	if got := e.count(t, `SELECT COUNT(*) FROM everstack.organization_members WHERE role = 'owner'`); got != 1 {
		t.Fatalf("owners = %d, want 1", got)
	}
}

// Two owners leaving (or being removed) at the same moment must not leave
// the organization with no owner. The check and the delete have to be one
// atomic step, so this runs many rounds to give an interleaving a chance.
func TestConcurrentLastOwnerRemovalKeepsAnOwner(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	a, orgID := e.registerOwner(t)
	b := e.addPasswordUser(t, "second-owner@example.test")

	for round := 0; round < 40; round++ {
		for _, id := range []uuid.UUID{a, b} {
			if err := e.orgs.orgRepo.AddMemberSimple(ctx, orgID, id, domain.RoleOwner); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, id := range []uuid.UUID{a, b} {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				<-start
				_ = e.orgs.RemoveMember(ctx, id, orgID, id) // each owner leaves
			}(id)
		}
		close(start)
		wg.Wait()
		if n := e.owners(t, orgID); n < 1 {
			t.Fatalf("round %d: both owners left; the organization has no owner", round)
		}
	}
}

// The same race through role changes: two owners demoting each other at the
// same moment must not leave the organization with no owner.
func TestConcurrentOwnerDemotionKeepsAnOwner(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	a, orgID := e.registerOwner(t)
	b := e.addPasswordUser(t, "second-owner@example.test")

	for round := 0; round < 40; round++ {
		for _, id := range []uuid.UUID{a, b} {
			if err := e.orgs.orgRepo.AddMemberSimple(ctx, orgID, id, domain.RoleOwner); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
			wg.Add(1)
			go func(actor, target uuid.UUID) {
				defer wg.Done()
				<-start
				_, _ = e.orgs.UpdateMemberRole(ctx, actor, orgID, target, domain.RoleAdmin)
			}(pair[0], pair[1])
		}
		close(start)
		wg.Wait()
		if n := e.owners(t, orgID); n < 1 {
			t.Fatalf("round %d: both owners demoted; the organization has no owner", round)
		}
	}
}

// What an invitation grants is checked against the inviter's role when it is
// accepted. A demotion of the inviter that is in flight at that moment must
// either be seen or wait for the acceptance; the acceptance may not read the
// old role and then insert after the demotion lands.
func TestInviterDemotionDuringAcceptanceIsSeen(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	_, orgID := e.registerOwner(t)
	inviter := e.addPasswordUser(t, "inviter@example.test")
	if err := e.orgs.orgRepo.AddMemberSimple(ctx, orgID, inviter, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	inv, err := repository.NewInvitationRepository(e.DB).Create(ctx, "invitee@example.test", domain.RoleAdmin, &inviter, &orgID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Demote the inviter in a transaction that stays open while the
	// invitation is accepted.
	demotion, err := e.DB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := demotion.ExecContext(ctx,
		`UPDATE everstack.organization_members SET role = 'member' WHERE organization_id = $1 AND user_id = $2`, orgID, inviter); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := e.auth.AcceptInvitation(ctx, inv.Token, lifecyclePassword, nil, nil, nil)
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if err := demotion.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := <-done; !errors.Is(err, ErrInvitationRevoked) {
		t.Fatalf("accept during the inviter's demotion: err = %v, want ErrInvitationRevoked", err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM everstack.organization_members om JOIN everstack.users u ON u.id = om.user_id WHERE u.email = 'invitee@example.test'`); n != 0 {
		t.Fatalf("invitee joined with %d membership(s) granted by a demoted inviter", n)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM everstack.users WHERE email = 'invitee@example.test'`); n != 0 {
		t.Fatalf("refused acceptance left %d account(s) behind", n)
	}
}

// A refused sign-in tells a removed member to ask for an invitation, so an
// invitation must be able to bring them back: the owner can invite their
// address, and they accept with the password they already have (not a new
// one), returning with the invited role and nothing more.
func TestRemovedMemberCanBeInvitedBack(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	ownerID, orgID := e.registerOwner(t)
	memberID := e.addPasswordUser(t, "member@example.test")
	if err := e.orgs.orgRepo.AddMemberSimple(ctx, orgID, memberID, domain.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := e.auth.RemoveTeamMember(ctx, ownerID, memberID, []uuid.UUID{orgID}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	owner, err := e.auth.userRepo.GetWithOrganizations(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := e.auth.InviteTeamMember(ctx, owner, "", "member@example.test", domain.RoleMember, 0)
	if err != nil {
		t.Fatalf("invite a removed member: %v", err)
	}

	// The wrong password neither joins nor signs in.
	if _, _, err := e.auth.AcceptInvitation(ctx, invite.Invitation.Token, "Wrong-Password-123", nil, nil, nil); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("accept with the wrong password: err = %v, want ErrInvalidCredentials", err)
	}
	if n := e.memberships(t, memberID); n != 0 {
		t.Fatalf("wrong password created %d membership(s)", n)
	}

	u, session, err := e.auth.AcceptInvitation(ctx, invite.Invitation.Token, lifecyclePassword, nil, nil, nil)
	if err != nil {
		t.Fatalf("accept with the existing password: %v", err)
	}
	if u.User.ID != memberID || session == nil {
		t.Fatalf("accept returned user %s session %v, want the existing account signed in", u.User.ID, session)
	}
	if len(u.Organizations) != 1 || u.Organizations[0].ID != orgID || u.Organizations[0].Role != domain.RoleMember {
		t.Fatalf("organizations = %+v, want member of the inviting organization", u.Organizations)
	}
	if _, _, err := e.auth.Login(ctx, "member@example.test", lifecyclePassword, nil, nil); err != nil {
		t.Fatalf("login after rejoining: %v", err)
	}
}

// Only an owner or admin of an existing organization may create another
// one. A session alone (an organization-less user, or a member) must not be
// able to mint a fresh organization and own it.
func TestOnlyOwnersAndAdminsCreateOrganizations(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	ownerID, orgID := e.registerOwner(t)
	orgless := e.addPasswordUser(t, "orgless@example.test")
	member := e.addPasswordUser(t, "member@example.test")
	admin := e.addPasswordUser(t, "admin@example.test")
	for id, role := range map[uuid.UUID]string{member: domain.RoleMember, admin: domain.RoleAdmin} {
		if err := e.orgs.orgRepo.AddMemberSimple(ctx, orgID, id, role); err != nil {
			t.Fatal(err)
		}
	}

	for name, id := range map[string]uuid.UUID{"organization-less user": orgless, "member": member} {
		if _, err := e.orgs.CreateOrganization(ctx, id, "Mine "+name, nil, nil); err == nil {
			t.Fatalf("%s created an organization", name)
		}
	}
	if got := e.count(t, `SELECT COUNT(*) FROM everstack.organizations`); got != 1 {
		t.Fatalf("organizations = %d after refused creates, want 1", got)
	}
	for name, id := range map[string]uuid.UUID{"admin": admin, "owner": ownerID} {
		if _, err := e.orgs.CreateOrganization(ctx, id, "Second "+name, nil, nil); err != nil {
			t.Fatalf("%s create: %v", name, err)
		}
	}
}

// Sessions minted before this fix for users who have since lost every
// membership are swept at startup; members keep theirs.
func TestRevokeSessionsWithoutMembership(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	_, orgID := e.registerOwner(t)
	orgless := e.addPasswordUser(t, "orgless@example.test")
	member := e.addPasswordUser(t, "member@example.test")
	if err := e.orgs.orgRepo.AddMemberSimple(ctx, orgID, member, domain.RoleMember); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{orgless, member} {
		e.Exec(t, `INSERT INTO everstack.sessions (user_id, token, expires_at) VALUES ($1, $2, now() + interval '1 hour')`, id, "tok-"+id.String())
	}

	n, err := e.orgs.orgRepo.RevokeSessionsWithoutMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || e.sessions(t, orgless) != 0 || e.sessions(t, member) != 1 {
		t.Fatalf("revoked %d; orgless sessions %d, member sessions %d; want 1, 0, 1", n, e.sessions(t, orgless), e.sessions(t, member))
	}
}

// Deleting the instance's last organization would leave every user
// (including its owner) with nothing to sign in to. It is refused, also when
// two owners delete the last two at once.
func TestLastOrganizationCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	ownerID, orgID := e.registerOwner(t)
	if err := e.orgs.DeleteOrganization(ctx, ownerID, orgID); err == nil {
		t.Fatal("deleted the last organization")
	}

	for round := 0; round < 20; round++ {
		second, err := e.orgs.CreateOrganization(ctx, ownerID, fmt.Sprintf("Second %d", round), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ids []uuid.UUID
		if err := e.DB.Select(&ids, `SELECT id FROM everstack.organizations`); err != nil {
			t.Fatal(err)
		}
		if len(ids) != 2 {
			t.Fatalf("round %d: %d organizations, want 2", round, len(ids))
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, id := range ids {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				<-start
				_ = e.orgs.DeleteOrganization(ctx, ownerID, id)
			}(id)
		}
		close(start)
		wg.Wait()
		if got := e.count(t, `SELECT COUNT(*) FROM everstack.organizations`); got != 1 {
			t.Fatalf("round %d: %d organizations left, want 1", round, got)
		}
		_ = second
	}
}

// A gateway connected to Everstack Cloud attaches each cloud user who signs
// in to the cloud organization. It used to attach every one of them as an
// owner, so a cloud viewer or member was an owner on the gateway. The local
// role now follows the role the cloud vouched for; when the cloud names no
// role (an older cloud) a new user gets member and an existing role is left
// alone.
func TestCloudConnectedUserGetsTheirCloudRole(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	ddl, err := os.ReadFile("../../../database/migrations/sql/postgres/auth_config_cloud_connection_20260323150000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(t, `SET search_path TO everstack; `+string(ddl))
	// The gateway's DSN puts everstack on the search path; auth_config is
	// queried unqualified.
	sep := "?"
	if strings.Contains(e.DSN, "?") {
		sep = "&"
	}
	db, err := sqlx.Open("pgx", e.DSN+sep+"search_path=everstack,public")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	auth := NewSelfHostedAuthService(e.auth.config,
		repository.NewUserRepository(e.DB), repository.NewSessionRepository(e.DB),
		repository.NewCredentialsRepository(e.DB), repository.NewInvitationRepository(e.DB),
		repository.NewMagicLinkRepository(e.DB), repository.NewOrganizationRepository(e.DB),
		repository.NewAuthConfigRepository(db),
	)
	cloudOrg := uuid.New()
	if err := auth.SetCloudManaged(ctx, cloudOrg.String(), "acme", "", "", ""); err != nil {
		t.Fatal(err)
	}
	role := func(user uuid.UUID) string {
		var r string
		if err := e.DB.Get(&r, `SELECT role FROM everstack.organization_members WHERE organization_id = $1 AND user_id = $2`, cloudOrg, user); err != nil {
			t.Fatalf("membership: %v", err)
		}
		return r
	}

	viewer := e.addPasswordUser(t, "viewer@example.test")
	if _, err := auth.EnsureUserHasConfiguredCloudOrganization(ctx, viewer, domain.RoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	if got := role(viewer); got != domain.RoleViewer {
		t.Fatalf("cloud viewer is %q on the gateway, want viewer", got)
	}

	// The cloud is the authority: a later sign-in with a new cloud role
	// moves the local one, down as well as up.
	if _, err := auth.EnsureUserHasConfiguredCloudOrganization(ctx, viewer, domain.RoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if got := role(viewer); got != domain.RoleAdmin {
		t.Fatalf("after cloud promotion: %q, want admin", got)
	}

	// No role from the cloud: least privilege for a newcomer, and never an
	// owner by default.
	newcomer := e.addPasswordUser(t, "newcomer@example.test")
	if _, err := auth.EnsureUserHasConfiguredCloudOrganization(ctx, newcomer, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := role(newcomer); got != domain.RoleMember {
		t.Fatalf("roleless newcomer is %q, want member", got)
	}
	// ...and an existing role is not changed.
	if _, err := auth.EnsureUserHasConfiguredCloudOrganization(ctx, viewer, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := role(viewer); got != domain.RoleAdmin {
		t.Fatalf("roleless sign-in changed admin to %q", got)
	}
	// An unknown role is treated as no role.
	if _, err := auth.EnsureUserHasConfiguredCloudOrganization(ctx, newcomer, "superuser", nil); err != nil {
		t.Fatal(err)
	}
	if got := role(newcomer); got != domain.RoleMember {
		t.Fatalf("unknown cloud role made the newcomer %q", got)
	}
}

// An instance that has users but no organization (OIDC sign-ins before any
// registration, or an old install) cannot bootstrap by sign-in. The
// operator names the owner explicitly; that works only while there is no
// organization, and only for an existing account.
func TestBootstrapOwnerByEmail(t *testing.T) {
	ctx := context.Background()
	e := newLifecycleEnv(t)
	a := e.addPasswordUser(t, "a@example.test")
	e.addPasswordUser(t, "b@example.test")

	if _, _, err := e.auth.Login(ctx, "a@example.test", lifecyclePassword, nil, nil); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("sign-in on a used instance: err = %v, want ErrNoOrganization", err)
	}
	if org, err := e.orgs.orgRepo.BootstrapOwnerByEmail(ctx, "nobody@example.test", "default", "Default Organization"); err != nil || org != nil {
		t.Fatalf("unknown email: org=%v err=%v, want nothing done", org, err)
	}
	if org, err := e.orgs.orgRepo.BootstrapOwnerByEmail(ctx, "A@example.test", "default", "Default Organization"); err != nil || org == nil {
		t.Fatalf("bootstrap: org=%v err=%v", org, err)
	}
	if got := e.count(t, `SELECT COUNT(*) FROM everstack.organization_members WHERE user_id = $1 AND role = 'owner'`, a); got != 1 {
		t.Fatalf("named owner has %d owner membership(s), want 1", got)
	}
	// Once an organization exists it never runs again.
	if org, err := e.orgs.orgRepo.BootstrapOwnerByEmail(ctx, "b@example.test", "default", "Default Organization"); err != nil || org != nil {
		t.Fatalf("second bootstrap: org=%v err=%v, want nothing done", org, err)
	}
	if _, _, err := e.auth.Login(ctx, "a@example.test", lifecyclePassword, nil, nil); err != nil {
		t.Fatalf("owner sign-in after bootstrap: %v", err)
	}
}

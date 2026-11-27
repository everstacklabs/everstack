package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
)

// Verification used to read the token (GetValidByToken) and mark it used
// (MarkUsed) in two statements, so two concurrent verifications of the same
// link could both pass the read and each mint a session. Consume must let
// exactly one of them through. Real Postgres; requires EVS_SESSION_TEST_DSN.
func TestMagicLinkConsumeIsSingleUseUnderConcurrency(t *testing.T) {
	e := sessionlivetest.NewCE(t)
	repo := NewMagicLinkRepository(e.DB)
	ctx := context.Background()
	email := e.UserID.String() + "@example.test"

	link, err := repo.Create(ctx, email, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const attempts = 16
	var wg sync.WaitGroup
	results := make(chan *MagicLinkToken, attempts)
	errs := make(chan error, attempts)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := repo.Consume(ctx, link.Token)
			if err != nil {
				errs <- err
				return
			}
			results <- got
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("Consume error: %v", err)
	}
	var winners int
	for got := range results {
		if got != nil {
			winners++
			if got.Email != email || got.UsedAt == nil {
				t.Errorf("consumed token = %+v, want email %s with used_at set", got, email)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent consumes succeeded, want exactly 1", winners, attempts)
	}

	// Expired tokens are never consumed.
	expired, err := repo.Create(ctx, email, -time.Minute)
	if err != nil {
		t.Fatalf("Create expired: %v", err)
	}
	if got, err := repo.Consume(ctx, expired.Token); err != nil || got != nil {
		t.Fatalf("Consume(expired) = (%+v, %v), want (nil, nil)", got, err)
	}
	if got, err := repo.Consume(ctx, "no-such-token"); err != nil || got != nil {
		t.Fatalf("Consume(unknown) = (%+v, %v), want (nil, nil)", got, err)
	}
}

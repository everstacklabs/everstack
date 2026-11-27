package v1

// A parallel fallback step races its legs and serves the first success. These
// tests pin what happens when no leg succeeds: the step must end as soon as
// the last leg has failed, not wait for a winner that can never arrive (with
// no step timeout configured, that wait lasted until the client gave up).

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// prompt bounds how long a race that is already decided may take to return.
// Every leg in these tests answers at once (or on cancel), so anything near
// this is a hang, not a slow machine.
const prompt = 2 * time.Second

// within runs f and fails the test if it does not return inside d.
func within[T any](t *testing.T, d time.Duration, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(d):
		t.Fatalf("did not return within %v", d)
		var zero T
		return zero
	}
}

// waitGoroutines waits for the goroutine count to fall back to at most base,
// so a leg left blocked after the race returns fails the test.
func waitGoroutines(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(prompt)
	for {
		n := runtime.NumGoroutine()
		if n <= base {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("goroutines leaked: %d running, %d before the race\n%s", n, base, buf)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type raceResult struct {
	v   string
	err error
}

func TestRaceLegsAllFailReturnsAggregatedErrorPromptly(t *testing.T) {
	base := runtime.NumGoroutine()
	errA, errB, errC := errors.New("leg a down"), errors.New("leg b down"), errors.New("leg c down")
	legErrs := []error{errA, errB, errC}
	race := billingmeter.NewRace()
	defer race.Close()

	res := within(t, prompt, func() raceResult {
		v, err := raceLegs(context.Background(), race, len(legErrs), func(_ context.Context, leg int) (string, error) {
			return "", legErrs[leg]
		})
		return raceResult{v, err}
	})
	if res.err == nil {
		t.Fatalf("want an error when every leg fails, got winner %q", res.v)
	}
	for _, e := range legErrs {
		if !errors.Is(res.err, e) {
			t.Errorf("aggregated error %q does not wrap %q", res.err, e)
		}
		if !strings.Contains(res.err.Error(), e.Error()) {
			t.Errorf("aggregated error %q does not mention %q", res.err, e)
		}
	}
	if !race.Close() {
		t.Fatal("a race in which every leg failed must end with no winner")
	}
	waitGoroutines(t, base)
}

func TestRaceLegsOneSuccessWins(t *testing.T) {
	base := runtime.NumGoroutine()
	race := billingmeter.NewRace()
	defer race.Close()

	// Legs 0 and 2 fail at once; leg 1 answers after them, so the race has
	// already seen two failures when the winner arrives.
	res := within(t, prompt, func() raceResult {
		v, err := raceLegs(context.Background(), race, 3, func(_ context.Context, leg int) (string, error) {
			if leg == 1 {
				time.Sleep(50 * time.Millisecond)
				return "winner", nil
			}
			return "", errors.New("down")
		})
		return raceResult{v, err}
	})
	if res.err != nil || res.v != "winner" {
		t.Fatalf("got (%q, %v), want the succeeding leg", res.v, res.err)
	}
	if race.Close() {
		t.Fatal("the succeeding leg must have claimed the race")
	}
	waitGoroutines(t, base)
}

// The first success wins and the legs still running are cancelled, so they
// do not outlive the race.
func TestRaceLegsWinnerCancelsLosers(t *testing.T) {
	base := runtime.NumGoroutine()
	race := billingmeter.NewRace()
	defer race.Close()
	loserInFlight := make(chan struct{})

	res := within(t, prompt, func() raceResult {
		v, err := raceLegs(context.Background(), race, 2, func(ctx context.Context, leg int) (string, error) {
			if leg == 0 {
				<-loserInFlight
				return "winner", nil
			}
			close(loserInFlight)
			<-ctx.Done()
			return "", ctx.Err()
		})
		return raceResult{v, err}
	})
	if res.err != nil || res.v != "winner" {
		t.Fatalf("got (%q, %v), want the succeeding leg", res.v, res.err)
	}
	waitGoroutines(t, base)
}

func TestRaceLegsContextCancellationPropagates(t *testing.T) {
	base := runtime.NumGoroutine()
	race := billingmeter.NewRace()
	defer race.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const legs = 3
	entered := make(chan struct{}, legs)
	legCancelled := make(chan struct{}, legs)
	go func() {
		for i := 0; i < legs; i++ {
			<-entered
		}
		cancel()
	}()
	res := within(t, prompt, func() raceResult {
		v, err := raceLegs(ctx, race, legs, func(ctx context.Context, _ int) (string, error) {
			entered <- struct{}{}
			<-ctx.Done()
			legCancelled <- struct{}{}
			return "", ctx.Err()
		})
		return raceResult{v, err}
	})
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("got (%q, %v), want the cancellation", res.v, res.err)
	}
	for i := 0; i < legs; i++ {
		select {
		case <-legCancelled:
		case <-time.After(prompt):
			t.Fatalf("only %d of %d legs saw the cancellation", i, legs)
		}
	}
	waitGoroutines(t, base)
}

// End to end through the chat handler: the primary fails and a parallel
// fallback step with no timeout runs two legs that both fail. The request
// must fail at once, naming both legs, rather than hang until the client
// disconnects.
func TestHandlerParallelFallbackAllLegsFailReturnsPromptly(t *testing.T) {
	sink := newAttemptSink()
	s := attemptServer(t, sink,
		[]validator.FallbackFactorConfig{{Name: "race", Priority: 1, Strategy: "parallel", Models: models("m-a", "m-b")}},
		&scriptedModel{name: "pp", model: "m-primary", fail: errors.New("primary upstream 503")},
		&scriptedModel{name: "pa", model: "m-a", fail: errors.New("pa upstream 502")},
		&scriptedModel{name: "pb", model: "m-b", fail: errors.New("pb upstream 429")},
	)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	err := within(t, prompt, func() error {
		return processChatCompletion(ctx, s, chatReq("m-primary", false), func(*pb.ChatCompletionResponse) error { return nil })
	})
	if err == nil {
		t.Fatal("want an error when the primary and every race leg fail")
	}
	for _, want := range []string{"primary upstream 503", "pa upstream 502", "pb upstream 429"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// Every attempt is still recorded: the primary and both legs.
	sink.waitUnits(3)
}

func TestHandlerParallelFallbackOneLegWins(t *testing.T) {
	sink := newAttemptSink()
	s := attemptServer(t, sink,
		[]validator.FallbackFactorConfig{{Name: "race", Priority: 1, Strategy: "parallel", Models: models("m-a", "m-b")}},
		&scriptedModel{name: "pp", model: "m-primary", fail: errors.New("primary upstream 503")},
		&scriptedModel{name: "pa", model: "m-a", fail: errors.New("pa upstream 502")},
		&scriptedModel{name: "pb", model: "m-b"},
	)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	var served string
	err := within(t, prompt, func() error {
		return processChatCompletion(ctx, s, chatReq("m-primary", false), func(r *pb.ChatCompletionResponse) error {
			served = r.GetModel()
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if served != "m-b" {
		t.Fatalf("served %q, want the leg that succeeded (m-b)", served)
	}
}

// A client that goes away mid-race cancels every leg and the handler returns.
func TestHandlerParallelFallbackClientCancelPropagates(t *testing.T) {
	sink := newAttemptSink()
	aIn, bIn := make(chan struct{}), make(chan struct{})
	s := attemptServer(t, sink,
		[]validator.FallbackFactorConfig{{Name: "race", Priority: 1, Strategy: "parallel", Models: models("m-a", "m-b")}},
		&scriptedModel{name: "pp", model: "m-primary", fail: errors.New("primary upstream 503")},
		&scriptedModel{name: "pa", model: "m-a", untilCancelled: true, entered: aIn},
		&scriptedModel{name: "pb", model: "m-b", untilCancelled: true, entered: bIn},
	)
	ctx, cancel := context.WithCancel(contextkeys.WithTenantID(context.Background(), "tenant-1"))
	defer cancel()
	go func() {
		<-aIn
		<-bIn
		cancel()
	}()
	err := within(t, prompt, func() error {
		return processChatCompletion(ctx, s, chatReq("m-primary", false), func(*pb.ChatCompletionResponse) error { return nil })
	})
	if err == nil {
		t.Fatal("want an error after the client cancelled")
	}
	// Both legs were cancelled and settled as race losers.
	sink.waitUnits(3)
}

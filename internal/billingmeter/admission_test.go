package billingmeter

import (
	"errors"
	"testing"
)

func funded(available int64) *Wallet {
	return &Wallet{OrgID: "org-a", BalanceMicros: available, State: WalletActive, HasPurchase: true}
}

func TestEvaluateAdmissionMatrix(t *testing.T) {
	dbDown := errors.New("dial tcp: connection refused")
	for _, tc := range []struct {
		name      string
		in        AdmissionInput
		allow     bool
		reason    string
		code      string
		exempt    bool
		evaluated bool
	}{
		{name: "funded wallet covers estimate", in: AdmissionInput{Wallet: funded(10_000), EstimateMicros: 5_000, FreeAllowed: true}, allow: true, code: CodeOK},
		{name: "exact cover is admitted", in: AdmissionInput{Wallet: funded(5_000), EstimateMicros: 5_000}, allow: true, code: CodeOK},
		{name: "zero balance would deny", in: AdmissionInput{Wallet: funded(0), EstimateMicros: 1}, reason: ReasonInsufficientCredit, code: CodeFailedPrecondition},
		{name: "estimate above available would deny", in: AdmissionInput{Wallet: funded(4_999), EstimateMicros: 5_000}, reason: ReasonInsufficientCredit, code: CodeFailedPrecondition},
		{name: "holds reduce available", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 10_000, HeldMicros: 9_000, State: WalletActive, HasPurchase: true}, EstimateMicros: 5_000}, reason: ReasonInsufficientCredit, code: CodeFailedPrecondition},
		{name: "zero estimate at zero balance would deny (compute)", in: AdmissionInput{Wallet: funded(0)}, reason: ReasonInsufficientCredit, code: CodeFailedPrecondition},
		{name: "zero estimate with positive balance admits (compute)", in: AdmissionInput{Wallet: funded(1)}, allow: true, code: CodeOK},
		{name: "frozen wallet would deny even when funded", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 10_000_000, State: WalletFrozen, HasPurchase: true}, EstimateMicros: 1}, reason: ReasonWalletFrozen, code: CodeFailedPrecondition},
		{name: "unknown wallet state is treated as frozen", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 10_000_000, State: "deleted", HasPurchase: true}, EstimateMicros: 1}, reason: ReasonWalletFrozen, code: CodeFailedPrecondition},
		{name: "free org off the allowlist would deny", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 5_000_000, State: WalletActive}, EstimateMicros: 1, FreeAllowed: false}, reason: ReasonModelNotInFreeAllowlist, code: CodeFailedPrecondition},
		{name: "free org on the allowlist admits", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 5_000_000, State: WalletActive}, EstimateMicros: 1, FreeAllowed: true}, allow: true, code: CodeOK},
		{name: "paid org off the allowlist admits", in: AdmissionInput{Wallet: funded(5_000_000), EstimateMicros: 1, FreeAllowed: false}, allow: true, code: CodeOK},
		{name: "paid subscriber without a purchase is not free", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 20_000_000, State: WalletActive, PlanTier: "basic", PaidSubscription: true}, EstimateMicros: 1, FreeAllowed: false}, allow: true, code: CodeOK},
		{name: "plan tier alone without a live subscription stays free", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 5_000_000, State: WalletActive, PlanTier: "pro"}, EstimateMicros: 1, FreeAllowed: false}, reason: ReasonModelNotInFreeAllowlist, code: CodeFailedPrecondition},
		{name: "enterprise tier is not free", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 5_000_000, State: WalletActive, PlanTier: "Enterprise"}, EstimateMicros: 1}, allow: true, code: CodeOK},
		{name: "compute skips the model allowlist", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 5_000_000, State: WalletActive}, Compute: true}, allow: true, code: CodeOK},
		{name: "enterprise negative limit admits into the negative", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: 0, State: WalletActive, PlanTier: "enterprise", CreditLimitMicros: -10_000_000}, EstimateMicros: 1_000}, allow: true, code: CodeOK},
		{name: "enterprise negative limit admits at the floor", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: -9_999_000, State: WalletActive, PlanTier: "enterprise", CreditLimitMicros: -10_000_000}, EstimateMicros: 1_000}, allow: true, code: CodeOK},
		{name: "enterprise negative limit denies past the floor", in: AdmissionInput{Wallet: &Wallet{BalanceMicros: -9_999_500, State: WalletActive, PlanTier: "enterprise", CreditLimitMicros: -10_000_000}, EstimateMicros: 1_000}, reason: ReasonInsufficientCredit, code: CodeFailedPrecondition},
		{name: "in-flight cap reached would deny", in: AdmissionInput{Wallet: funded(10_000_000), EstimateMicros: 1, Inflight: 33, InflightCap: 32}, reason: ReasonInflightLimit, code: CodeResourceExhausted},
		{name: "in-flight at cap admits", in: AdmissionInput{Wallet: funded(10_000_000), EstimateMicros: 1, Inflight: 32, InflightCap: 32}, allow: true, code: CodeOK},
		{name: "in-flight with no cap admits", in: AdmissionInput{Wallet: funded(10_000_000), EstimateMicros: 1, Inflight: 10_000}, allow: true, code: CodeOK},
		{name: "in-flight evaluated without the DB", in: AdmissionInput{WalletErr: dbDown, Inflight: 40, InflightCap: 32}, reason: ReasonInflightLimit, code: CodeResourceExhausted},
		{name: "platform DB down reports unavailable, not 402", in: AdmissionInput{WalletErr: dbDown, EstimateMicros: 1}, reason: ReasonArbiterUnavailable, code: CodeUnavailable},
		{name: "missing wallet snapshot reports unavailable", in: AdmissionInput{EstimateMicros: 1}, reason: ReasonArbiterUnavailable, code: CodeUnavailable},
		{name: "unpriced model is rejected first", in: AdmissionInput{Unpriced: true, Wallet: funded(10_000_000)}, reason: ReasonUnpricedModel, code: CodeFailedPrecondition},
		{name: "internal call is exempt at zero balance", in: AdmissionInput{Internal: true, Wallet: funded(0), EstimateMicros: 100}, allow: true, code: CodeOK, exempt: true},
		{name: "internal call is exempt with the DB down", in: AdmissionInput{Internal: true, WalletErr: dbDown}, allow: true, code: CodeOK, exempt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateAdmission(tc.in)
			if d.Admit != tc.allow || d.Reason != tc.reason || d.Code != tc.code || d.Exempt != tc.exempt {
				t.Fatalf("EvaluateAdmission = %+v, want admit=%v reason=%q code=%q exempt=%v", d, tc.allow, tc.reason, tc.code, tc.exempt)
			}
		})
	}
}

func TestWalletAvailableAndFree(t *testing.T) {
	w := Wallet{BalanceMicros: 100, HeldMicros: 30}
	if w.Available() != 70 {
		t.Fatalf("Available = %d, want 70", w.Available())
	}
	if !w.Free() {
		t.Fatal("no purchase, non-enterprise tier must be free")
	}
	w.HasPurchase = true
	if w.Free() {
		t.Fatal("an org with a purchase is not free")
	}
	w.HasPurchase, w.PaidSubscription = false, true
	if w.Free() {
		t.Fatal("an org with an active or trialing paid subscription is not free")
	}
}

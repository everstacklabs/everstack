package service

import (
	"context"
	"strings"
	"time"
)

// AutoTopUpOutcome selects the auto top-up notice.
type AutoTopUpOutcome string

const (
	// AutoTopUpCharged: the default card was charged and credit added.
	AutoTopUpCharged AutoTopUpOutcome = "charged"
	// AutoTopUpFailed: the charge did not go through; action needed.
	AutoTopUpFailed AutoTopUpOutcome = "failed"
)

// AutoTopUpParams describes one auto top-up charge or failure. Amounts are
// USD. Every optional field hides its row when empty or unknown.
type AutoTopUpParams struct {
	To             string
	IdempotencyKey string
	OrgName        string
	OrgSlug        string
	Outcome        AutoTopUpOutcome
	Amount         float64
	CardLabel      string // "Visa ending in 4242"
	// Charged.
	Balance           float64
	BalanceKnown      bool
	RemainingCap      float64
	MonthlyCap        float64
	RemainingCapKnown bool
	// Failed: what happened and what to do, in customer words.
	Reason string
	// NoCard: there was no default card to charge.
	NoCard bool
	// Paused: auto top-up is stopped until an owner or admin turns it on.
	Paused bool
	// PauseReason is "dispute" or "refund" when a dispute or refund of
	// credit paused it (no charge was attempted); empty for failed charges.
	PauseReason string
	NextAttempt time.Time // zero hides the row
	At          time.Time // when it was charged or attempted; zero hides the row
}

func autoTopUpData(p AutoTopUpParams) map[string]interface{} {
	data := map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"amount":            "",
		"pause_reason":      p.PauseReason,
		"card":              strings.TrimSpace(p.CardLabel),
		"balance":           "",
		"remaining_cap":     "",
		"monthly_cap":       "",
		"reason":            strings.TrimSpace(p.Reason),
		"no_card":           p.NoCard,
		"paused":            p.Paused,
		"next_attempt":      FormatNoticeTime(p.NextAttempt),
		"changed_at":        FormatNoticeTime(p.At),
	}
	if p.Amount > 0 {
		data["amount"] = FormatMoney(p.Amount, "usd")
	}
	if p.BalanceKnown {
		data["balance"] = FormatMoney(p.Balance, "usd")
	}
	if p.RemainingCapKnown {
		data["remaining_cap"] = FormatMoney(p.RemainingCap, "usd")
		data["monthly_cap"] = FormatMoney(p.MonthlyCap, "usd")
	}
	return data
}

// SendAutoTopUp sends the auto top-up charged or failed notice.
func (s *EmailService) SendAutoTopUp(ctx context.Context, p AutoTopUpParams) error {
	typ := EmailTypeAutoTopUpCharged
	if p.Outcome == AutoTopUpFailed {
		typ = EmailTypeAutoTopUpFailed
	}
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: typ, To: p.To, IdempotencyKey: p.IdempotencyKey, Data: autoTopUpData(p),
	})
	return err
}

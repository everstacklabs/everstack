package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/everstacklabs/everstack/internal/email/types"
)

// CardNoticeEvent selects which saved-payment-method email to send
// (docs/design/payment-methods.md).
type CardNoticeEvent string

const (
	CardNoticeAdded          CardNoticeEvent = "added"
	CardNoticeRemoved        CardNoticeEvent = "removed"
	CardNoticeDefaultChanged CardNoticeEvent = "default_changed"
	CardNoticeExpiring       CardNoticeEvent = "expiring"
)

var cardNoticeTypes = map[CardNoticeEvent]types.EmailType{
	CardNoticeAdded:          types.EmailTypeCardAdded,
	CardNoticeRemoved:        types.EmailTypeCardRemoved,
	CardNoticeDefaultChanged: types.EmailTypeCardDefaultChanged,
	CardNoticeExpiring:       types.EmailTypeCardExpiring,
}

// CardNoticeSource says where a payment method change came from, so the
// email can say who made it.
type CardNoticeSource string

const (
	// CardNoticeSourceMember: a member made the change on the Everstack
	// billing page (ActorName names them).
	CardNoticeSourceMember CardNoticeSource = "member"
	// CardNoticeSourceStripe: the change was made in Stripe, outside the
	// Everstack billing page, and reached us as a Stripe event.
	CardNoticeSourceStripe CardNoticeSource = "stripe"
	// CardNoticeSourceSync: the daily reconciliation with Stripe found the
	// change (its Stripe event never arrived).
	CardNoticeSourceSync CardNoticeSource = "sync"
)

// CardNoticeParams describes one saved payment method change. It carries
// display metadata only (brand, last four, expiry, a masked Link email);
// never a card number.
type CardNoticeParams struct {
	To             string
	IdempotencyKey string // optional provider idempotency key (see SendEmailRequest)
	OrgName        string // the organization's display name, never its slug
	OrgSlug        string // builds the billing link; empty falls back to the app root
	Event          CardNoticeEvent
	Type           string // Stripe payment method type, e.g. "card" or "link"
	Brand          string // Stripe card brand, e.g. "visa"; empty for non-cards
	Last4          string
	ExpMonth       int
	ExpYear        int
	IsDefault      bool // added: the new card became the default

	// MethodLabel names the method ("Visa ending in 4242", "Link
	// (a***@gmail.com)", "Apple Pay (Visa ending in 4242)"), captured when
	// the notice was queued: a removed method's Link email is scrubbed from
	// the database by then. Empty (a notice queued by an older release)
	// falls back to a label built from Type, Brand and Last4.
	MethodLabel string
	// IsCard: the method is a plain card (no wallet, not Link), so the
	// headline may say "Card". Only meaningful with MethodLabel.
	IsCard bool

	Source    CardNoticeSource // empty when unknown: no "by" line
	ActorName string           // Source member: the member's name or email
	ChangedAt time.Time        // zero hides the date

	// What is left after a removal or default change. RemainingKnown false
	// (older notices) hides the paragraph.
	RemainingKnown   bool
	RemainingDefault string // label of the default now, "" when none
	RemainingCount   int    // active methods left
	PreviousDefault  string // default changed: the method it replaced, if known
}

var cardBrandNames = map[string]string{
	"amex": "American Express", "diners": "Diners Club", "discover": "Discover",
	"eftpos_au": "eftpos", "jcb": "JCB", "mastercard": "Mastercard",
	"unionpay": "UnionPay", "visa": "Visa", "cartes_bancaires": "Cartes Bancaires",
	"interac": "Interac",
}

var walletNames = map[string]string{
	"apple_pay": "Apple Pay", "google_pay": "Google Pay", "samsung_pay": "Samsung Pay",
	"link": "Link", "masterpass": "Masterpass", "visa_checkout": "Visa Checkout",
	"amex_express_checkout": "Amex Express Checkout",
}

// PaymentMethodIdentity is what names a saved payment method in copy.
type PaymentMethodIdentity struct {
	Type      string // Stripe payment method type
	Brand     string // card brand
	Last4     string
	Wallet    string // card wallet type, e.g. "apple_pay"
	LinkEmail string // Link account email; masked before it is shown
}

// PaymentMethodLabel names a payment method for customer copy:
//
//	card:   "Visa ending in 4242"
//	wallet: "Apple Pay (Visa ending in 4242)"
//	Link:   "Link (a***@gmail.com)"
//
// isCard reports a plain card, the only kind a headline may call "Card".
// Nothing here ever prints more than the last four digits.
func PaymentMethodLabel(id PaymentMethodIdentity) (label string, isCard bool) {
	last4 := strings.TrimSpace(id.Last4)
	typ := strings.ToLower(strings.TrimSpace(id.Type))
	if typ == "link" {
		if masked := MaskEmail(id.LinkEmail); masked != "" {
			return "Link (" + masked + ")", false
		}
		return "Link", false
	}
	if last4 == "" {
		if typ == "card" || typ == "" {
			return "Card", typ == "card" && strings.TrimSpace(id.Wallet) == ""
		}
		return "Payment method", false
	}
	brand := cardBrandNames[strings.ToLower(strings.TrimSpace(id.Brand))]
	if brand == "" {
		brand = "Card"
	}
	label = brand + " ending in " + last4
	if w := strings.ToLower(strings.TrimSpace(id.Wallet)); w != "" {
		name := walletNames[w]
		if name == "" {
			name = "Wallet"
		}
		return name + " (" + label + ")", false
	}
	return label, typ == "card" || typ == ""
}

// MaskEmail keeps the first character of the local part and the domain:
// "ada@gmail.com" -> "a***@gmail.com". Anything that is not an address
// masks to "" (the caller then omits it).
func MaskEmail(addr string) string {
	addr = strings.TrimSpace(addr)
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at == len(addr)-1 {
		return ""
	}
	first, _ := utf8.DecodeRuneInString(addr)
	return string(first) + "***" + addr[at:]
}

// legacyCardLabel is the label for a notice queued before MethodLabel
// existed.
func legacyCardLabel(p CardNoticeParams) (label string, isCard bool) {
	return PaymentMethodLabel(PaymentMethodIdentity{Type: p.Type, Brand: p.Brand, Last4: p.Last4})
}

// FormatNoticeTime renders an instant for billing copy, always in UTC:
// "Oct 4, 2026 at 6:32 PM UTC".
func FormatNoticeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("Jan 2, 2006 at 3:04 PM UTC")
}

func cardNoticeData(p CardNoticeParams) map[string]interface{} {
	label, isCard := p.MethodLabel, p.IsCard
	if strings.TrimSpace(label) == "" {
		label, isCard = legacyCardLabel(p)
	}
	noun, nounTitle := "payment method", "Payment method"
	if isCard {
		noun, nounTitle = "card", "Card"
	}
	expires := ""
	if p.ExpMonth >= 1 && p.ExpMonth <= 12 && p.ExpYear > 0 {
		expires = fmt.Sprintf("%02d/%d", p.ExpMonth, p.ExpYear)
	}
	org := strings.TrimSpace(p.OrgName)
	if org == "" {
		org = "your organization"
	}
	data := map[string]interface{}{
		"organization_name": org,
		"org_slug":          p.OrgSlug,
		"method":            label,
		"noun":              noun,
		"noun_title":        nounTitle,
		"expires":           expires,
		"is_default":        p.IsDefault,
		"changed_at":        FormatNoticeTime(p.ChangedAt),
		"previous_default":  p.PreviousDefault,
	}
	by, sourceNote := changeAttribution(p, org)
	data["changed_by"] = by
	data["source_note"] = sourceNote
	data["remaining_note"] = remainingNote(p)
	return data
}

// changeAttribution renders who made the change: the "by" row value (only
// a member is ever named) and, for a change made anywhere but the billing
// page, one plain sentence saying how it happened. No vendor or system names.
func changeAttribution(p CardNoticeParams, org string) (by, note string) {
	switch p.Source {
	case CardNoticeSourceMember:
		by = strings.TrimSpace(p.ActorName)
		if by == "" {
			by = "A member of " + org
		}
	case CardNoticeSourceStripe:
		switch p.Event {
		case CardNoticeRemoved:
			note = "It was removed outside your Everstack billing page."
		case CardNoticeDefaultChanged:
			note = "The default was changed outside your Everstack billing page."
		}
	case CardNoticeSourceSync:
		switch p.Event {
		case CardNoticeRemoved:
			note = "It was removed automatically because it is no longer on file for this organization."
		case CardNoticeDefaultChanged:
			note = "The default was changed outside your Everstack billing page."
		}
	}
	return by, note
}

// remainingNote says what the organization can still be charged with.
func remainingNote(p CardNoticeParams) string {
	if !p.RemainingKnown {
		return ""
	}
	switch p.Event {
	case CardNoticeRemoved:
	default:
		// Default changed names the new default in its headline and table.
		return ""
	}
	if p.RemainingDefault != "" {
		return "Your default payment method is now " + p.RemainingDefault + "."
	}
	if p.RemainingCount > 0 {
		return "You still have " + plural(p.RemainingCount, "saved payment method") +
			", but none is the default. Choose a default on the billing page so credit top-ups can be charged."
	}
	return "You have no saved payment method. Credit top-ups will ask for a card at checkout."
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// SendCardNotice sends a card added, removed, default changed or expiring
// soon email to one billing manager.
func (s *EmailService) SendCardNotice(ctx context.Context, p CardNoticeParams) error {
	typ, ok := cardNoticeTypes[p.Event]
	if !ok {
		return fmt.Errorf("unknown card notice event %q", p.Event)
	}
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type:           typ,
		To:             p.To,
		IdempotencyKey: p.IdempotencyKey,
		Data:           cardNoticeData(p),
	})
	return err
}

// BillingContactChangedParams describes an organization whose billing email
// followed an owner's sign-in email change, for one of its other owners.
type BillingContactChangedParams struct {
	To         string // the owner being told
	OrgName    string
	OrgSlug    string
	MemberName string // the owner whose email changed; may be empty
	OldEmail   string
	NewEmail   string
	ChangedAt  time.Time
}

func billingContactChangedData(p BillingContactChangedParams) map[string]interface{} {
	member := strings.TrimSpace(p.MemberName)
	if member == "" {
		member = "An owner"
	}
	org := strings.TrimSpace(p.OrgName)
	if org == "" {
		org = "your organization"
	}
	return map[string]interface{}{
		"organization_name": org,
		"org_slug":          p.OrgSlug,
		"member":            member,
		"old_email":         strings.TrimSpace(p.OldEmail),
		"new_email":         strings.TrimSpace(p.NewEmail),
		"changed_at":        FormatNoticeTime(p.ChangedAt),
	}
}

// SendBillingContactChanged tells one owner that the organization's billing
// email moved with another owner's sign-in email.
func (s *EmailService) SendBillingContactChanged(ctx context.Context, p BillingContactChangedParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeBillingContactChanged,
		To:   p.To,
		Data: billingContactChangedData(p),
	})
	return err
}

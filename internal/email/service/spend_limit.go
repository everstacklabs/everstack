package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SpendLimitParams describes one spend limit threshold crossing (80% or
// 100%) for the organization or one of its API keys. Amounts are USD. The
// email names the key by its display name and never carries an id.
type SpendLimitParams struct {
	To             string
	IdempotencyKey string
	OrgName        string
	OrgSlug        string
	// APIKeyName is the key's display name; empty for an organization limit.
	APIKeyName string
	// Period is "daily" or "monthly".
	Period  string
	Percent int
	Spent   float64
	Limit   float64
	// Block: the limit refuses requests when reached (else it only alerts).
	Block bool
	// Enforced: refusals are switched on for the organization. While they
	// are not, a block limit says requests "would be refused".
	Enforced bool
	ResetsAt time.Time
	// CheckedAt is when the crossing was seen; zero hides the row.
	CheckedAt time.Time
	// Creator: the recipient created the API key and is not an owner or
	// admin, so the footer says why they got the email.
	Creator bool
}

func spendLimitData(p SpendLimitParams) map[string]interface{} {
	org := orgDisplayName(p.OrgName)
	key := strings.TrimSpace(p.APIKeyName)
	isKey := key != ""
	// A crossing seen just after its window ended (UTC midnight) is still
	// sent, in the past tense.
	ended := !p.CheckedAt.IsZero() && !p.ResetsAt.After(p.CheckedAt)
	periodWord, periodTitle, periodNoun, per := "monthly", "Monthly", "this month", "per month"
	if ended {
		periodNoun = "last month"
	}
	if p.Period == "daily" {
		periodWord, periodTitle, periodNoun, per = "daily", "Daily", "today", "per day"
		if ended {
			periodNoun = "yesterday"
		}
	}
	scopeLabel, scopePhrase, requestScope := "The whole organization", "the "+org+" organization", "from this organization"
	if isKey {
		scopeLabel = `API key "` + key + `"`
		scopePhrase = `API key "` + key + `" in the ` + org + ` organization`
		requestScope = "made with this API key"
	}
	action := "Alert only"
	if p.Block {
		action = "Block requests"
	}
	limit := FormatMoney(p.Limit, "usd")
	return map[string]interface{}{
		"organization_name": org,
		"org_slug":          p.OrgSlug,
		"is_key":            isKey,
		"key_name":          key,
		"scope_label":       scopeLabel,
		"scope_phrase":      scopePhrase,
		"request_scope":     requestScope,
		"percent":           fmt.Sprintf("%d", p.Percent),
		"reached":           p.Percent >= 100,
		"spent":             FormatMoney(p.Spent, "usd"),
		"limit":             limit,
		"limit_label":       limit + " " + per,
		"period_word":       periodWord,
		"period_title":      periodTitle,
		"period_noun":       periodNoun,
		"block":             p.Block,
		"enforced":          p.Enforced,
		"action_label":      action,
		"resets_at":         FormatNoticeTime(p.ResetsAt),
		"checked_at":        FormatNoticeTime(p.CheckedAt),
		"creator":           p.Creator,
		"ended":             ended,
	}
}

// SendSpendLimit sends a spend limit threshold notice.
func (s *EmailService) SendSpendLimit(ctx context.Context, p SpendLimitParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeSpendLimit, To: p.To, IdempotencyKey: p.IdempotencyKey, Data: spendLimitData(p),
	})
	return err
}

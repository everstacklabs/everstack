package enterprise

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// LimitCheck describes one creation-time cap check.
//
// Query returns a single count. $1 is bound to the tenant ids the cap is
// counted over (the whole organization on a managed gateway, the tenant alone
// elsewhere), typed by Column; Args bind $2 onward. Write the tenant predicate
// as `tenant_id = ANY($1::uuid[])` or `tenant_id = ANY($1::text[])` to match
// the column, never `tenant_id = $1`.
type LimitCheck struct {
	Type UsageType
	// Resource is the singular noun used in messages, e.g. "agent".
	Resource string
	// TenantID is the tenant the new rows are written under.
	TenantID string
	Query    string
	Column   TenantColumn
	Args     []interface{}
	// Delta is how many resources the request creates (batch size, min 1).
	Delta int64
	// ManagedOnly applies the check on a managed (Cloud) gateway only. New
	// checks use it so self-hosted and CE installs keep their behaviour.
	ManagedOnly bool
}

// Managed returns c restricted to managed gateways.
func (c LimitCheck) Managed() LimitCheck {
	c.ManagedOnly = true
	return c
}

func (c LimitCheck) args(scope CountScope) []interface{} {
	return append([]interface{}{scope.Arg(c.Column)}, c.Args...)
}

func (c LimitCheck) delta() int64 {
	if c.Delta < 1 {
		return 1
	}
	return c.Delta
}

// CheckLimit is the unserialized creation-time gate: COUNT then compare.
// Prefer ReserveLimitSlot where two concurrent creates could both pass.
func CheckLimit(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, c LimitCheck) error {
	if c.ManagedOnly && !ManagedGateway() {
		return nil
	}
	ent := ResolveEntitlements(ctx, monitor)
	limit, capped := ent.Limit(c.Type)
	if !capped {
		return nil
	}
	if limit == 0 {
		return unavailableError(ent, c)
	}
	if db == nil {
		return nil
	}
	scope, err := resolveCountScopeStrict(ctx, c.TenantID)
	if err != nil {
		return err
	}
	return countAgainstLimit(ctx, db, ent, limit, c, c.Query, c.args(scope))
}

// PrecheckLimit is an advisory, unlocked check for refusing early, before
// expensive work such as a model call. It is not enforcement: the create
// itself must still take ReserveLimitSlot, which serializes racing creators.
func PrecheckLimit(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, c LimitCheck) error {
	return CheckLimit(ctx, db, monitor, c)
}

// ReserveLimitSlot is CheckLimit serialized per (organization, limit type)
// with an advisory lock; see ReserveResourceSlot for the slot lifecycle.
func ReserveLimitSlot(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, c LimitCheck) (*ResourceSlot, error) {
	if c.ManagedOnly && !ManagedGateway() {
		return &ResourceSlot{}, nil
	}
	ent := ResolveEntitlements(ctx, monitor)
	limit, capped := ent.Limit(c.Type)
	if !capped {
		return &ResourceSlot{}, nil
	}
	if limit == 0 {
		return nil, unavailableError(ent, c)
	}
	if db == nil {
		return &ResourceSlot{}, nil
	}
	scope, err := resolveCountScopeStrict(ctx, c.TenantID)
	if err != nil {
		return nil, err
	}
	return reserveCountedSlot(ctx, db, ent, limit, c, c.Query, c.args(scope), scope.Key)
}

// --- Errors ---------------------------------------------------------------

// PlanLimitReason is the ErrorInfo reason attached to a plan cap refusal.
const PlanLimitReason = "plan_limit_reached"

// UpgradeURL is where a capped customer can compare plans.
const UpgradeURL = "https://everstack.ai/pricing"

// LimitError is a genuine plan cap refusal. Its message is written for end
// users; errors.Is(err, ErrResourceLimit) is true.
type LimitError struct {
	Type     UsageType
	Resource string
	// Limit is the plan cap; 0 means the plan does not include the resource.
	Limit int64
	// Used is the organization's current count; Requested is how many the
	// refused request would have added.
	Used      int64
	Requested int64
	Tier      string
	// CommunityEdition is set when the cap comes from CE defaults on a
	// self-hosted gateway rather than a plan.
	CommunityEdition bool
}

func (e *LimitError) Is(target error) bool { return target == ErrResourceLimit }

func (e *LimitError) Error() string {
	plural := pluralize(e.Resource)
	if e.Limit == 0 {
		if e.CommunityEdition {
			return fmt.Sprintf("%s are not available on the Community Edition. Upgrade to use them: %s", capitalize(plural), UpgradeURL)
		}
		return fmt.Sprintf("%s are not included in your current plan. Upgrade your plan to use them: %s", capitalize(plural), UpgradeURL)
	}
	holder := "your plan includes"
	if e.CommunityEdition {
		holder = "the Community Edition includes"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s limit reached: %s %d and you have %d", capitalize(e.Resource), holder, e.Limit, e.Used)
	if e.Requested > 1 && e.Used < e.Limit {
		fmt.Fprintf(&b, ", so %d more would not fit", e.Requested)
	}
	if e.CommunityEdition {
		fmt.Fprintf(&b, ". Upgrade for higher limits: %s", UpgradeURL)
	} else {
		fmt.Fprintf(&b, ". Upgrade your plan to add more %s: %s", plural, UpgradeURL)
	}
	return b.String()
}

// ErrorInfo is the structured form clients use to recognise a plan cap and
// render an upgrade path without parsing the message.
func (e *LimitError) ErrorInfo() *errdetails.ErrorInfo {
	return &errdetails.ErrorInfo{
		Reason: PlanLimitReason,
		Domain: "everstack.ai",
		Metadata: map[string]string{
			"limit_type":  string(e.Type),
			"resource":    e.Resource,
			"limit":       strconv.FormatInt(e.Limit, 10),
			"used":        strconv.FormatInt(e.Used, 10),
			"requested":   strconv.FormatInt(e.Requested, 10),
			"tier":        e.Tier,
			"upgrade_url": UpgradeURL,
		},
	}
}

func unavailableError(ent Entitlements, c LimitCheck) error {
	return &LimitError{
		Type: c.Type, Resource: c.Resource, Limit: 0, Requested: c.delta(),
		Tier: ent.Tier, CommunityEdition: ent.Source == "ce",
	}
}

func capError(ent Entitlements, c LimitCheck, limit, used int64) error {
	return &LimitError{
		Type: c.Type, Resource: c.Resource, Limit: limit, Used: used, Requested: c.delta(),
		Tier: ent.Tier, CommunityEdition: ent.Source == "ce",
	}
}

func pluralize(noun string) string {
	noun = strings.TrimSpace(noun)
	if noun == "" {
		return "resources"
	}
	if strings.HasSuffix(noun, "s") {
		return noun
	}
	return noun + "s"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

package billingcredit

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrPurchaseNotInReview is returned when an approval names a purchase that
// is not awaiting review (or was already approved).
var ErrPurchaseNotInReview = errors.New("credit purchase is not awaiting review")

// ReviewPurchase is a paid credit purchase held in needs_review.
type ReviewPurchase struct {
	ID                   uuid.UUID  `db:"id"`
	OrganizationID       uuid.UUID  `db:"organization_id"`
	RequestedMicros      int64      `db:"requested_micros"`
	AmountTotalMinor     *int64     `db:"amount_total_minor"`
	AmountTaxMinor       *int64     `db:"amount_tax_minor"`
	Currency             string     `db:"currency"`
	ReviewReason         *string    `db:"review_reason"`
	ReviewApprovedMicros *int64     `db:"review_approved_micros"`
	ReviewApprovedBy     *string    `db:"review_approved_by"`
	CreatedAt            time.Time  `db:"created_at"`
	ReviewApprovedAt     *time.Time `db:"review_approved_at"`
}

// ListPurchasesInReview returns an organization's purchases in needs_review.
func ListPurchasesInReview(ctx context.Context, db *sqlx.DB, organizationID uuid.UUID) ([]ReviewPurchase, error) {
	var out []ReviewPurchase
	err := db.SelectContext(ctx, &out, `
		SELECT id, organization_id, requested_micros, amount_total_minor, amount_tax_minor, currency,
		       review_reason, review_approved_micros, review_approved_by, created_at, review_approved_at
		FROM billing.credit_purchases
		WHERE organization_id = $1 AND state = 'needs_review'
		ORDER BY created_at DESC LIMIT 50`, organizationID)
	return out, err
}

// ApproveReviewInput is an operator's decision to honour a needs_review
// purchase for a given amount of credit.
type ApproveReviewInput struct {
	PurchaseID   uuid.UUID
	AmountMicros int64
	Actor        string
	Note         string
}

// ApprovePurchaseReview records an approval on a needs_review purchase. It
// does not touch the ledger: the billing service mints approved purchases
// through its normal purchase path (key purchase:<id>, with every known
// refund and dispute applied), so later reversals claw back correctly. A
// purchase can be approved once. Returns the owning organization.
func ApprovePurchaseReview(ctx context.Context, db *sqlx.DB, in ApproveReviewInput) (uuid.UUID, error) {
	if db == nil {
		return uuid.Nil, errors.New("billing database is not configured")
	}
	if in.PurchaseID == uuid.Nil || in.AmountMicros <= 0 {
		return uuid.Nil, errors.New("purchase and a positive amount are required")
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		return uuid.Nil, errors.New("a note is required")
	}
	var org uuid.UUID
	err := db.GetContext(ctx, &org, `
		UPDATE billing.credit_purchases
		SET review_approved_micros = $2, review_approved_by = $3, review_approved_at = NOW(),
		    review_note = $4, updated_at = NOW()
		WHERE id = $1 AND state = 'needs_review' AND review_approved_micros IS NULL
		RETURNING organization_id`, in.PurchaseID, in.AmountMicros, in.Actor, note)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, ErrPurchaseNotInReview
	}
	return org, err
}

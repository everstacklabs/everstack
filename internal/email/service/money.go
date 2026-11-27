package service

import (
	"fmt"
	"math"
	"strings"
)

// Stripe amounts are integers in the currency's minor unit, and the minor unit
// is not always a hundredth. These sets mirror the ones the cloud invoice
// table uses (apps/cloud/src/components/billing/invoices-tab.tsx) so an email
// and the billing page never disagree on the same invoice.
// https://docs.stripe.com/currencies#zero-decimal
var zeroDecimalCurrencies = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true, "KRW": true,
	"MGA": true, "PYG": true, "RWF": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true,
}

var threeDecimalCurrencies = map[string]bool{
	"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true,
}

func normalizeCurrency(currency string) string {
	code := strings.ToUpper(strings.TrimSpace(currency))
	if code == "" {
		return "USD"
	}
	return code
}

// MinorUnitDecimals is how many decimal places the currency's minor unit has
// (0 for JPY, 3 for KWD, 2 for everything else).
func MinorUnitDecimals(currency string) int {
	code := normalizeCurrency(currency)
	switch {
	case zeroDecimalCurrencies[code]:
		return 0
	case threeDecimalCurrencies[code]:
		return 3
	default:
		return 2
	}
}

// MajorUnits converts a provider minor-unit amount (Stripe's amount_paid,
// amount_due) to major units: 1000 JPY -> 1000, 1230 KWD -> 1.230, 1000 USD
// cents -> 10.00.
func MajorUnits(minor int64, currency string) float64 {
	return float64(minor) / math.Pow10(MinorUnitDecimals(currency))
}

// FormatMoney renders a major-unit amount with its currency, at the currency's
// own precision. USD, EUR and GBP get their symbol; anything else is written as
// "1000 JPY" or "1.230 KWD" so the reader is never shown a wrong symbol.
func FormatMoney(amount float64, currency string) string {
	code := normalizeCurrency(currency)
	decimals := MinorUnitDecimals(code)
	number := fmt.Sprintf("%.*f", decimals, amount)
	switch code {
	case "USD":
		return "$" + number
	case "EUR":
		return "€" + number
	case "GBP":
		return "£" + number
	default:
		return number + " " + code
	}
}

// Package shipto is the ShipTo value object: the delivery address of an order
// or a saved address of a shopper. It is synthetic PII held only by
// customer-ordering; it never appears in an event (ADR 0002, A18).
package shipto

import (
	"strings"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// Bounds of the value object (ADR 0002, A15). Plain literals on purpose.
const (
	// MaxText is the longest name, address line, city or region.
	MaxText = 100
	// MaxPostalCode is the longest postal code.
	MaxPostalCode = 20
	// MaxPhone is the longest phone number.
	MaxPhone = 40
	// PrefixLength is the length of a destination postal prefix (A10).
	PrefixLength = 3
)

// ShipTo is a postal address. Line2 and Phone are optional.
type ShipTo struct {
	Name        string
	Line1       string
	Line2       string
	City        string
	Region      string
	PostalCode  string
	CountryCode string
	Phone       string
}

// Validate checks every field and returns an error wrapping
// shared.ErrValidation naming the first broken one.
func (s ShipTo) Validate() error {
	required := []struct{ field, value string }{
		{"name", s.Name}, {"line1", s.Line1}, {"city", s.City}, {"region", s.Region},
	}
	for _, f := range required {
		if !shared.ValidTextN(f.value, MaxText) {
			return shared.Invalid("shipTo.%s must be 1 to %d trimmed characters without control characters", f.field, MaxText)
		}
	}
	if s.Line2 != "" && !shared.ValidTextN(s.Line2, MaxText) {
		return shared.Invalid("shipTo.line2 must be at most %d trimmed characters without control characters", MaxText)
	}
	if !shared.ValidTextN(s.PostalCode, MaxPostalCode) {
		return shared.Invalid("shipTo.postalCode must be 1 to %d trimmed characters", MaxPostalCode)
	}
	if len(Normalize(s.PostalCode)) < PrefixLength {
		return shared.Invalid("shipTo.postalCode must hold at least %d letters or digits", PrefixLength)
	}
	if !validCountry(s.CountryCode) {
		return shared.Invalid("shipTo.countryCode must be two upper-case letters (ISO 3166-1 alpha-2)")
	}
	if s.Phone != "" && !shared.ValidTextN(s.Phone, MaxPhone) {
		return shared.Invalid("shipTo.phone must be at most %d trimmed characters", MaxPhone)
	}
	return nil
}

func validCountry(c string) bool {
	if len(c) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return false
		}
	}
	return true
}

// Normalize upper-cases s and keeps only ASCII letters and digits.
func Normalize(postalCode string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(postalCode) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DestinationPrefix returns the destination postal prefix: the first
// PrefixLength characters of the normalised postal code (A10). Call it on a
// validated ShipTo.
func (s ShipTo) DestinationPrefix() string {
	n := Normalize(s.PostalCode)
	return n[:min(len(n), PrefixLength)]
}

// PrefixMatches reports whether prefix (a delivery-promise destination) is a
// non-empty prefix of the normalised postal code.
func (s ShipTo) PrefixMatches(prefix string) bool {
	return prefix != "" && strings.HasPrefix(Normalize(s.PostalCode), prefix)
}

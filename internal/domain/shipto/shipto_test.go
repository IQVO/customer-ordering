package shipto

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

func valid() ShipTo {
	return ShipTo{Name: "Ana Example", Line1: "1 Sample Street", Line2: "Apt 2", City: "Austin", Region: "TX",
		PostalCode: "78701", CountryCode: "US", Phone: "+1 555 0100"}
}

func TestValidateAcceptsAWellFormedAddress(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid address rejected: %v", err)
	}
	s := valid()
	s.Line2, s.Phone = "", ""
	if err := s.Validate(); err != nil {
		t.Fatalf("optional fields must be optional: %v", err)
	}
}

func TestValidateAcceptsTheBounds(t *testing.T) {
	s := valid()
	s.Name = strings.Repeat("n", MaxText)
	s.Line2 = strings.Repeat("l", MaxText)
	s.PostalCode = strings.Repeat("9", MaxPostalCode)
	s.Phone = strings.Repeat("1", MaxPhone)
	if err := s.Validate(); err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
	s.PostalCode = "A1-B"
	if err := s.Validate(); err != nil {
		t.Fatalf("a code with exactly %d letters or digits is enough: %v", PrefixLength, err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*ShipTo){
		"blank name":       func(s *ShipTo) { s.Name = "" },
		"padded name":      func(s *ShipTo) { s.Name = " Ana" },
		"long name":        func(s *ShipTo) { s.Name = strings.Repeat("n", MaxText+1) },
		"blank line1":      func(s *ShipTo) { s.Line1 = "" },
		"blank city":       func(s *ShipTo) { s.City = "" },
		"blank region":     func(s *ShipTo) { s.Region = "" },
		"padded line2":     func(s *ShipTo) { s.Line2 = "x " },
		"long line2":       func(s *ShipTo) { s.Line2 = strings.Repeat("l", MaxText+1) },
		"blank postalCode": func(s *ShipTo) { s.PostalCode = "" },
		"long postalCode":  func(s *ShipTo) { s.PostalCode = strings.Repeat("9", MaxPostalCode+1) },
		"short postalCode": func(s *ShipTo) { s.PostalCode = "7-8" },
		"lower country":    func(s *ShipTo) { s.CountryCode = "us" },
		"long country":     func(s *ShipTo) { s.CountryCode = "USA" },
		"short country":    func(s *ShipTo) { s.CountryCode = "U" },
		"digit country":    func(s *ShipTo) { s.CountryCode = "U1" },
		"digit first":      func(s *ShipTo) { s.CountryCode = "1U" },
		"edge Z country":   func(s *ShipTo) { s.CountryCode = "U[" },
		"edge A country":   func(s *ShipTo) { s.CountryCode = "@U" },
		"padded phone":     func(s *ShipTo) { s.Phone = " 1" },
		"long phone":       func(s *ShipTo) { s.Phone = strings.Repeat("1", MaxPhone+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := valid()
			mutate(&s)
			if err := s.Validate(); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
		})
	}
}

func TestCountryEdgesAreAccepted(t *testing.T) {
	for _, c := range []string{"AA", "ZZ", "AZ", "ZA"} {
		s := valid()
		s.CountryCode = c
		if err := s.Validate(); err != nil {
			t.Errorf("%s rejected: %v", c, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{"78701": "78701", "sw1a 1aa": "SW1A1AA", "12-345": "12345", "é9z": "9Z", "": "", "A\u00a0B": "AB"}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDestinationPrefix(t *testing.T) {
	s := valid()
	if got := s.DestinationPrefix(); got != "787" {
		t.Fatalf("prefix = %q", got)
	}
	s.PostalCode = "sw1a 1aa"
	if got := s.DestinationPrefix(); got != "SW1" {
		t.Fatalf("prefix = %q", got)
	}
	s.PostalCode = "78"
	if got := s.DestinationPrefix(); got != "78" {
		t.Fatalf("short code is returned whole, got %q", got)
	}
	s.PostalCode = "787"
	if got := s.DestinationPrefix(); got != "787" {
		t.Fatalf("exact length, got %q", got)
	}
}

func TestPrefixMatches(t *testing.T) {
	s := valid()
	for _, p := range []string{"7", "78", "787", "78701"} {
		if !s.PrefixMatches(p) {
			t.Errorf("%q must match 78701", p)
		}
	}
	for _, p := range []string{"", "788", "787012", "x"} {
		if s.PrefixMatches(p) {
			t.Errorf("%q must not match 78701", p)
		}
	}
}

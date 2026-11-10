package shipto

import "testing"

func TestNormalizedAddressIgnoresCaseAndPunctuation(t *testing.T) {
	a, b := valid(), valid()
	a.Line1, b.Line1 = "12 Main St.", "12 main st"
	if a.NormalizedAddress() != b.NormalizedAddress() {
		t.Fatalf("%q != %q", a.NormalizedAddress(), b.NormalizedAddress())
	}
}

func TestNormalizedAddressIgnoresNameAndPhone(t *testing.T) {
	a, b := valid(), valid()
	b.Name, b.Phone = "Someone Else", ""
	if a.NormalizedAddress() != b.NormalizedAddress() {
		t.Fatalf("name and phone must not matter: %q != %q", a.NormalizedAddress(), b.NormalizedAddress())
	}
}

func TestNormalizedAddressSeparatesEveryAddressPart(t *testing.T) {
	base := valid().NormalizedAddress()
	mutate := map[string]func(*ShipTo){
		"line1":   func(s *ShipTo) { s.Line1 = "2 Other Street" },
		"line2":   func(s *ShipTo) { s.Line2 = "Apt 3" },
		"city":    func(s *ShipTo) { s.City = "Dallas" },
		"region":  func(s *ShipTo) { s.Region = "OK" },
		"postal":  func(s *ShipTo) { s.PostalCode = "78702" },
		"country": func(s *ShipTo) { s.CountryCode = "CA" },
	}
	for name, m := range mutate {
		s := valid()
		m(&s)
		if s.NormalizedAddress() == base {
			t.Errorf("a different %s gives the same normalised address", name)
		}
	}
}

func TestNormalizedAddressKeepsEmptyPartsPositional(t *testing.T) {
	a, b := valid(), valid()
	a.Line2, a.City = "", "B"
	b.Line2, b.City = "B", ""
	if a.NormalizedAddress() == b.NormalizedAddress() {
		t.Fatal("a value moved from one part to its neighbour must change the result")
	}
}

package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

const testSecret = "0123456789abcdef-test-secret"

func addr() shipto.ShipTo {
	return shipto.ShipTo{Name: "Ana Example", Line1: "12 Main St.", Line2: "Apt 2", City: "Austin", Region: "TX",
		PostalCode: "78701", CountryCode: "US", Phone: "+1 555 0100"}
}

func keyOf(t *testing.T, secret, shopper string, s shipto.ShipTo) string {
	t.Helper()
	k, err := NewKeyer(secret)
	if err != nil {
		t.Fatalf("NewKeyer: %v", err)
	}
	return k.KeyFor(shopper, s.NormalizedAddress())
}

func TestKeyIsTheDocumentedHMAC(t *testing.T) {
	got := keyOf(t, testSecret, "shopper-1", addr())
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte("consolidation-key/v1\x00shopper-1\x0012MAINST|APT2|AUSTIN|TX|78701|US"))
	if want := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("key = %s, want %s", got, want)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Fatalf("key %q is not 64 lower-case hex characters", got)
	}
}

func TestKeyIgnoresCaseAndPunctuationOfTheAddress(t *testing.T) {
	a, b := addr(), addr()
	a.Line1, b.Line1 = "12 Main St.", "12 main st"
	if keyOf(t, testSecret, "s", a) != keyOf(t, testSecret, "s", b) {
		t.Fatal(`"12 Main St." and "12 main st" must share a key`)
	}
}

func TestKeyIgnoresNameAndPhone(t *testing.T) {
	a, b := addr(), addr()
	b.Name, b.Phone = "Someone Else", ""
	if keyOf(t, testSecret, "s", a) != keyOf(t, testSecret, "s", b) {
		t.Fatal("recipient name and phone must not change the key")
	}
}

func TestKeyDiffersByShopperLine2PostalCodeAndSecret(t *testing.T) {
	base := keyOf(t, testSecret, "shopper-1", addr())
	line2, postal := addr(), addr()
	line2.Line2, postal.PostalCode = "Apt 3", "78702"
	for name, other := range map[string]string{
		"shopper": keyOf(t, testSecret, "shopper-2", addr()),
		"line2":   keyOf(t, testSecret, "shopper-1", line2),
		"postal":  keyOf(t, testSecret, "shopper-1", postal),
		"secret":  keyOf(t, strings.ToUpper(testSecret), "shopper-1", addr()),
	} {
		if other == base {
			t.Errorf("a different %s must give a different key", name)
		}
	}
}

func TestShopperAndAddressCannotBleedIntoEachOther(t *testing.T) {
	k, err := NewKeyer(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if k.KeyFor("ab", "c") == k.KeyFor("a", "bc") {
		t.Fatal("the 0x00 separator must keep (ab, c) and (a, bc) apart")
	}
}

func TestNewKeyerRefusesAShortOrEmptySecret(t *testing.T) {
	if _, err := NewKeyer("short"); !errors.Is(err, ErrSecretTooShort) {
		t.Fatalf("short secret = %v", err)
	}
	if _, err := NewKeyer(""); err == nil {
		t.Fatal("an empty secret must be refused")
	}
	if _, err := NewKeyer(strings.Repeat("x", MinSecretLen)); err != nil {
		t.Fatalf("exactly %d bytes is enough: %v", MinSecretLen, err)
	}
}

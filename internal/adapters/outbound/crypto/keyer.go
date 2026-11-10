// Package crypto is the production ports.ConsolidationKeyer: a keyed hash of
// shopper id and normalised ship-to (ADR 0012).
package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// MinSecretLen is the shortest secret accepted, in bytes.
const MinSecretLen = 16

// derivation is the domain-separation label of the key, versioned: a changed
// normalisation rule is a new label, never an edit of this one.
const derivation = "consolidation-key/v1"

// ErrSecretTooShort is returned for a secret under MinSecretLen bytes.
var ErrSecretTooShort = fmt.Errorf("the consolidation key secret must be at least %d bytes", MinSecretLen)

// Keyer derives consolidation keys with HMAC-SHA-256.
type Keyer struct{ secret []byte }

var _ ports.ConsolidationKeyer = (*Keyer)(nil)

// NewKeyer returns a Keyer over secret (at least MinSecretLen bytes).
func NewKeyer(secret string) (*Keyer, error) {
	if secret == "" {
		return nil, errors.New("the consolidation key secret is empty")
	}
	if len(secret) < MinSecretLen {
		return nil, ErrSecretTooShort
	}
	return &Keyer{secret: []byte(secret)}, nil
}

// KeyFor returns hex(HMAC-SHA-256(secret, derivation || 0x00 || shopperID ||
// 0x00 || normalizedShipTo)): 64 lower-case hex characters.
func (k *Keyer) KeyFor(shopperID, normalizedShipTo string) string {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(derivation))
	mac.Write([]byte{0})
	mac.Write([]byte(shopperID))
	mac.Write([]byte{0})
	mac.Write([]byte(normalizedShipTo))
	return hex.EncodeToString(mac.Sum(nil))
}

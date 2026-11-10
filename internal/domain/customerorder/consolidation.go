package customerorder

// ConsolidationKeyLen is the length of a consolidation key: the hex form of an
// HMAC-SHA-256 digest (ADR 0012).
const ConsolidationKeyLen = 64

// ValidConsolidationKey reports whether k is "" (no key was derived: the
// order never merges) or exactly ConsolidationKeyLen lower-case hex characters.
func ValidConsolidationKey(k string) bool {
	if k == "" {
		return true
	}
	if len(k) != ConsolidationKeyLen {
		return false
	}
	for i := 0; i < len(k); i++ {
		if c := k[i]; c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

// ConsolidationKey returns the key order-sourcing may merge this order's
// groups by, "" when none was derived (ADR 0012).
func (o *CustomerOrder) ConsolidationKey() string { return o.consolidationKey }

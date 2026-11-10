package main

import (
	"fmt"
	"log/slog"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/crypto"
	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// consolidationEnvVar names the secret the consolidation key is keyed with
// (ADR 0012).
const consolidationEnvVar = "CONSOLIDATION_KEY_SECRET"

// loadConsolidationSecret reads CONSOLIDATION_KEY_SECRET. Unset (or empty) is
// valid and means the feature is off; a value shorter than crypto.MinSecretLen
// bytes is refused at boot, naming the variable, because a weak secret would
// make the key guessable from a known address.
func loadConsolidationSecret(env func(string) string) (string, error) {
	secret := env(consolidationEnvVar)
	if secret != "" && len(secret) < crypto.MinSecretLen {
		return "", fmt.Errorf("%s must be at least %d bytes (unset it to switch consolidation keys off)", consolidationEnvVar, crypto.MinSecretLen)
	}
	return secret, nil
}

// consolidationKeyer builds the keyer for secret. An empty secret returns nil
// and logs ONE warning: no key is derived, the placed event omits the field
// and the order never merges.
func consolidationKeyer(secret string, logger *slog.Logger) (ports.ConsolidationKeyer, error) {
	if secret == "" {
		logger.Warn(consolidationEnvVar + " is not set: no consolidation key is derived, so no order of this service is ever merged with another")
		return nil, nil
	}
	keyer, err := crypto.NewKeyer(secret)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", consolidationEnvVar, err)
	}
	return keyer, nil
}

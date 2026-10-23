// Package ids is the production ports.IDGenerator: random UUID v4 shopper ids
// and order ids derived as a UUID v5 of the Idempotency-Key (ADR 0002, A13).
package ids

import (
	"github.com/google/uuid"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// orderIDNamespace is the fixed UUID v5 namespace of the order ids derived from
// an Idempotency-Key. Never change it: a retried checkout must land on the same order.
var orderIDNamespace = uuid.MustParse("6f1c1d1e-7a5b-5c7e-9b0a-3d2f4c1b8a90")

// Generator mints ids.
type Generator struct{}

var _ ports.IDGenerator = Generator{}

// NewShopperID returns a random UUID v4.
func (Generator) NewShopperID() shopper.ID { return shopper.ID(uuid.NewString()) }

// OrderIDFor returns the UUID v5 of key: the same key always yields the same id.
func (Generator) OrderIDFor(key string) customerorder.ID {
	return customerorder.ID(uuid.NewSHA1(orderIDNamespace, []byte(key)).String())
}

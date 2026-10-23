// Package http is the REST inbound adapter: every route of apis/openapi.yaml on
// a chi router, RFC 9457 problem documents for every error and the liveness and
// readiness endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

type shipTo = shipto.ShipTo

// Route names under which the Idempotency-Key records are stored.
const (
	routeCheckout = "POST /checkout"
	routeCancel   = "POST /orders/{orderId}:cancel"
	routeShopper  = "POST /shoppers"
)

// Server holds the use cases the handlers call.
type Server struct {
	PlaceOrder       usecases.PlaceOrder
	GetOrder         usecases.GetOrder
	GetOrderTracking usecases.GetOrderTracking
	CancelOrder      usecases.CancelOrder
	GetShipTo        usecases.GetShipTo
	RegisterShopper  usecases.RegisterShopper

	// IdempotencyStore and Clock back the Idempotency-Key middleware of the
	// three POST routes.
	IdempotencyStore ports.IdempotencyStore
	Clock            ports.Clock
	// IDs derives the order id of a checkout from its Idempotency-Key.
	IDs ports.IDGenerator
	// Readiness backs GET /readyz; nil means always ready (unit tests).
	Readiness *Readiness
}

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	// A NUL (U+0000) in the path, the query or a body is a uniform client error,
	// never a Postgres 22021 -> 500; it runs before the Idempotency-Key
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.With(s.idempotent(routeCheckout)).Post("/checkout", s.handlePlaceOrder)
	r.Get("/orders/{orderId}", s.handleGetOrder)
	r.Get("/orders/{orderId}/tracking", s.handleGetTracking)
	r.Get("/orders/{orderId}/ship-to", s.handleGetShipTo)
	r.With(s.idempotent(routeCancel)).Post("/orders/{orderId}:cancel", s.handleCancelOrder)
	r.With(s.idempotent(routeShopper)).Post("/shoppers", s.handleRegisterShopper)
	return r
}

func (s *Server) idempotent(route string) func(http.Handler) http.Handler {
	return RequireIdempotencyKey(s.IdempotencyStore, s.Clock, route)
}

// Package http is the REST inbound adapter: every route of apis/openapi.yaml on
// a chi router, RFC 9457 problem documents for every error and the liveness and
// readiness endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/customer-ordering/internal/adapters/inbound/requestlog"
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
	routeAddress  = "POST /shoppers/{shopperId}/addresses"
)

// Server holds the use cases the handlers call.
type Server struct {
	PlaceOrder       usecases.PlaceOrder
	GetOrder         usecases.GetOrder
	GetOrderTracking usecases.GetOrderTracking
	CancelOrder      usecases.CancelOrder
	GetShipTo        usecases.GetShipTo
	RegisterShopper  usecases.RegisterShopper
	AddSavedAddress  usecases.AddSavedAddress
	GetShopper       usecases.GetShopper
	// FindShopperByEmail backs GET /shoppers?email= (NG3).
	FindShopperByEmail usecases.FindShopperByEmail
	// ListOrdersByShopper backs GET /orders?shopperId= (NG2).
	ListOrdersByShopper usecases.ListOrdersByShopper

	// IdempotencyStore and Clock back the Idempotency-Key middleware of the
	// POST routes.
	IdempotencyStore ports.IdempotencyStore
	Clock            ports.Clock
	// IDs derives the order id of a checkout from its Idempotency-Key.
	IDs ports.IDGenerator
	// Readiness backs GET /readyz; nil means always ready (unit tests).
	Readiness *Readiness
}

// DefaultServiceName names the server spans and the HTTP metrics when the
// composition root passes no service name.
const DefaultServiceName = "customer-ordering"

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml. A nil
// logger defaults to slog.Default(); an empty serviceName to DefaultServiceName.
//
// Middleware order is the fleet observability standard: RequestID, then otelchi
// (the server span, named after the chi route pattern, so one span name per
// route and not per order id), then the request-duration histogram, then
// RequestLogger (it runs inside the span, so its record carries trace_id and
// span_id), then Recoverer. telemetry.Setup must have run before this call:
// otelchimetric binds the global MeterProvider when it is constructed.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	// A NUL (U+0000) in the path, the query or a body is a uniform client error,
	// never a Postgres 22021 -> 500; it runs before the Idempotency-Key
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.With(s.idempotent(routeCheckout)).Post("/checkout", s.handlePlaceOrder)
	r.Get("/orders", s.handleListOrders)
	r.Get("/orders/{orderId}", s.handleGetOrder)
	r.Get("/orders/{orderId}/tracking", s.handleGetTracking)
	r.Get("/orders/{orderId}/ship-to", s.handleGetShipTo)
	r.With(s.idempotent(routeCancel)).Post("/orders/{orderId}:cancel", s.handleCancelOrder)
	r.Get("/shoppers", s.handleFindShopper)
	r.With(s.idempotent(routeShopper)).Post("/shoppers", s.handleRegisterShopper)
	r.Get("/shoppers/{shopperId}", s.handleGetShopper)
	r.With(s.idempotent(routeAddress)).Post("/shoppers/{shopperId}/addresses", s.handleAddSavedAddress)
	return r
}

func (s *Server) idempotent(route string) func(http.Handler) http.Handler {
	return RequireIdempotencyKey(s.IdempotencyStore, s.Clock, route)
}

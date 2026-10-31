package main

import (
	"log/slog"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	inbounda2a "github.com/claudioed/customer-ordering/internal/adapters/inbound/a2a"
	"github.com/claudioed/customer-ordering/internal/adapters/inbound/requestlog"
)

// newAgent composes the A2A surface over the two read use cases (the SAME ones
// the REST and MCP adapters use): the Agent Card advertising cfg.AgentURL, the
// JSON-RPC handler and the HTTP router around them. The adapter never writes,
// so no unit of work, outbox, event publisher or promise gateway is wired, and
// it calls no other context.
func newAgent(deps inbounda2a.Deps, cfg inbounda2a.Config, logger *slog.Logger, serviceName string) http.Handler {
	return newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), inbounda2a.Card(cfg), logger, serviceName)
}

// newRouter wraps the A2A handler in a small chi router so the binary is
// deployable behind Kubernetes probes:
//
//   - GET /healthz  -> 200 {"status":"ok"} (open; liveness/readiness probes).
//   - GET /.well-known/agent-card.json -> the Agent Card (the discovery
//     contract, ADR 0008).
//   - /  and /a2a   -> the A2A JSON-RPC handler. Both paths are served: the
//     canonical root and the fleet's <CTX>_A2A_ENDPOINT convention. The root
//     mount is a catch-all subtree, so "/a2a" reaches the handler through it
//     (TestRouter_BothMountsReachTheJSONRPCHandler pins both paths); the
//     explicit routes above it (healthz, the card) win over it.
//
// The middleware order is the fleet observability standard, identical to the
// REST router: RequestID, otelchi (server span named after the route pattern),
// the request-duration histogram, RequestLogger, Recoverer. telemetry.Setup
// must have run before this call (otelchimetric binds the global MeterProvider
// at construction).
//
// No auth middleware is ever added here (TestNoAuthIsRequired).
func newRouter(a2aHandler http.Handler, card *a2a.AgentCard, logger *slog.Logger, serviceName string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	r.Mount("/", a2aHandler)
	return r
}

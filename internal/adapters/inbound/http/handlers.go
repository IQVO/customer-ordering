package http

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// orderIDNamespace is the fixed UUID v5 namespace of the order ids derived from
// an Idempotency-Key (ADR 0002, A13). Never change it: a retried checkout must
// land on the same order.
var orderIDNamespace = uuid.MustParse("6f1c1d1e-7a5b-5c7e-9b0a-3d2f4c1b8a90")

// maxOrderIDLength bounds a path identifier; a longer one cannot name an order.
const maxOrderIDLength = 64

// orderIDFromKey derives the order id of a checkout from its Idempotency-Key.
func orderIDFromKey(key string) customerorder.ID {
	return customerorder.ID(uuid.NewSHA1(orderIDNamespace, []byte(key)).String())
}

// orderIDParam returns the {orderId} path parameter, or false after writing 404
// for one that can never name an order.
func orderIDParam(w http.ResponseWriter, r *http.Request) (customerorder.ID, bool) {
	id := chi.URLParam(r, "orderId")
	if id == "" || len(id) > maxOrderIDLength {
		writeError(w, r, ports.ErrNotFound)
		return "", false
	}
	return customerorder.ID(id), true
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

func (s *Server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	groups := make([]usecases.CheckoutGroup, len(req.Groups))
	for i, g := range req.Groups {
		groups[i] = usecases.CheckoutGroup(g)
	}
	view, err := s.PlaceOrder.Execute(r.Context(), usecases.PlaceOrderInput{
		OrderID: orderIDFromKey(r.Header.Get(IdempotencyKeyHeader)), ShopperID: req.ShopperID, ShipTo: req.ShipTo.domain(), Lines: req.lines(), Groups: groups,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/orders/"+url.PathEscape(view.OrderID))
	writeJSON(w, http.StatusCreated, orderView(view))
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := orderIDParam(w, r)
	if !ok {
		return
	}
	view, err := s.GetOrder.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, orderView(view))
}

func (s *Server) handleGetTracking(w http.ResponseWriter, r *http.Request) {
	id, ok := orderIDParam(w, r)
	if !ok {
		return
	}
	view, err := s.GetOrderTracking.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trackingView(view))
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := orderIDParam(w, r)
	if !ok {
		return
	}
	var req cancelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.CancelOrder.Execute(r.Context(), usecases.CancelOrderInput{OrderID: id, Reason: req.Reason})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, orderView(view))
}

// handleGetShipTo serves the synthetic PII. It is the only place the address
// leaves the service (ADR RN-0006): never cached, never in an event.
func (s *Server) handleGetShipTo(w http.ResponseWriter, r *http.Request) {
	id, ok := orderIDParam(w, r)
	if !ok {
		return
	}
	view, err := s.GetShipTo.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, shipToView(view))
}

func (s *Server) handleRegisterShopper(w http.ResponseWriter, r *http.Request) {
	var req registerShopperRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	saved := make([]shipTo, len(req.SavedAddresses))
	for i, a := range req.SavedAddresses {
		saved[i] = a.domain()
	}
	view, err := s.RegisterShopper.Execute(r.Context(), usecases.RegisterShopperInput{Email: req.Email, SavedAddresses: saved})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/shoppers/"+url.PathEscape(view.ShopperID))
	writeJSON(w, http.StatusCreated, shopperView(view))
}

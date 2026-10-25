package http

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// maxOrderIDLength bounds a path identifier; a longer one cannot name an order.
const maxOrderIDLength = 64

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

// shopperIDParam returns the {shopperId} path parameter, or false after
// writing 404 for one that can never name a shopper.
func shopperIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "shopperId")
	if id == "" || len(id) > maxOrderIDLength {
		writeError(w, r, ports.ErrNotFound)
		return "", false
	}
	return id, true
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
		OrderID: s.IDs.OrderIDFor(r.Header.Get(IdempotencyKeyHeader)), ShopperID: req.ShopperID,
		ShipTo: req.ShipTo.domain(), ShipToAddressID: req.ShipToAddressID, Lines: req.lines(), Groups: groups,
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

// handleGetShopper serves GET /shoppers/{shopperId} (NG1): the profile with
// its saved addresses. The addresses are synthetic PII and leave the service
// only here and on the add route's answer.
func (s *Server) handleGetShopper(w http.ResponseWriter, r *http.Request) {
	id, ok := shopperIDParam(w, r)
	if !ok {
		return
	}
	view, err := s.GetShopper.Execute(r.Context(), shopper.ID(id))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, shopperView(view))
}

// handleFindShopper serves GET /shoppers?email= (NG3): the match is trimmed
// and lower-cased (A16); no shopper answers 404 not-found.
func (s *Server) handleFindShopper(w http.ResponseWriter, r *http.Request) {
	view, err := s.FindShopperByEmail.Execute(r.Context(), r.URL.Query().Get("email"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, shopperView(view))
}

// handleAddSavedAddress serves POST /shoppers/{shopperId}/addresses (NG1):
// the address id derives from the Idempotency-Key, so a replay after the
// idempotency record expired still returns the same address.
func (s *Server) handleAddSavedAddress(w http.ResponseWriter, r *http.Request) {
	id, ok := shopperIDParam(w, r)
	if !ok {
		return
	}
	var req shipToDTO
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.AddSavedAddress.Execute(r.Context(), usecases.AddSavedAddressInput{
		ShopperID: id, Address: req.domain(), IdempotencyKey: r.Header.Get(IdempotencyKeyHeader),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/shoppers/"+url.PathEscape(id)+"/addresses/"+url.PathEscape(view.AddressID))
	writeJSON(w, http.StatusCreated, savedAddressView(view))
}

// handleListOrders serves GET /orders?shopperId=&after=&limit= (NG2): newest
// first, the cursor of the previous page in after. The items never carry the
// address.
func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Get("shopperId") == "" {
		writeError(w, r, shared.Invalid("shopperId is required"))
		return
	}
	limit := 0
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, r, shared.Invalid("limit must be an integer between 1 and %d", usecases.MaxOrderListLimit))
			return
		}
		limit = n
	}
	view, err := s.ListOrdersByShopper.Execute(r.Context(), usecases.ListOrdersInput{
		ShopperID: query.Get("shopperId"), After: query.Get("after"), Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, orderListView(view))
}

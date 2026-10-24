package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/claudioed/customer-ordering/internal/adapters/inbound/http"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/ids"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	store *memory.Store
	gw    *memory.PromiseGateway
	srv   *httptest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := memory.NewStore(memory.NewClock(t0))
	gw := memory.NewPromiseGateway()
	deps := usecases.Deps{
		Orders: s.Orders(), Shoppers: s.Shoppers(), Tracking: s.Tracking(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Promises: gw, Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{},
	}
	sh, err := shopper.Register(shopper.NewShopper{ID: "shopper-1", Email: "ana@example.com"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Shoppers().Save(context.Background(), sh); err != nil {
		t.Fatal(err)
	}
	terms := func(p, n string) promising.Terms {
		return promising.Terms{PromiseID: p, Tier: "NEXT_DAY", WitnessNodeID: n, ShipBy: t0.Add(10 * time.Hour), DeliverByEarliest: t0.Add(20 * time.Hour), DeliverByLatest: t0.Add(30 * time.Hour), DestinationPostalPrefix: "787"}
	}
	gw.Terms["offer-1"], gw.Terms["offer-2"] = terms("p-1", "fc-1"), terms("p-2", "fc-2")
	gw.Offers = []promising.Offer{{OfferID: "new-1", Tier: "NEXT_DAY", DeliverByEarliest: t0.Add(40 * time.Hour), DeliverByLatest: t0.Add(50 * time.Hour), ExpiresAt: t0.Add(time.Hour)}}
	api := &httpadapter.Server{
		PlaceOrder: usecases.PlaceOrder{Deps: deps}, GetOrder: usecases.GetOrder{Deps: deps}, GetOrderTracking: usecases.GetOrderTracking{Deps: deps},
		CancelOrder: usecases.CancelOrder{Deps: deps}, GetShipTo: usecases.GetShipTo{Deps: deps}, RegisterShopper: usecases.RegisterShopper{Deps: deps},
		IdempotencyStore: s.Idempotency(), Clock: deps.Clock, IDs: ids.Generator{},
	}
	srv := httptest.NewServer(httpadapter.NewRouter(api))
	t.Cleanup(srv.Close)
	return &env{t: t, store: s, gw: gw, srv: srv}
}

type reply struct {
	Status int
	Header http.Header
	Body   string
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("body is not JSON: %q (%v)", r.Body, err)
	}
	return m
}

func (e *env) do(method, path, key, body string) reply {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: string(b)}
}

const checkoutBody = `{"shopperId":"shopper-1",
 "shipTo":{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US","phone":"+1 555 0100"},
 "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1},{"lineNo":3,"catalogItemId":"sku-300","quantity":5}],
 "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1,2]},{"groupNo":2,"offerId":"offer-2","lineNos":[3]}]}`

func (e *env) checkout(key string) reply { return e.do("POST", "/checkout", key, checkoutBody) }

func (e *env) placed() string {
	e.t.Helper()
	r := e.checkout("key-placed")
	if r.Status != 201 {
		e.t.Fatalf("checkout = %d %s", r.Status, r.Body)
	}
	return r.json(e.t)["orderId"].(string)
}

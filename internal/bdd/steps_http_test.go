package bdd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/cucumber/godog"

	httpadapter "github.com/claudioed/customer-ordering/internal/adapters/inbound/http"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// checkoutBody is the cart of the use-case steps spelled as openapi.yaml's
// CheckoutRequest: 3 lines, 2 groups, the two scripted offers, postal 78701.
const checkoutBody = `{"shopperId":"shopper-1",
 "shipTo":{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US"},
 "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1},{"lineNo":3,"catalogItemId":"sku-300","quantity":5}],
 "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1,2]},{"groupNo":2,"offerId":"offer-2","lineNos":[3]}]}`

// api serves the real router of apis/openapi.yaml over the scenario's
// in-memory world, so scenarios can speak HTTP exactly as a caller does.
func (w *world) api() *httptest.Server {
	if w.apiSrv == nil {
		s := &httpadapter.Server{
			PlaceOrder: usecases.PlaceOrder{Deps: w.deps}, GetOrder: usecases.GetOrder{Deps: w.deps}, GetOrderTracking: usecases.GetOrderTracking{Deps: w.deps},
			CancelOrder: usecases.CancelOrder{Deps: w.deps}, GetShipTo: usecases.GetShipTo{Deps: w.deps}, RegisterShopper: usecases.RegisterShopper{Deps: w.deps},
			AddSavedAddress: usecases.AddSavedAddress{Deps: w.deps}, GetShopper: usecases.GetShopper{Deps: w.deps},
			FindShopperByEmail: usecases.FindShopperByEmail{Deps: w.deps}, ListOrdersByShopper: usecases.ListOrdersByShopper{Deps: w.deps},
			IdempotencyStore: w.store.Idempotency(), Clock: w.deps.Clock, IDs: w.deps.IDs,
		}
		w.apiSrv = httptest.NewServer(httpadapter.NewRouter(s, nil, ""))
	}
	return w.apiSrv
}

// call records the last API answer.
func (w *world) call(method, path, key, body string) error {
	req, err := http.NewRequest(method, w.api().URL+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	w.apiStatus, w.apiBody, w.apiHeader = resp.StatusCode, string(b), resp.Header
	return nil
}

func (w *world) apiJSON() (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(w.apiBody), &m); err != nil {
		return nil, fmt.Errorf("answer is not a JSON object: %q (%v)", w.apiBody, err)
	}
	return m, nil
}

func registerHTTP(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the API requests "([^"]*)"$`, func(path string) error { return w.call("GET", path, "", "") })
	sc.Step(`^the API posts to "([^"]*)" with Idempotency-Key "([^"]*)" the body:$`, func(path, key, body string) error {
		return w.call("POST", path, key, body)
	})
	sc.Step(`^the API posts to "([^"]*)" without an Idempotency-Key and body:$`, func(path, body string) error {
		return w.call("POST", path, "", body)
	})
	sc.Step(`^the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "([^"]*)"$`, func(key string) error {
		if err := w.call("POST", "/checkout", key, checkoutBody); err != nil {
			return err
		}
		w.orderID = "order-" + key // the order id derives from the key
		w.lastKey = key
		return nil
	})
	sc.Step(`^the API checks out the same cart again with the same key$`, func() error {
		return w.call("POST", "/checkout", w.lastKey, checkoutBody)
	})
	registerHTTPAssertions(sc, w)
}

func registerHTTPAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the API answers (\d+)$`, w.apiAnswers)
	sc.Step(`^the API answers (\d+) with problem "([^"]*)"$`, w.apiAnswersProblem)
	sc.Step(`^the API response field "([^"]*)" is "([^"]*)"$`, w.apiFieldIs)
	sc.Step(`^the API response contains no address$`, w.apiHasNoAddress)
	sc.Step(`^the response is marked replayed$`, func() error {
		if w.apiHeader.Get("Idempotency-Replayed") != "true" {
			return fmt.Errorf("Idempotency-Replayed = %q, want true", w.apiHeader.Get("Idempotency-Replayed"))
		}
		return nil
	})
	sc.Step(`^(\d+) "([^"]*)" events were published$`, func(n int, name string) error {
		if got := len(w.published(name)); got != n {
			return fmt.Errorf("%d %s events were published, want %d", got, name, n)
		}
		return nil
	})
}

func (w *world) apiAnswers(want int) error {
	if w.apiStatus != want {
		return fmt.Errorf("API answered %d (%s), want %d", w.apiStatus, truncate(w.apiBody), want)
	}
	return nil
}

func (w *world) apiAnswersProblem(want int, slug string) error {
	if err := w.apiAnswers(want); err != nil {
		return err
	}
	if ct := w.apiHeader.Get("Content-Type"); ct != "application/problem+json" {
		return fmt.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	if got := strings.TrimPrefix(fmt.Sprint(m["type"]), "/problems/"); got != slug {
		return fmt.Errorf("problem type = %q, want %q (%s)", got, slug, truncate(w.apiBody))
	}
	return nil
}

func (w *world) apiFieldIs(path, want string) error {
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: %v is not an object (%s)", path, cur, truncate(w.apiBody))
		}
		cur, ok = obj[part]
		if !ok {
			return fmt.Errorf("answer has no field %q (%s)", path, truncate(w.apiBody))
		}
	}
	if got := fmt.Sprint(cur); got != want {
		return fmt.Errorf("%s = %q, want %q", path, got, want)
	}
	return nil
}

// apiHasNoAddress proves the PII rule on the wire: no ship-to object and none
// of the address strings the scenarios check out with.
func (w *world) apiHasNoAddress() error {
	for _, pii := range []string{"shipTo", "Ana Example", "1 Sample Street", "Austin", "78701"} {
		if strings.Contains(w.apiBody, pii) {
			return fmt.Errorf("answer contains %q: %s", pii, truncate(w.apiBody))
		}
	}
	return nil
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

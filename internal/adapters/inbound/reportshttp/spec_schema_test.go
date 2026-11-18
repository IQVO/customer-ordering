package reportshttp_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/claudioed/customer-ordering/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// The bodies the router answers validate against the response schemas of
// apis/openapi.yaml (types, required fields, nullability, bounds): the wire
// contract is the spec, not the struct tags.
func TestResponsesValidateAgainstTheSpecSchemas(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("../../../../apis/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	asOf := now.Add(-time.Minute)
	full := &fakeReader{
		placed:    []report.PlacedDay{{Day: nov3, Orders: 4, Consolidated: 1, Pickup: 2, Split: 1}},
		cancelled: []report.CancelledDay{{Day: nov3.Add(24 * time.Hour), Orders: 2}}, // a day without placements: null share
		notices: []report.NoticeDay{{Day: nov3, Family: "A", Count: 7}, {Day: nov3, Family: "B", Count: 6}, {Day: nov3, Family: "C", Count: 5},
			{Day: nov3, Family: "D", Count: 4}, {Day: nov3, Family: "E", Count: 3}, {Day: nov3, Family: "F", Count: 2}},
		last: &asOf,
	}
	cases := []struct {
		name, path string
		reader     *fakeReader
	}{
		{"a full report", "/reports/ordering-performance", full},
		{"an empty report", "/reports/ordering-performance", &fakeReader{}},
		{"freshness", "/reports/freshness", full},
		{"freshness before the first event", "/reports/freshness", &fakeReader{}},
	}
	for _, c := range cases {
		code, _, body := serve(t, &reportshttp.Server{Reader: c.reader}, http.MethodGet, c.path)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", c.name, code)
		}
		op := doc.Paths.Find(c.path).Get
		schema := op.Responses.Status(http.StatusOK).Value.Content.Get("application/json").Schema.Value
		if err := schema.VisitJSON(body); err != nil {
			t.Errorf("%s: the body does not match the spec: %v", c.name, err)
		}
	}
}

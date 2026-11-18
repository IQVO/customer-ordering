package bdd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	kafkago "github.com/segmentio/kafka-go"

	inboundkafka "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
	"github.com/claudioed/customer-ordering/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// reportsNow is "now" of the reports under test: the day after the scenarios'
// t0, so the freshness lag is the time since the last event.
var reportsNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// analyticsWorld is the analytics read side of one scenario: the REAL encoder
// writes the CloudEvents of the use cases' events, the REAL projector consumer
// decodes and projects them into the in-memory twin of the analytical store,
// and the REAL reports router answers over it. Nothing is stubbed in between.
type analyticsWorld struct {
	store    *analyticsstore.MemoryStore
	consumer *inboundkafka.AnalyticsConsumer
	fed      int            // the outbox events already encoded and read
	pending  []shared.Event // events injected straight into the encoder
	messages [][]byte       // everything the projector has read, for the replay step
	status   int
	raw      string
	body     map[string]any
}

func newAnalyticsWorld() *analyticsWorld {
	store := analyticsstore.NewMemoryStore()
	return &analyticsWorld{store: store, consumer: &inboundkafka.AnalyticsConsumer{
		Projection: store, DLQ: discardDLQ{}, Topic: inboundkafka.OwnEventsTopic, DLQTopic: inboundkafka.AnalyticsDLQTopic,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

type discardDLQ struct{}

// WriteMessages fails the scenario: every event the real publisher writes must
// be projectable, so a dead letter here is a contract break between the two.
func (discardDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	cause := ""
	for _, h := range msgs[0].Headers {
		if h.Key == "x-dlq-error" {
			cause = string(h.Value)
		}
	}
	return fmt.Errorf("a published event was dead-lettered by the analytics projector: %s", cause)
}
func (discardDLQ) Close() error { return nil }

// read encodes the events that have not been read yet and hands each message to
// the projector exactly as the Kafka loop would.
func (a *analyticsWorld) read(w *world) error {
	events := w.store.Outbox().Events()
	fresh := append(append([]shared.Event{}, events[a.fed:]...), a.pending...)
	a.fed, a.pending = len(events), nil
	msgs, err := kafka.NewEncoder().Encode(fresh...)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		a.messages = append(a.messages, m.Value)
		if err := a.consumer.HandleMessage(context.Background(), kafkago.Message{Topic: inboundkafka.OwnEventsTopic, Key: m.Key, Value: m.Value}); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) requestReport(from, to string) error {
	srv := &reportshttp.Server{Reader: w.analytics.store, Now: func() time.Time { return reportsNow }}
	router := reportshttp.NewRouter(srv, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	target := "/reports/ordering-performance?from=" + from + "&to=" + to
	router.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	w.analytics.status, w.analytics.raw, w.analytics.body = rec.Code, rec.Body.String(), nil
	return json.Unmarshal(rec.Body.Bytes(), &w.analytics.body)
}

func (w *world) reportDays() ([]map[string]any, error) {
	raw, _ := w.analytics.body["days"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, d := range raw {
		out = append(out, d.(map[string]any))
	}
	if w.analytics.status != http.StatusOK {
		return nil, fmt.Errorf("the report answered %d: %s", w.analytics.status, w.analytics.raw)
	}
	return out, nil
}

func (w *world) reportDay(day string) (map[string]any, error) {
	days, err := w.reportDays()
	if err != nil {
		return nil, err
	}
	for _, d := range days {
		if d["day"] == day {
			return d, nil
		}
	}
	return nil, fmt.Errorf("the report has no day %s: %s", day, w.analytics.raw)
}

func (w *world) reportShows(day string, t *godog.Table) error {
	d, err := w.reportDay(day)
	if err != nil {
		return err
	}
	for _, row := range t.Rows {
		field, want := row.Cells[0].Value, row.Cells[1].Value
		got, ok := d[field]
		if !ok {
			return fmt.Errorf("day %s has no field %q: %v", day, field, d)
		}
		if render(got) != want {
			return fmt.Errorf("day %s %s = %s, want %s", day, field, render(got), want)
		}
	}
	return nil
}

// render prints a JSON value the way the feature files write it.
func render(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func (w *world) reportListsKinds(day, kinds string, other int) error {
	d, err := w.reportDay(day)
	if err != nil {
		return err
	}
	var got []string
	for _, k := range d["shopper_notices_by_kind"].([]any) {
		m := k.(map[string]any)
		got = append(got, fmt.Sprintf("%s:%s", m["kind"], render(m["count"])))
	}
	if strings.Join(got, ",") != kinds || render(d["shopper_notices_other"]) != strconv.Itoa(other) {
		return fmt.Errorf("day %s lists %v and %s others, want %s and %d", day, got, render(d["shopper_notices_other"]), kinds, other)
	}
	return nil
}

// noPII is what the scenarios put in the OLTP events and must never reach a report.
func (w *world) reportNamesNoOne() error {
	for _, s := range []string{"Ana Example", "Sample Street", "Austin", "78701", "787", "shopper-1", "pp-77", "changed my mind", "consolidationKey"} {
		if strings.Contains(w.analytics.raw, s) {
			return fmt.Errorf("the report body contains %q: %s", s, w.analytics.raw)
		}
	}
	return nil
}

func (w *world) placeInGroups(shopper, order string, groups int) {
	in := w.cart("78701", twoGroups()...)
	in.OrderID, in.ShopperID, w.orderID = customerorder.ID(order), shopper, order
	in.ShipTo = address("78701")
	if groups == 1 {
		in.Groups = []usecases.CheckoutGroup{{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1, 2, 3}}}
	}
	w.checkout(in)
}

func (w *world) injectNotices(at string, t *godog.Table) error {
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return err
	}
	seq := 0
	for _, row := range t.Rows[1:] {
		n, err := strconv.Atoi(row.Cells[1].Value)
		if err != nil {
			return err
		}
		for range n {
			seq++
			w.analytics.pending = append(w.analytics.pending, notice.ShopperNoticeDecided{
				Header:  shared.Header{Version: 1, At: when},
				OrderID: fmt.Sprintf("order-n%d", seq), GroupNo: 1, Family: notice.Family(row.Cells[0].Value), Notifier: notice.NotifierNobody,
				NoticeKey: fmt.Sprintf("key-%d", seq), DecidedAt: when,
			})
		}
	}
	return nil
}

func registerAnalytics(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the analytics projector has read the published events$`, func() error { return w.analytics.read(w) })
	sc.Step(`^the analytics projector reads every published event again$`, func() error {
		for _, m := range w.analytics.messages {
			if err := w.analytics.consumer.HandleMessage(context.Background(), kafkago.Message{Topic: inboundkafka.OwnEventsTopic, Value: m}); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the ordering performance report is requested from "([^"]*)" to "([^"]*)"$`, w.requestReport)
	sc.Step(`^the report answers 200 for "([^"]*)" to "([^"]*)"$`, func(from, to string) error {
		if w.analytics.status != http.StatusOK || w.analytics.body["from"] != from || w.analytics.body["to"] != to {
			return fmt.Errorf("status %d range %v..%v", w.analytics.status, w.analytics.body["from"], w.analytics.body["to"])
		}
		return nil
	})
	sc.Step(`^the report answers 400 with problem "([^"]*)"$`, func(problem string) error {
		if w.analytics.status != http.StatusBadRequest || w.analytics.body["type"] != problem {
			return fmt.Errorf("status %d body %s", w.analytics.status, w.analytics.raw)
		}
		return nil
	})
	sc.Step(`^the report has no days$`, func() error { return w.reportHasDays(0) })
	sc.Step(`^the report has (\d+) days?$`, w.reportHasDays)
	sc.Step(`^the report day "([^"]*)" shows:$`, w.reportShows)
	sc.Step(`^the report day "([^"]*)" lists the notice kinds "([^"]*)" and (\d+) others?$`, w.reportListsKinds)
	sc.Step(`^the report freshness is unknown$`, func() error {
		f, _ := w.analytics.body["freshness"].(map[string]any)
		if f["as_of"] != nil || f["lag_seconds"] != nil || len(f) != 2 {
			return fmt.Errorf("freshness = %v, want explicit nulls", w.analytics.body["freshness"])
		}
		return nil
	})
	sc.Step(`^the report freshness is (\d+) seconds behind$`, func(sec int) error {
		f, _ := w.analytics.body["freshness"].(map[string]any)
		if f["lag_seconds"] != float64(sec) {
			return fmt.Errorf("freshness = %v, want %d seconds behind", f, sec)
		}
		return nil
	})
	sc.Step(`^the report body names no shopper, address, postal prefix or free text$`, w.reportNamesNoOne)
	sc.Step(`^the shopper "([^"]*)" places order "([^"]*)" in (\d+) groups?$`, func(shopper, order string, groups int) error {
		w.placeInGroups(shopper, order, groups)
		return w.lastErr
	})
	sc.Step(`^the clock moves to "([^"]*)"$`, func(at string) error {
		t, err := time.Parse(time.RFC3339, at)
		if err == nil {
			w.clock.Set(t)
		}
		return err
	})
	sc.Step(`^shopper notices are decided on "([^"]*)" with the counts:$`, w.injectNotices)
}

func (w *world) reportHasDays(n int) error {
	days, err := w.reportDays()
	if err != nil {
		return err
	}
	if len(days) != n {
		return fmt.Errorf("the report has %d days, want %d: %s", len(days), n, w.analytics.raw)
	}
	return nil
}

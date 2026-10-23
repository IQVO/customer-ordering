package kafka

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/customer-ordering/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/customer-ordering/internal/application/outbox"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// Topic is this service's integration-event topic.
const Topic = "retailnetwork.customer-ordering.events"

// schemaVersion is the dataschema version of every payload below.
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, camelCase JSON) pinned by
// apis/asyncapi.yaml. Timestamps are RFC 3339 UTC; arrays are always present.

type lineData struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type placedGroupData struct {
	GroupNo           int        `json:"groupNo"`
	PromiseID         string     `json:"promiseId"`
	Tier              string     `json:"tier"`
	WitnessNodeID     string     `json:"witnessNodeId"`
	ShipBy            string     `json:"shipBy"`
	DeliverByEarliest string     `json:"deliverByEarliest"`
	DeliverByLatest   string     `json:"deliverByLatest"`
	Lines             []lineData `json:"lines"`
}

type placedData struct {
	OrderID                 string            `json:"orderId"`
	ShopperID               string            `json:"shopperId"`
	DestinationPostalPrefix string            `json:"destinationPostalPrefix"`
	PlacedAt                string            `json:"placedAt"`
	Groups                  []placedGroupData `json:"groups"`
}

type cancelledData struct {
	OrderID     string `json:"orderId"`
	Reason      string `json:"reason"`
	CancelledAt string `json:"cancelledAt"`
	GroupNos    []int  `json:"groupNos"`
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Encoder implements outbox.Encoder: each domain event becomes one outbox
// message holding the CloudEvents bytes, the Kafka key (the orderId) and the
// content-type header. The CloudEvents `id` is minted HERE, once, and persisted
// with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ outbox.Encoder = (*Encoder)(nil)

// Encode encodes events in order. An event type it does not publish is a
// programming error and fails the whole call.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		msg, err := buildMessage(ev, newID(), data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func buildMessage(ev shared.Event, id string, data any) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(), Time: ev.OccurredAt(),
		Stream: cloudevents.StreamEvents, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID: id, Topic: Topic, EventType: cloudevents.Type(ev.Entity(), ev.EventName()), Subject: ev.SubjectID(),
		Key: []byte(ev.SubjectID()), DataSchema: cloudevents.DataSchema(cloudevents.StreamEvents, ev.EventName(), schemaVersion),
		Value: value, Headers: []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

func payloadFor(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case customerorder.CustomerOrderPlaced:
		return placedPayload(e), nil
	case customerorder.CustomerOrderCancelled:
		return cancelledData{OrderID: string(e.OrderID), Reason: e.Reason, CancelledAt: ts(e.CancelledAt), GroupNos: append([]int{}, e.GroupNos...)}, nil
	}
	return nil, fmt.Errorf("kafka encoder: %T is not a published event", ev)
}

func placedPayload(e customerorder.CustomerOrderPlaced) placedData {
	groups := make([]placedGroupData, len(e.Groups))
	for i, g := range e.Groups {
		lines := make([]lineData, len(g.Lines))
		for j, l := range g.Lines {
			lines[j] = lineData{LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
		}
		groups[i] = placedGroupData{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: string(g.Tier), WitnessNodeID: g.WitnessNodeID, ShipBy: ts(g.ShipBy),
			DeliverByEarliest: ts(g.DeliverByEarliest), DeliverByLatest: ts(g.DeliverByLatest), Lines: lines,
		}
	}
	return placedData{
		OrderID: string(e.OrderID), ShopperID: e.ShopperID, DestinationPostalPrefix: e.DestinationPostalPrefix, PlacedAt: ts(e.PlacedAt), Groups: groups,
	}
}

// Headers converts the stored outbox headers to Kafka headers.
func Headers(hs []outbox.Header) []kafkago.Header {
	out := make([]kafkago.Header, len(hs))
	for i, h := range hs {
		out[i] = kafkago.Header{Key: h.Key, Value: []byte(h.Value)}
	}
	return out
}

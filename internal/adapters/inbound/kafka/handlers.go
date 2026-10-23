package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// Consumed topics: the producers' integration topics (their contracts are
// restated in apis/asyncapi.yaml, never imported).
const (
	DeliveryPromiseTopic       = "retailnetwork.delivery-promise.events"
	OrderSourcingTopic         = "retailnetwork.order-sourcing.events"
	ShippingAndTrackingTopic   = "retailnetwork.shipping-and-tracking.events"
	typePrefixDeliveryPromise  = "com.retailnetwork.commerce.delivery-promise.promise."
	typePrefixOrderSourcing    = "com.retailnetwork.commerce.order-sourcing.assignment."
	typePrefixShippingLabel    = "com.retailnetwork.logistics.shipping-and-tracking.label."
	typePrefixShippingJourney  = "com.retailnetwork.logistics.shipping-and-tracking.journey."
	maxFieldLength             = 200
	maxTrackingNumbersPerGroup = 500
)

// FULL CloudEvents types of the consumed events, byte-identical to the
// producers' contracts.
const (
	TypePromiseAtRisk           = typePrefixDeliveryPromise + "PromiseAtRisk"
	TypePromiseRevised          = typePrefixDeliveryPromise + "PromiseRevised"
	TypePromiseKept             = typePrefixDeliveryPromise + "PromiseKept"
	TypePromiseBroken           = typePrefixDeliveryPromise + "PromiseBroken"
	TypePromiseUnkeepable       = typePrefixDeliveryPromise + "PromiseUnkeepable"
	TypeFulfillmentLocked       = typePrefixOrderSourcing + "FulfillmentLocked"
	TypeFulfillmentShipped      = typePrefixOrderSourcing + "FulfillmentShipped"
	TypeFulfillmentUnsourceable = typePrefixOrderSourcing + "FulfillmentUnsourceable"
	TypeFulfillmentCancelled    = typePrefixOrderSourcing + "FulfillmentCancelled"
	TypeFulfillmentCancelRefuse = typePrefixOrderSourcing + "FulfillmentCancelRefused"
	TypeLabelIssued             = typePrefixShippingLabel + "LabelIssued"
	TypeLabelVoided             = typePrefixShippingLabel + "LabelVoided"
	TypePackageScanned          = typePrefixShippingJourney + "PackageScanned"
	TypePackageDelivered        = typePrefixShippingJourney + "PackageDelivered"
	TypePackageException        = typePrefixShippingJourney + "PackageExceptionRaised"
	TypePackageLost             = typePrefixShippingJourney + "PackageLost"
	TypePackageReturned         = typePrefixShippingJourney + "PackageReturned"
)

// Payloads restate the producers' v1 `data` (camelCase JSON); unknown fields
// are ignored so a producer may add fields without breaking us.
type (
	promiseAtRiskData struct {
		PromiseID string `json:"promiseId"`
		OrderID   string `json:"orderId"`
		Reason    string `json:"reason"`
	}
	promiseRevisedData struct {
		PromiseID         string    `json:"promiseId"`
		OrderID           string    `json:"orderId"`
		RevisionNo        int       `json:"revisionNo"`
		DeliverByEarliest time.Time `json:"deliverByEarliest"`
		DeliverByLatest   time.Time `json:"deliverByLatest"`
		ShipBy            time.Time `json:"shipBy"`
		NodeID            string    `json:"nodeId"`
	}
	promiseKeptData struct {
		PromiseID string `json:"promiseId"`
		OrderID   string `json:"orderId"`
	}
	promiseBrokenData struct {
		PromiseID     string `json:"promiseId"`
		OrderID       string `json:"orderId"`
		LateByMinutes *int   `json:"lateByMinutes"`
	}
	promiseUnkeepableData struct {
		PromiseID string `json:"promiseId"`
		OrderID   string `json:"orderId"`
		GroupNo   int    `json:"groupNo"`
		Reason    string `json:"reason"`
	}
	fulfillmentLockedData struct {
		AssignmentID string    `json:"assignmentId"`
		OrderID      string    `json:"orderId"`
		GroupNo      int       `json:"groupNo"`
		NodeID       string    `json:"nodeId"`
		PromiseID    string    `json:"promiseId"`
		LockedAt     time.Time `json:"lockedAt"`
	}
	fulfillmentShippedData struct {
		AssignmentID    string    `json:"assignmentId"`
		OrderID         string    `json:"orderId"`
		GroupNo         int       `json:"groupNo"`
		TrackingNumbers []string  `json:"trackingNumbers"`
		ShippedAt       time.Time `json:"shippedAt"`
	}
	fulfillmentReasonData struct {
		OrderID string `json:"orderId"`
		GroupNo int    `json:"groupNo"`
		Reason  string `json:"reason"`
	}
	labelIssuedData struct {
		TrackingNumber string `json:"trackingNumber"`
		AssignmentID   string `json:"assignmentId"`
		Carrier        string `json:"carrier"`
	}
	labelVoidedData struct {
		TrackingNumber string `json:"trackingNumber"`
	}
	packageScannedData struct {
		TrackingNumber string    `json:"trackingNumber"`
		NodeID         string    `json:"nodeId"`
		State          string    `json:"state"`
		At             time.Time `json:"at"`
	}
	packageDeliveredData struct {
		TrackingNumber string    `json:"trackingNumber"`
		AssignmentID   string    `json:"assignmentId"`
		OrderID        string    `json:"orderId"`
		DeliveredAt    time.Time `json:"deliveredAt"`
	}
	packageLostData struct {
		TrackingNumber string    `json:"trackingNumber"`
		OrderID        string    `json:"orderId"`
		DeclaredAt     time.Time `json:"declaredAt"`
	}
	packageReturnedData struct {
		TrackingNumber string    `json:"trackingNumber"`
		OrderID        string    `json:"orderId"`
		ReturnedAt     time.Time `json:"returnedAt"`
	}
	packageExceptionData struct {
		TrackingNumber string    `json:"trackingNumber"`
		AssignmentID   string    `json:"assignmentId"`
		OrderID        string    `json:"orderId"`
		Reason         string    `json:"reason"`
		At             time.Time `json:"at"`
	}
)

// meta is the identity of the consumed CloudEvent.
func meta(e ce.Event) usecases.Meta {
	return usecases.Meta{EventID: e.ID(), Type: e.Type(), At: e.Time().UTC()}
}

// need returns an errBadPayload naming the first empty required field.
func need(what string, pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			return fmt.Errorf("%w: %s.%s is required", errBadPayload, what, pairs[i])
		}
		if len(pairs[i+1]) > maxFieldLength {
			return fmt.Errorf("%w: %s.%s is longer than %d characters", errBadPayload, what, pairs[i], maxFieldLength)
		}
	}
	return nil
}

func needGroup(what string, groupNo int) error {
	if groupNo < 1 {
		return fmt.Errorf("%w: %s.groupNo must be at least 1", errBadPayload, what)
	}
	return nil
}

// handler decodes the payload of an event, validates it and applies it.
func handler[T any](what string, validate func(T) error, apply func(ctx context.Context, m usecases.Meta, d T) error) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[T](e)
		if err != nil {
			return err
		}
		if err := validate(d); err != nil {
			return err
		}
		if err := apply(ctx, meta(e), d); err != nil {
			return err
		}
		slog.DebugContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "what", what)
		return nil
	}
}

// DeliveryPromiseHandlers maps delivery-promise's events to the consumer use cases.
func DeliveryPromiseHandlers(c usecases.Consumer) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypePromiseAtRisk: handler("PromiseAtRisk", func(d promiseAtRiskData) error {
			return need("PromiseAtRisk", "promiseId", d.PromiseID, "orderId", d.OrderID)
		}, func(ctx context.Context, m usecases.Meta, d promiseAtRiskData) error {
			return c.ApplyPromiseAtRisk(ctx, m, usecases.PromiseAtRisk{PromiseID: d.PromiseID, OrderID: d.OrderID, Reason: d.Reason})
		}),
		TypePromiseRevised: handler("PromiseRevised", func(d promiseRevisedData) error {
			if err := need("PromiseRevised", "promiseId", d.PromiseID, "orderId", d.OrderID); err != nil {
				return err
			}
			if d.RevisionNo < 1 || d.DeliverByEarliest.IsZero() || d.DeliverByLatest.IsZero() {
				return fmt.Errorf("%w: PromiseRevised needs revisionNo >= 1 and the new window", errBadPayload)
			}
			return nil
		}, func(ctx context.Context, m usecases.Meta, d promiseRevisedData) error {
			return c.ApplyPromiseRevised(ctx, m, usecases.PromiseRevised{
				PromiseID: d.PromiseID, OrderID: d.OrderID, RevisionNo: d.RevisionNo, DeliverByEarliest: d.DeliverByEarliest,
				DeliverByLatest: d.DeliverByLatest, ShipBy: d.ShipBy, NodeID: d.NodeID,
			})
		}),
		TypePromiseKept: handler("PromiseKept", func(d promiseKeptData) error {
			return need("PromiseKept", "promiseId", d.PromiseID, "orderId", d.OrderID)
		}, func(ctx context.Context, m usecases.Meta, d promiseKeptData) error {
			return c.ApplyPromiseKept(ctx, m, usecases.PromiseKept{PromiseID: d.PromiseID, OrderID: d.OrderID})
		}),
		TypePromiseBroken: handler("PromiseBroken", func(d promiseBrokenData) error {
			return need("PromiseBroken", "promiseId", d.PromiseID, "orderId", d.OrderID)
		}, func(ctx context.Context, m usecases.Meta, d promiseBrokenData) error {
			return c.ApplyPromiseBroken(ctx, m, usecases.PromiseBroken{PromiseID: d.PromiseID, OrderID: d.OrderID, LateByMinutes: d.LateByMinutes})
		}),
		TypePromiseUnkeepable: handler("PromiseUnkeepable", func(d promiseUnkeepableData) error {
			if err := need("PromiseUnkeepable", "promiseId", d.PromiseID, "orderId", d.OrderID); err != nil {
				return err
			}
			return needGroup("PromiseUnkeepable", d.GroupNo)
		}, func(ctx context.Context, m usecases.Meta, d promiseUnkeepableData) error {
			return c.ApplyPromiseUnkeepable(ctx, m, usecases.PromiseUnkeepable{PromiseID: d.PromiseID, OrderID: d.OrderID, GroupNo: d.GroupNo, Reason: d.Reason})
		}),
	}
}

func validReason(what string) func(fulfillmentReasonData) error {
	return func(d fulfillmentReasonData) error {
		if err := need(what, "orderId", d.OrderID); err != nil {
			return err
		}
		return needGroup(what, d.GroupNo)
	}
}

// OrderSourcingHandlers maps order-sourcing's events to the consumer use cases.
func OrderSourcingHandlers(c usecases.Consumer) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeFulfillmentLocked: handler("FulfillmentLocked", func(d fulfillmentLockedData) error {
			if err := need("FulfillmentLocked", "assignmentId", d.AssignmentID, "orderId", d.OrderID); err != nil {
				return err
			}
			return needGroup("FulfillmentLocked", d.GroupNo)
		}, func(ctx context.Context, m usecases.Meta, d fulfillmentLockedData) error {
			return c.ApplyFulfillmentLocked(ctx, m, usecases.FulfillmentLocked{
				AssignmentID: d.AssignmentID, OrderID: d.OrderID, GroupNo: d.GroupNo, NodeID: d.NodeID, PromiseID: d.PromiseID, LockedAt: d.LockedAt,
			})
		}),
		TypeFulfillmentShipped: handler("FulfillmentShipped", func(d fulfillmentShippedData) error {
			if err := need("FulfillmentShipped", "assignmentId", d.AssignmentID, "orderId", d.OrderID); err != nil {
				return err
			}
			if len(d.TrackingNumbers) > maxTrackingNumbersPerGroup {
				return fmt.Errorf("%w: FulfillmentShipped carries %d tracking numbers", errBadPayload, len(d.TrackingNumbers))
			}
			return needGroup("FulfillmentShipped", d.GroupNo)
		}, func(ctx context.Context, m usecases.Meta, d fulfillmentShippedData) error {
			return c.ApplyFulfillmentShipped(ctx, m, usecases.FulfillmentShipped{
				AssignmentID: d.AssignmentID, OrderID: d.OrderID, GroupNo: d.GroupNo, TrackingNumbers: d.TrackingNumbers, ShippedAt: d.ShippedAt,
			})
		}),
		TypeFulfillmentUnsourceable: handler("FulfillmentUnsourceable", validReason("FulfillmentUnsourceable"),
			func(ctx context.Context, m usecases.Meta, d fulfillmentReasonData) error {
				return c.ApplyFulfillmentUnsourceable(ctx, m, usecases.FulfillmentUnsourceable{OrderID: d.OrderID, GroupNo: d.GroupNo, Reason: d.Reason})
			}),
		TypeFulfillmentCancelled: handler("FulfillmentCancelled", validReason("FulfillmentCancelled"),
			func(ctx context.Context, m usecases.Meta, d fulfillmentReasonData) error {
				return c.ApplyFulfillmentCancelled(ctx, m, usecases.FulfillmentCancelled{OrderID: d.OrderID, GroupNo: d.GroupNo})
			}),
		TypeFulfillmentCancelRefuse: handler("FulfillmentCancelRefused", validReason("FulfillmentCancelRefused"),
			func(ctx context.Context, m usecases.Meta, d fulfillmentReasonData) error {
				return c.ApplyFulfillmentCancelRefused(ctx, m, usecases.FulfillmentCancelRefused{OrderID: d.OrderID, GroupNo: d.GroupNo, Reason: d.Reason})
			}),
	}
}

func tracking(what string) func(string) error {
	return func(tn string) error { return need(what, "trackingNumber", tn) }
}

// ShippingAndTrackingHandlers maps shipping-and-tracking's events to the consumer use cases.
func ShippingAndTrackingHandlers(c usecases.Consumer) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeLabelIssued: handler("LabelIssued", func(d labelIssuedData) error {
			return tracking("LabelIssued")(d.TrackingNumber)
		}, func(ctx context.Context, m usecases.Meta, d labelIssuedData) error {
			return c.ApplyLabelIssued(ctx, m, usecases.LabelIssued{TrackingNumber: d.TrackingNumber, AssignmentID: d.AssignmentID, Carrier: d.Carrier})
		}),
		TypeLabelVoided: handler("LabelVoided", func(d labelVoidedData) error {
			return tracking("LabelVoided")(d.TrackingNumber)
		}, func(ctx context.Context, m usecases.Meta, d labelVoidedData) error {
			return c.ApplyLabelVoided(ctx, m, usecases.LabelVoided{TrackingNumber: d.TrackingNumber})
		}),
		TypePackageScanned: handler("PackageScanned", func(d packageScannedData) error {
			return tracking("PackageScanned")(d.TrackingNumber)
		}, func(ctx context.Context, m usecases.Meta, d packageScannedData) error {
			return c.ApplyPackageScanned(ctx, m, usecases.PackageScanned{TrackingNumber: d.TrackingNumber, NodeID: d.NodeID, State: d.State, At: d.At})
		}),
		TypePackageDelivered: handler("PackageDelivered", func(d packageDeliveredData) error {
			return tracking("PackageDelivered")(d.TrackingNumber)
		}, func(ctx context.Context, m usecases.Meta, d packageDeliveredData) error {
			return c.ApplyPackageDelivered(ctx, m, usecases.PackageDelivered{
				TrackingNumber: d.TrackingNumber, AssignmentID: d.AssignmentID, OrderID: d.OrderID, DeliveredAt: d.DeliveredAt,
			})
		}),
		TypePackageLost: handler("PackageLost", func(d packageLostData) error {
			return need("PackageLost", "trackingNumber", d.TrackingNumber, "orderId", d.OrderID)
		}, func(ctx context.Context, m usecases.Meta, d packageLostData) error {
			return c.ApplyPackageLost(ctx, m, usecases.PackageLost{TrackingNumber: d.TrackingNumber, OrderID: d.OrderID, DeclaredAt: d.DeclaredAt})
		}),
		TypePackageReturned: handler("PackageReturned", func(d packageReturnedData) error {
			return need("PackageReturned", "trackingNumber", d.TrackingNumber, "orderId", d.OrderID)
		}, func(ctx context.Context, m usecases.Meta, d packageReturnedData) error {
			return c.ApplyPackageReturned(ctx, m, usecases.PackageReturned{TrackingNumber: d.TrackingNumber, OrderID: d.OrderID, ReturnedAt: d.ReturnedAt})
		}),
		TypePackageException: handler("PackageExceptionRaised", func(d packageExceptionData) error {
			return tracking("PackageExceptionRaised")(d.TrackingNumber)
		}, func(ctx context.Context, m usecases.Meta, d packageExceptionData) error {
			return c.ApplyPackageException(ctx, m, usecases.PackageExceptionRaised{
				TrackingNumber: d.TrackingNumber, AssignmentID: d.AssignmentID, OrderID: d.OrderID, Reason: d.Reason, At: d.At,
			})
		}),
	}
}

// NewDeliveryPromiseConsumer reads delivery-promise's topic under groupID
// (DELIVERY_PROMISE_CONSUMER_GROUP at the composition root, never a literal).
func NewDeliveryPromiseConsumer(brokers []string, groupID string, c usecases.Consumer, logger *slog.Logger) *Consumer {
	return NewConsumerForTopic("delivery-promise consumer", brokers, DeliveryPromiseTopic, groupID, DeliveryPromiseHandlers(c), logger)
}

// NewOrderSourcingConsumer reads order-sourcing's topic under groupID
// (ORDER_SOURCING_CONSUMER_GROUP at the composition root).
func NewOrderSourcingConsumer(brokers []string, groupID string, c usecases.Consumer, logger *slog.Logger) *Consumer {
	return NewConsumerForTopic("order-sourcing consumer", brokers, OrderSourcingTopic, groupID, OrderSourcingHandlers(c), logger)
}

// NewShippingAndTrackingConsumer reads shipping-and-tracking's topic under
// groupID (SHIPPING_CONSUMER_GROUP at the composition root).
func NewShippingAndTrackingConsumer(brokers []string, groupID string, c usecases.Consumer, logger *slog.Logger) *Consumer {
	return NewConsumerForTopic("shipping-and-tracking consumer", brokers, ShippingAndTrackingTopic, groupID, ShippingAndTrackingHandlers(c), logger)
}

// NewConsumerForTopic builds a consumer on an explicit topic (tests isolate
// theirs). A payload that can never be applied is dead-lettered at once.
func NewConsumerForTopic(name string, brokers []string, topic, groupID string, handlers map[string]HandlerFunc, logger *slog.Logger) *Consumer {
	c := newConsumer(name, brokers, topic, groupID, handlers, logger)
	c.DeadLetterInvalid = true
	return c
}

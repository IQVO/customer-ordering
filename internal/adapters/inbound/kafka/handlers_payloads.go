package kafka

import (
	"time"
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
		TimeZone          string    `json:"timeZone"`
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

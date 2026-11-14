package http

import (
	"net/http"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// notificationPolicyDTO is the wire shape of the document (apis/openapi.yaml).
type notificationPolicyDTO struct {
	Version int               `json:"version"`
	Rows    []notificationRow `json:"rows"`
	// PublishedAt is absent on version 0, the built-in default.
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

// notificationRow is one family's decision.
type notificationRow struct {
	Family   string `json:"family"`
	Notifier string `json:"notifier"`
}

// putNotificationPolicyRequest is the body of PUT /notification-policy: the
// whole document, one row per family (ADR 0017).
type putNotificationPolicyRequest struct {
	Rows []notificationRow `json:"rows"`
}

func toNotificationPolicyDTO(v views.NotificationPolicy) notificationPolicyDTO {
	rows := make([]notificationRow, len(v.Rows))
	for i, r := range v.Rows {
		rows[i] = notificationRow{Family: string(r.Family), Notifier: string(r.Notifier)}
	}
	var at *time.Time
	if !v.PublishedAt.IsZero() {
		u := v.PublishedAt.UTC()
		at = &u
	}
	return notificationPolicyDTO{Version: v.Version, Rows: rows, PublishedAt: at}
}

func (s *Server) handleGetNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	v, err := s.NotificationPolicies.Current(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toNotificationPolicyDTO(v))
}

func (s *Server) handlePutNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	var body putNotificationPolicyRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	rows := make([]notice.Row, len(body.Rows))
	for i, row := range body.Rows {
		rows[i] = notice.Row{Family: notice.Family(row.Family), Notifier: notice.Notifier(row.Notifier)}
	}
	v, err := s.NotificationPolicies.Publish(r.Context(), rows)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toNotificationPolicyDTO(v))
}

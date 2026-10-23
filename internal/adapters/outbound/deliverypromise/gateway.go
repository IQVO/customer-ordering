// Package deliverypromise is the outbound HTTP client of delivery-promise, the
// only synchronous in-organization call customer-ordering makes (the model's
// sync_calls QuoteDeliveryOffers and MakePromise, plus ConfirmPromise, E7, and
// the compensating CancelPromise). It
// restates the four routes it uses and nothing else; the DTOs here are not
// shared with the service.
package deliverypromise

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
)

// DefaultTimeout bounds one call; a slower delivery-promise is unavailable.
const DefaultTimeout = 5 * time.Second

// maxResponseBytes bounds what the client reads of one response.
const maxResponseBytes = 1 << 20

// Gateway implements ports.PromiseGateway over HTTP.
type Gateway struct {
	base   string
	client *http.Client
}

var _ ports.PromiseGateway = (*Gateway)(nil)

// New builds a Gateway for the delivery-promise base URL (no trailing slash
// needed). A nil client gets one with DefaultTimeout.
func New(baseURL string, client *http.Client) *Gateway {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &Gateway{base: strings.TrimRight(baseURL, "/"), client: client}
}

type lineDTO struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

func linesDTO(in []promising.Line) []lineDTO {
	out := make([]lineDTO, len(in))
	for i, l := range in {
		out[i] = lineDTO(l)
	}
	return out
}

type windowDTO struct {
	Earliest time.Time `json:"earliest"`
	Latest   time.Time `json:"latest"`
}

type offerDTO struct {
	OfferID   string    `json:"offerId"`
	State     string    `json:"state"`
	Tier      string    `json:"tier"`
	Window    windowDTO `json:"window"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type quoteResponse struct {
	Offers []offerDTO `json:"offers"`
}

type promiseDTO struct {
	PromiseID               string `json:"promiseId"`
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
	Terms                   struct {
		Tier              string    `json:"tier"`
		ShipBy            time.Time `json:"shipBy"`
		DeliverByEarliest time.Time `json:"deliverByEarliest"`
		DeliverByLatest   time.Time `json:"deliverByLatest"`
	} `json:"terms"`
	WitnessPath struct {
		NodeID string `json:"nodeId"`
	} `json:"witnessPath"`
}

type problemDTO struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// QuoteOffers implements ports.PromiseGateway: POST /delivery-offers:quote. Only
// QUOTED offers are returned.
func (g *Gateway) QuoteOffers(ctx context.Context, destinationPostalPrefix string, lines []promising.Line) ([]promising.Offer, error) {
	var resp quoteResponse
	body := map[string]any{"destinationPostalPrefix": destinationPostalPrefix, "lines": linesDTO(lines)}
	if err := g.post(ctx, "/delivery-offers:quote", "", body, &resp); err != nil {
		return nil, err
	}
	out := make([]promising.Offer, 0, len(resp.Offers))
	for _, o := range resp.Offers {
		if o.State != "" && o.State != "QUOTED" {
			continue
		}
		out = append(out, promising.Offer{OfferID: o.OfferID, Tier: o.Tier, DeliverByEarliest: o.Window.Earliest.UTC(), DeliverByLatest: o.Window.Latest.UTC(), ExpiresAt: o.ExpiresAt.UTC()})
	}
	return out, nil
}

// MakePromise implements ports.PromiseGateway: POST /promises with the
// Idempotency-Key "<orderId>:<groupNo>", so a retried checkout re-uses the promise.
func (g *Gateway) MakePromise(ctx context.Context, req promising.MakeRequest) (promising.Terms, error) {
	var p promiseDTO
	key := fmt.Sprintf("%s:%d", req.OrderID, req.GroupNo)
	if err := g.post(ctx, "/promises", key, map[string]any{"offerId": req.OfferID, "lines": linesDTO(req.Lines)}, &p); err != nil {
		return promising.Terms{}, err
	}
	if p.PromiseID == "" {
		return promising.Terms{}, fmt.Errorf("%w: the promise has no promiseId", ports.ErrPromiseUnavailable)
	}
	return promising.Terms{
		PromiseID: p.PromiseID, Tier: p.Terms.Tier, WitnessNodeID: p.WitnessPath.NodeID, ShipBy: p.Terms.ShipBy.UTC(),
		DeliverByEarliest: p.Terms.DeliverByEarliest.UTC(), DeliverByLatest: p.Terms.DeliverByLatest.UTC(),
		DestinationPostalPrefix: p.DestinationPostalPrefix,
	}, nil
}

// ConfirmPromise implements ports.PromiseGateway: POST /promises/{id}:confirm
// with {orderId, groupNo} and the Idempotency-Key "<orderId>:<groupNo>:confirm".
func (g *Gateway) ConfirmPromise(ctx context.Context, promiseID, orderID string, groupNo int) error {
	key := fmt.Sprintf("%s:%d:confirm", orderID, groupNo)
	return g.post(ctx, "/promises/"+url.PathEscape(promiseID)+":confirm", key, map[string]any{"orderId": orderID, "groupNo": groupNo}, nil)
}

// CancelPromise implements ports.PromiseGateway: POST /promises/{id}:cancel
// with no body and the Idempotency-Key "<orderId>:<groupNo>:cancel". 200 (also
// for a promise that is already CANCELLED) is success; 404 not-found and 409
// illegal-transition (KEPT or BROKEN) are ports.ErrPromiseNotCancellable.
func (g *Gateway) CancelPromise(ctx context.Context, promiseID, orderID string, groupNo int) error {
	key := fmt.Sprintf("%s:%d:cancel", orderID, groupNo)
	err := g.post(ctx, "/promises/"+url.PathEscape(promiseID)+":cancel", key, nil, nil)
	if errors.Is(err, ports.ErrPromiseOfferNotQuotable) {
		return fmt.Errorf("%w: %v", ports.ErrPromiseNotCancellable, err)
	}
	return err
}

// post sends one JSON POST (no body when body is nil) and maps the answer: 2xx
// decodes into out (when non-nil); 404, 409 and 422 are the typed refusals of
// mapRefusal; anything else, a transport error or a timeout is
// ports.ErrPromiseUnavailable.
func (g *Gateway) post(ctx context.Context, path, idempotencyKey string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request to delivery-promise: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.base+path, reader)
	if err != nil {
		return fmt.Errorf("build request to delivery-promise: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, application/problem+json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ports.ErrPromiseUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: read response: %v", ports.ErrPromiseUnavailable, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("%w: undecodable response: %v", ports.ErrPromiseUnavailable, err)
		}
		return nil
	}
	return mapRefusal(resp.StatusCode, payload)
}

// mapRefusal turns a non-2xx answer into the typed error of the port. The
// problem slug (the last segment of `type`) tells the 409/422 flavours apart.
func mapRefusal(status int, payload []byte) error {
	var p problemDTO
	_ = json.Unmarshal(payload, &p)
	slug := p.Type[strings.LastIndex(p.Type, "/")+1:]
	detail := p.Detail
	if detail == "" {
		detail = p.Title
	}
	switch {
	case status == http.StatusConflict && slug == "promise-capacity-exhausted":
		return fmt.Errorf("%w: %s", ports.ErrPromiseCapacityExhausted, detail)
	case status == http.StatusConflict && slug == "book-closed":
		return fmt.Errorf("%w: %s", ports.ErrPromiseBookClosed, detail)
	case status == http.StatusUnprocessableEntity && slug == "group-spans-nodes":
		return fmt.Errorf("%w: %s", ports.ErrPromiseGroupSpansNodes, detail)
	case status == http.StatusNotFound || status == http.StatusConflict || status == http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %d %s", ports.ErrPromiseOfferNotQuotable, status, detail)
	}
	return fmt.Errorf("%w: status %d", ports.ErrPromiseUnavailable, status)
}

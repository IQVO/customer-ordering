package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/customer-ordering/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card). They are the MCP tool
// names of the same reads (ADR 0005), so one name identifies one question
// across both agent-facing surfaces.
const (
	skillGetOrder         = "get_order"
	skillGetOrderTracking = "get_order_tracking"
)

// maxOrderIDLength mirrors the REST adapter's {orderId} guard
// (internal/adapters/inbound/http/handlers.go): an id longer than this can
// never name an order and reads as not-found, never a server error.
const maxOrderIDLength = 64

// skillRequest is the wire contract of every skill call: the first message
// part carries (as a Data part, or a Text part holding the same JSON) the
// skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored. (Go's
// encoding/json matches field names case-insensitively, so "orderid" still
// reaches orderId; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// orderArgs is the argument shape of both skills.
type orderArgs struct {
	OrderID string `json:"orderId"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0008).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: &dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillGetOrder:
		data, problem := deps.getOrder(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{Order: &data, summary: summarizeOrder(data)}, nil
	case skillGetOrderTracking:
		data, problem := deps.getOrderTracking(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{Tracking: &data, summary: summarizeTracking(data)}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part.
// A Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered, not raised, because a
// malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// orderID decodes the shared {orderId} argument and applies the REST
// adapter's id rules: a missing id is a bad request, a NUL character is a
// bad request too (REST: 400 in a body, which this is; Postgres cannot store
// it), and an over-long id can never name an order, so it reads as not-found
// exactly like GET /orders/{orderId}.
func orderID(rawArgs json.RawMessage) (customerorder.ID, *problem) {
	var args orderArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return "", problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	if strings.TrimSpace(args.OrderID) == "" {
		return "", problemInvalidArgument("orderId is required")
	}
	if strings.ContainsRune(args.OrderID, 0) {
		return "", problemInvalidArgument("a NUL character (U+0000) is not allowed in orderId")
	}
	if len(args.OrderID) > maxOrderIDLength {
		return "", problemNotFound("an order id is 1 to 64 characters; this one cannot name an order")
	}
	return customerorder.ID(args.OrderID), nil
}

// getOrder is the order read skill, through the same GetOrder use case the
// REST and MCP adapters use: the order without its ship-to.
func (d Deps) getOrder(ctx context.Context, rawArgs json.RawMessage) (orderDTO, *problem) {
	id, problem := orderID(rawArgs)
	if problem != nil {
		return orderDTO{}, problem
	}
	view, err := d.GetOrder.Execute(ctx, id)
	if err != nil {
		return orderDTO{}, problemFromUseCase(ctx, err, id)
	}
	return toOrderDTO(view), nil
}

// getOrderTracking is the tracking read skill, through the same
// GetOrderTracking use case the REST and MCP adapters use.
func (d Deps) getOrderTracking(ctx context.Context, rawArgs json.RawMessage) (trackingDTO, *problem) {
	id, problem := orderID(rawArgs)
	if problem != nil {
		return trackingDTO{}, problem
	}
	view, err := d.GetOrderTracking.Execute(ctx, id)
	if err != nil {
		return trackingDTO{}, problemFromUseCase(ctx, err, id)
	}
	return toTrackingDTO(view), nil
}

// problemFromUseCase maps a use case error to a problem answer, using the
// SAME classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a read can raise: a missing order is 404
// not-found and anything unrecognised is 500 internal-error. (The REST
// adapter's 409, 422 and 503 classes belong to writes and to the
// delivery-promise gateway, which this read-only agent does not hold.) The
// detail is adapter-owned text, so a shared error's wording never reaches an
// agent client. Domain rejections are EXPECTED business outcomes and are
// answered as RFC 7807-shaped problems; anything unrecognised is still
// answered as a problem, so the calling agent always receives a structured,
// machine-readable result and infrastructure detail (DSNs, SQL) stays in the
// log.
func problemFromUseCase(ctx context.Context, err error, id customerorder.ID) *problem {
	if errors.Is(err, ports.ErrNotFound) {
		return problemNotFound(fmt.Sprintf("no order exists with orderId %q", string(id)))
	}
	slog.ErrorContext(ctx, "a2a skill failed", "error", err)
	return problemInternal()
}

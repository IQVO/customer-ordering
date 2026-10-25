package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// errorCatalogue names each typed error a read tool can meet with the same
// stable slug the REST adapter uses as the last segment of its RFC 9457
// "type" URI (internal/adapters/inbound/http/errors.go), so a client sees one
// error vocabulary over both surfaces. The reads this surface exposes can
// only fail by not finding the order (ports.ErrNotFound): every domain and
// gateway error belongs to a write path, which this adapter does not hold.
// ports is imported for this sentinel only — an interface-and-errors package
// of the application layer, not a write seam.
var errorCatalogue = []struct {
	target error
	slug   string
}{
	{ports.ErrNotFound, "not-found"},
}

// slugFor returns the REST slug of err; ok is false for an untyped error.
func slugFor(err error) (slug string, ok bool) {
	for _, entry := range errorCatalogue {
		if errors.Is(err, entry.target) {
			return entry.slug, true
		}
	}
	return "", false
}

// toolError builds a tool-level error "<slug>: <detail>". Returned from a
// handler it becomes an isError tool result, never a transport failure.
func toolError(slug, detail string) error {
	return fmt.Errorf("%s: %s", slug, detail)
}

// mapError turns a use-case error into the tool error the model sees. Typed
// errors are prefixed with their REST slug and keep their message; anything
// else is logged and reported generically so infrastructure details (DSNs,
// SQL) never reach the model. The log uses the request context so the record
// carries the trace_id of the tool call.
func mapError(ctx context.Context, err error) error {
	if slug, ok := slugFor(err); ok {
		return toolError(slug, err.Error())
	}
	slog.ErrorContext(ctx, "mcp tool failed with an unexpected error", "error", err)
	return toolError("internal-error", "an unexpected internal error occurred")
}

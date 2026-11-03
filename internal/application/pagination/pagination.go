// Package pagination is the keyset vocabulary of the paginated reads: the
// position a page starts after. It is plain data, shared by the use cases and
// the repositories; the opaque wire cursor is the HTTP adapter's concern.
package pagination

import "time"

// Keyset is the sort key AND id of the last row of the previous page. A list
// ordered by (PlacedAt DESC, ID ASC) resumes strictly after this row, so rows
// that share a PlacedAt are neither skipped nor repeated. A nil *Keyset means
// the first page.
type Keyset struct {
	PlacedAt time.Time
	ID       string
}

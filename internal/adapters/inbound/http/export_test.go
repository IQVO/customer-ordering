package http

import "time"

// ProblemOf exposes the status and slug an error is answered with.
func ProblemOf(err error) (int, string) {
	p := problemFor(err)
	return p.status, p.slug
}

// FixedNow is the instant fixedClock reports.
var FixedNow = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

// CatalogueRow is one problem type of the catalogue as the replay-class tests read it.
type CatalogueRow struct {
	Err    error
	Slug   string
	Status int
	Class  string
}

// Catalogue exposes every entry of problemCatalogue with the replay class it declares.
func Catalogue() []CatalogueRow {
	rows := make([]CatalogueRow, 0, len(problemCatalogue))
	for _, e := range problemCatalogue {
		rows = append(rows, CatalogueRow{Err: e.err, Slug: e.p.slug, Status: e.p.status, Class: string(e.replay)})
	}
	return rows
}

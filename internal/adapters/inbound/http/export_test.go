package http

import "time"

// ProblemOf exposes the status and slug an error is answered with.
func ProblemOf(err error) (int, string) {
	p := problemFor(err)
	return p.status, p.slug
}

// FixedNow is the instant fixedClock reports.
var FixedNow = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

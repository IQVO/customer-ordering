package customerorder

// derive computes the order status from the group states (ADR 0002, A1). The
// groups that are CANCELLED do not count; a group in CANCELLATION_REQUESTED
// still counts as unlocked.
func derive(groups []GroupPromise) Status {
	var live, locked, shipped, delivered int
	for _, g := range groups {
		switch g.Status {
		case GroupCancelled:
			continue
		case GroupLocked:
			locked++
		case GroupShipped:
			shipped++
		case GroupDelivered:
			delivered++
		}
		live++
	}
	switch {
	case live == 0:
		return StatusCancelled
	case delivered == live:
		return StatusDelivered
	case delivered > 0:
		return StatusPartiallyDelivered
	case shipped == live:
		return StatusShipped
	case shipped > 0:
		return StatusPartiallyShipped
	case locked > 0:
		return StatusInFulfillment
	}
	return StatusPlaced
}

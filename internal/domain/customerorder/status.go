package customerorder

// derive computes the order status from the group states (ADR 0002, A1). The
// groups that are CANCELLED do not count; a group in CANCELLATION_REQUESTED
// still counts as unlocked.
func derive(groups []GroupPromise) Status {
	var live, locked, shipped, delivered int
	for _, g := range groups {
		if g.Status == GroupCancelled {
			continue
		}
		live++
		if g.Status == GroupLocked {
			locked++
		}
		if g.Status == GroupShipped {
			shipped++
		}
		if g.Status == GroupDelivered {
			delivered++
		}
	}
	if live == 0 {
		return StatusCancelled
	}
	if delivered == live {
		return StatusDelivered
	}
	if delivered > 0 {
		return StatusPartiallyDelivered
	}
	if shipped == live {
		return StatusShipped
	}
	if shipped > 0 {
		return StatusPartiallyShipped
	}
	if locked > 0 {
		return StatusInFulfillment
	}
	return StatusPlaced
}

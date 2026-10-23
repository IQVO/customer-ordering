package customerorder

// derive computes the order status from the group states (ADR 0002, A1 and
// A23). The groups that are CANCELLED do not count; a group in
// CANCELLATION_REQUESTED still counts as unlocked. A LOST or RETURNED group is
// closed without a delivery, and it did ship, so while other groups are open
// it counts as shipped progress; once every counted group is closed
// (DELIVERED, LOST or RETURNED) the order is closed too.
func derive(groups []GroupPromise) Status {
	var live, locked, shipped, delivered, lost, returned int
	for _, g := range groups {
		if g.Status == GroupCancelled {
			continue
		}
		live++
		switch g.Status {
		case GroupLocked:
			locked++
		case GroupShipped:
			shipped++
		case GroupDelivered:
			delivered++
		case GroupLost:
			lost++
		case GroupReturned:
			returned++
		}
	}
	if live == 0 {
		return StatusCancelled
	}
	if delivered+lost+returned == live {
		return closed(live, delivered, lost)
	}
	if delivered > 0 {
		return StatusPartiallyDelivered
	}
	if shipped+lost+returned == live {
		return StatusShipped
	}
	if shipped+lost+returned > 0 {
		return StatusPartiallyShipped
	}
	if locked > 0 {
		return StatusInFulfillment
	}
	return StatusPlaced
}

// closed is the status of an order whose counted groups are all DELIVERED,
// LOST or RETURNED: DELIVERED when all were delivered, PARTIALLY_DELIVERED
// when some were, otherwise LOST when any group is lost, else RETURNED.
func closed(live, delivered, lost int) Status {
	if delivered == live {
		return StatusDelivered
	}
	if delivered > 0 {
		return StatusPartiallyDelivered
	}
	if lost > 0 {
		return StatusLost
	}
	return StatusReturned
}

package customerorder

import (
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// GroupInput is one shipment group of PlaceOrder with the promise obtained
// (made and confirmed) at delivery-promise.
type GroupInput struct {
	GroupNo                 int
	PromiseID               string
	Tier                    Tier
	WitnessNodeID           string
	ShipBy                  time.Time
	DeliverByEarliest       time.Time
	DeliverByLatest         time.Time
	TimeZone                string // optional IANA zone of the window (ADR 0009)
	LineNos                 []int
	DestinationPostalPrefix string
	// PickupPointID is the promise's own point when it names one (ADR 0016):
	// an optional answer field, empty when the promise carries none. A pickup
	// order refuses a group whose promise names another point.
	PickupPointID string
	// ChosenDeliveryDay and ConsolidationEligible: CONSOLIDATED groups only (ADR 0013).
	ChosenDeliveryDay     string
	ConsolidationEligible *bool
}

// NewOrder is the input of Place. Exactly one destination is set: ShipTo for a
// ship-to order, PickupPointID for a pickup order (ADR 0016).
type NewOrder struct {
	ID            ID
	ShopperID     string
	ShipTo        shipto.ShipTo
	PickupPointID string
	Lines         []OrderLine
	Groups        []GroupInput
	// ConsolidationKey is "" (feature off: the event omits it) or the 64 hex
	// characters of the keyed hash of shopper and address (ADR 0012).
	ConsolidationKey string
}

// Place creates a PLACED order at version 1 and raises CustomerOrderPlaced
// (PlaceOrder). It enforces ErrLineWithoutPromise and
// ErrPromiseDestinationMismatch.
func Place(in NewOrder, now time.Time) (*CustomerOrder, []shared.Event, error) {
	if !shared.ValidID(string(in.ID)) {
		return nil, nil, shared.Invalid("orderId %q is not a valid id", in.ID)
	}
	if !shared.ValidID(in.ShopperID) {
		return nil, nil, shared.Invalid("shopperId %q is not a valid id", in.ShopperID)
	}
	if err := checkDestination(in); err != nil {
		return nil, nil, err
	}
	if !ValidConsolidationKey(in.ConsolidationKey) {
		return nil, nil, shared.Invalid("consolidationKey must be empty or 64 lower-case hex characters")
	}
	if err := validateLines(in.Lines); err != nil {
		return nil, nil, err
	}
	groups, prefix, err := buildGroups(in)
	if err != nil {
		return nil, nil, err
	}
	o := &CustomerOrder{
		id: in.ID, shopperID: in.ShopperID, shipTo: in.ShipTo, pickupPointID: in.PickupPointID, prefix: prefix,
		status: StatusPlaced, placedAt: shared.UTC(now), lines: append([]OrderLine{}, in.Lines...), groups: groups, version: 1,
		consolidationKey: in.ConsolidationKey,
	}
	ev := CustomerOrderPlaced{
		Header: shared.Header{Version: o.version, At: o.placedAt}, OrderID: o.id, ShopperID: o.shopperID,
		PickupPointID: in.PickupPointID, DestinationPostalPrefix: o.prefix, PlacedAt: o.placedAt, Groups: placedGroups(o),
		ConsolidationKey: o.consolidationKey,
	}
	return o, []shared.Event{ev}, nil
}

// checkDestination enforces the destination invariant of ADR 0016: exactly one
// of ShipTo and PickupPointID, the address valid when it is the destination,
// the point a valid id when it is.
func checkDestination(in NewOrder) error {
	pickup := in.PickupPointID != ""
	if pickup == (in.ShipTo != (shipto.ShipTo{})) {
		return shared.Invalid("exactly one of shipTo and pickupPointId is required")
	}
	if pickup {
		if !shared.ValidID(in.PickupPointID) {
			return shared.Invalid("pickupPointId %q is not a valid id", in.PickupPointID)
		}
		return nil
	}
	return in.ShipTo.Validate()
}

func placedGroups(o *CustomerOrder) []PlacedGroup {
	byNo := make(map[int]OrderLine, len(o.lines))
	for _, l := range o.lines {
		byNo[l.LineNo] = l
	}
	out := make([]PlacedGroup, len(o.groups))
	for i, g := range o.groups {
		lines := make([]OrderLine, len(g.LineNos))
		for j, no := range g.LineNos {
			lines[j] = byNo[no]
		}
		out[i] = PlacedGroup{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier, WitnessNodeID: g.WitnessNodeID, ShipBy: g.ShipBy,
			DeliverByEarliest: g.DeliverByEarliest, DeliverByLatest: g.DeliverByLatest, TimeZone: g.TimeZone, Lines: lines,
			ChosenDeliveryDay: g.ChosenDeliveryDay, ConsolidationEligible: cloneBool(g.ConsolidationEligible),
		}
	}
	return out
}

func validateLines(lines []OrderLine) error {
	if len(lines) == 0 || len(lines) > MaxLines {
		return shared.Invalid("an order holds 1 to %d lines", MaxLines)
	}
	seen := make(map[int]bool, len(lines))
	for _, l := range lines {
		if l.LineNo < 1 || l.LineNo > MaxNumber || seen[l.LineNo] {
			return shared.Invalid("lineNo %d must be unique and between 1 and %d", l.LineNo, MaxNumber)
		}
		seen[l.LineNo] = true
		if !shared.ValidID(l.CatalogItemID) {
			return shared.Invalid("line %d: catalogItemId %q is not a valid id", l.LineNo, l.CatalogItemID)
		}
		if l.Quantity < 1 || l.Quantity > MaxNumber {
			return shared.Invalid("line %d: quantity must be between 1 and %d", l.LineNo, MaxNumber)
		}
	}
	return nil
}

func buildGroups(in NewOrder) ([]GroupPromise, string, error) {
	if len(in.Groups) == 0 {
		return nil, "", wrapLine("the order has lines but no shipment group with a promise")
	}
	if len(in.Groups) > MaxGroups {
		return nil, "", shared.Invalid("an order holds at most %d shipment groups", MaxGroups)
	}
	known := make(map[int]bool, len(in.Lines))
	for _, l := range in.Lines {
		known[l.LineNo] = false
	}
	// prefix is the order's destination prefix: the ship-to's, or for a pickup
	// order the FIRST promise's (every promise of the order was quoted against
	// the same point, so they must agree; ADR 0016).
	prefix := ""
	if in.PickupPointID == "" {
		prefix = in.ShipTo.DestinationPrefix()
	}
	seen := make(map[int]bool, len(in.Groups))
	out := make([]GroupPromise, 0, len(in.Groups))
	for _, g := range in.Groups {
		if g.GroupNo < 1 || g.GroupNo > MaxNumber || seen[g.GroupNo] {
			return nil, "", shared.Invalid("groupNo %d must be unique and between 1 and %d", g.GroupNo, MaxNumber)
		}
		seen[g.GroupNo] = true
		if err := checkGroup(g, in, &prefix); err != nil {
			return nil, "", err
		}
		if err := assignLines(g, known); err != nil {
			return nil, "", err
		}
		out = append(out, newGroup(g))
	}
	for no, assigned := range known {
		if !assigned {
			return nil, "", wrapLine("line %d is in no shipment group", no)
		}
	}
	return out, prefix, nil
}

// checkGroup verifies one group's promise against the order's destination
// (ADR 0016). prefix is in-out: for a pickup order the first group's promise
// sets it and every later group must agree.
func checkGroup(g GroupInput, in NewOrder, prefix *string) error {
	if g.PromiseID == "" {
		return wrapLine("group %d has no promise", g.GroupNo)
	}
	if !shared.ValidID(g.PromiseID) || !shared.ValidID(g.WitnessNodeID) || !g.Tier.Valid() {
		return shared.Invalid("group %d: promiseId, witnessNodeId and tier must be valid", g.GroupNo)
	}
	if g.ShipBy.IsZero() || g.DeliverByEarliest.IsZero() || g.DeliverByLatest.IsZero() || g.DeliverByLatest.Before(g.DeliverByEarliest) {
		return shared.Invalid("group %d: shipBy and a delivery window with earliest <= latest are required", g.GroupNo)
	}
	if g.TimeZone != "" && !shared.ValidTimeZone(g.TimeZone) {
		return shared.Invalid("group %d: timeZone %q is not an IANA time-zone name", g.GroupNo, g.TimeZone)
	}
	if err := checkDestinationMatch(g, in, prefix); err != nil {
		return err
	}
	if err := checkChoice(g.Tier, g.ChosenDeliveryDay, g.ConsolidationEligible); err != nil {
		return shared.Invalid("group %d: %v", g.GroupNo, err)
	}
	return nil
}

// checkDestinationMatch is the ErrPromiseDestinationMismatch rule: the promise
// must serve the order's own destination. A ship-to order takes a promise
// quoted to the ship-to prefix and one that names no point; a pickup order
// takes a promise quoted to the point's prefix (the first promise fixes it)
// and, when the promise names a point at all, the very same point.
func checkDestinationMatch(g GroupInput, in NewOrder, prefix *string) error {
	if g.PickupPointID != "" && g.PickupPointID != in.PickupPointID {
		return wrapDestination(g.GroupNo, g.DestinationPostalPrefix)
	}
	if in.PickupPointID == "" {
		if !in.ShipTo.PrefixMatches(g.DestinationPostalPrefix) {
			return wrapDestination(g.GroupNo, g.DestinationPostalPrefix)
		}
		return nil
	}
	if g.DestinationPostalPrefix == "" {
		return shared.Invalid("group %d: the promise names no destination prefix", g.GroupNo)
	}
	if *prefix == "" {
		*prefix = g.DestinationPostalPrefix
		return nil
	}
	if g.DestinationPostalPrefix != *prefix {
		return wrapDestination(g.GroupNo, g.DestinationPostalPrefix)
	}
	return nil
}

func assignLines(g GroupInput, known map[int]bool) error {
	if len(g.LineNos) == 0 {
		return shared.Invalid("group %d has no lines", g.GroupNo)
	}
	for _, no := range g.LineNos {
		assigned, exists := known[no]
		switch {
		case !exists:
			return shared.Invalid("group %d names line %d, which the order does not have", g.GroupNo, no)
		case assigned:
			return wrapLine("line %d is in more than one shipment group", no)
		}
		known[no] = true
	}
	return nil
}

func newGroup(g GroupInput) GroupPromise {
	return GroupPromise{
		GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier, WitnessNodeID: g.WitnessNodeID,
		ShipBy: shared.UTC(g.ShipBy), DeliverByEarliest: shared.UTC(g.DeliverByEarliest), DeliverByLatest: shared.UTC(g.DeliverByLatest),
		TimeZone: g.TimeZone, LineNos: append([]int{}, g.LineNos...), Status: GroupPromised,
		ChosenDeliveryDay: g.ChosenDeliveryDay, ConsolidationEligible: cloneBool(g.ConsolidationEligible),
		TrackingNumbers: []string{}, DeliveredTrackingNumbers: []string{},
	}
}

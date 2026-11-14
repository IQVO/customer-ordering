package bdd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// pathValue follows a dotted path through decoded JSON; a numeric segment
// indexes an array.
func pathValue(root any, path string) (any, bool) {
	cur := root
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func registerRulePathSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the API problem detail names "([^"]*)"$`, func(word string) error {
		m, err := w.apiJSON()
		if err != nil {
			return err
		}
		if detail := fmt.Sprint(m["detail"]); !strings.Contains(detail, word) {
			return fmt.Errorf("problem detail %q does not name %q", detail, word)
		}
		return nil
	})
	sc.Step(`^the API response path "([^"]*)" is "([^"]*)"$`, func(path, want string) error {
		m, err := w.apiJSON()
		if err != nil {
			return err
		}
		v, ok := pathValue(m, path)
		if !ok {
			return fmt.Errorf("answer has no %q (%s)", path, truncate(w.apiBody))
		}
		if got := fmt.Sprint(v); got != want {
			return fmt.Errorf("%s = %q, want %q", path, got, want)
		}
		return nil
	})
	sc.Step(`^the API response has no path "([^"]*)"$`, func(path string) error {
		m, err := w.apiJSON()
		if err != nil {
			return err
		}
		if v, ok := pathValue(m, path); ok {
			return fmt.Errorf("answer has %s = %v, want it absent", path, v)
		}
		return nil
	})
	sc.Step(`^the shopper saves an address whose "([^"]*)" has (\d+) characters$`, func(field string, n int) error {
		fields := map[string]string{"name": "Ana Example", "line1": "1 Sample Street", "city": "Austin", "region": "TX", "postalCode": "78701", "countryCode": "US", "phone": "+1 555 0100"}
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("no address field %q", field)
		}
		fields[field] = strings.Repeat("a", n)
		body := fmt.Sprintf(`{"name":%q,"line1":%q,"city":%q,"region":%q,"postalCode":%q,"countryCode":%q,"phone":%q}`,
			fields["name"], fields["line1"], fields["city"], fields["region"], fields["postalCode"], fields["countryCode"], fields["phone"])
		return w.call("POST", "/shoppers/shopper-1/addresses", fmt.Sprintf("limit-%s-%d", field, n), body)
	})
}

// trackingJSON reads the tracking view of the scenario's order over REST.
func (w *world) trackingJSON() (map[string]any, error) {
	if err := w.call("GET", "/orders/"+w.orderID+"/tracking", "", ""); err != nil {
		return nil, err
	}
	if w.apiStatus != 200 {
		return nil, fmt.Errorf("tracking answered %d (%s)", w.apiStatus, truncate(w.apiBody))
	}
	return w.apiJSON()
}

func (w *world) trackingGroup(n int) (map[string]any, error) {
	m, err := w.trackingJSON()
	if err != nil {
		return nil, err
	}
	groups, _ := m["groups"].([]any)
	for _, g := range groups {
		if gm, _ := g.(map[string]any); gm != nil && fmt.Sprint(gm["groupNo"]) == strconv.Itoa(n) {
			return gm, nil
		}
	}
	return nil, fmt.Errorf("no group %d in the tracking (%s)", n, truncate(w.apiBody))
}

func (w *world) trackingPackage(tn string) (map[string]any, error) {
	m, err := w.trackingJSON()
	if err != nil {
		return nil, err
	}
	groups, _ := m["groups"].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		pkgs, _ := gm["packages"].([]any)
		for _, p := range pkgs {
			if pm, _ := p.(map[string]any); pm != nil && pm["trackingNumber"] == tn {
				return pm, nil
			}
		}
	}
	return nil, fmt.Errorf("no package %s in the tracking (%s)", tn, truncate(w.apiBody))
}

func checkField(root map[string]any, what, path, want string) error {
	v, ok := pathValue(root, path)
	if !ok {
		return fmt.Errorf("%s has no %q: %v", what, path, root)
	}
	if got := fmt.Sprint(v); got != want {
		return fmt.Errorf("%s %s = %q, want %q", what, path, got, want)
	}
	return nil
}

func checkMinute(root map[string]any, what, path string, minute int) error {
	v, ok := pathValue(root, path)
	if !ok {
		return fmt.Errorf("%s has no %q: %v", what, path, root)
	}
	got, err := time.Parse(time.RFC3339Nano, fmt.Sprint(v))
	if err != nil {
		return err
	}
	if !got.Equal(at(minute)) {
		return fmt.Errorf("%s %s = %s, want minute %d (%s)", what, path, got.Format(time.RFC3339), minute, at(minute).Format(time.RFC3339))
	}
	return nil
}

func registerRuleTrackingSteps(sc *godog.ScenarioContext, w *world) {
	registerRuleTrackingAssertions(sc, w)
	registerRuleSignalSteps(sc, w)
	registerRuleJourneySteps(sc, w)
}

func registerRuleTrackingAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the tracking package "([^"]*)" shows ([\w.]+) "([^"]*)"$`, func(tn, path, want string) error {
		p, err := w.trackingPackage(tn)
		if err != nil {
			return err
		}
		return checkField(p, "package "+tn, path, want)
	})
	sc.Step(`^the tracking package "([^"]*)" shows ([\w.]+) at minute (\d+)$`, func(tn, path string, minute int) error {
		p, err := w.trackingPackage(tn)
		if err != nil {
			return err
		}
		return checkMinute(p, "package "+tn, path, minute)
	})
	sc.Step(`^the tracking package "([^"]*)" shows no ([\w.]+)$`, func(tn, path string) error {
		p, err := w.trackingPackage(tn)
		if err != nil {
			return err
		}
		if v, ok := pathValue(p, path); ok {
			return fmt.Errorf("package %s shows %s = %v, want it absent", tn, path, v)
		}
		return nil
	})
	sc.Step(`^the tracking view of group (\d+) shows ([\w.]+) "([^"]*)"$`, func(n int, path, want string) error {
		g, err := w.trackingGroup(n)
		if err != nil {
			return err
		}
		return checkField(g, fmt.Sprintf("group %d", n), path, want)
	})
	sc.Step(`^the tracking view of group (\d+) shows no ([\w.]+)$`, func(n int, path string) error {
		g, err := w.trackingGroup(n)
		if err != nil {
			return err
		}
		if v, ok := pathValue(g, path); ok {
			return fmt.Errorf("group %d shows %s = %v, want it absent", n, path, v)
		}
		return nil
	})
}

// registerRuleSignalSteps feeds the promise and sourcing signals that only the
// tracking view shows (A6).
func registerRuleSignalSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^order-sourcing reports group (\d+) unsourceable with "([^"]*)"$`, func(n int, reason string) error {
		return w.consumer().ApplyFulfillmentUnsourceable(context.Background(), w.meta("FulfillmentUnsourceable"), usecases.FulfillmentUnsourceable{
			OrderID: w.orderID, GroupNo: n, Reason: reason,
		})
	})
	sc.Step(`^delivery-promise reports the promise of group (\d+) at risk with "([^"]*)"$`, func(n int, reason string) error {
		return w.consumer().ApplyPromiseAtRisk(context.Background(), w.meta("PromiseAtRisk"), usecases.PromiseAtRisk{
			PromiseID: fmt.Sprintf("p-%d", n), OrderID: w.orderID, Reason: reason,
		})
	})
	sc.Step(`^delivery-promise reports the promise of group (\d+) at risk with "([^"]*)" as event "([^"]*)"$`, func(n int, reason, id string) error {
		return w.consumer().ApplyPromiseAtRisk(context.Background(), usecases.Meta{EventID: id, Type: "PromiseAtRisk", At: at(90)}, usecases.PromiseAtRisk{
			PromiseID: fmt.Sprintf("p-%d", n), OrderID: w.orderID, Reason: reason,
		})
	})
	sc.Step(`^delivery-promise reports the promise of group (\d+) kept$`, func(n int) error {
		return w.consumer().ApplyPromiseKept(context.Background(), w.meta("PromiseKept"), usecases.PromiseKept{
			PromiseID: fmt.Sprintf("p-%d", n), OrderID: w.orderID,
		})
	})
	sc.Step(`^delivery-promise reports the unknown promise "([^"]*)" at risk$`, func(promise string) error {
		return w.consumer().ApplyPromiseAtRisk(context.Background(), w.meta("PromiseAtRisk"), usecases.PromiseAtRisk{
			PromiseID: promise, OrderID: w.orderID, Reason: "CARRIER_DELAY",
		})
	})
}

// registerRuleJourneySteps feeds the package journey of shipping-and-tracking:
// the facts the view shows and never lets move the order.
func registerRuleJourneySteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^shipping-and-tracking issues label "([^"]*)" for assignment "([^"]*)" carried by "([^"]*)"$`, func(tn, assignment, carrier string) error {
		return w.consumer().ApplyLabelIssued(context.Background(), w.meta("LabelIssued"), usecases.LabelIssued{TrackingNumber: tn, AssignmentID: assignment, Carrier: carrier})
	})
	sc.Step(`^shipping-and-tracking issues label "([^"]*)" for assignment "([^"]*)" to pickup point "([^"]*)"$`, func(tn, assignment, point string) error {
		return w.consumer().ApplyLabelIssued(context.Background(), w.meta("LabelIssued"), usecases.LabelIssued{TrackingNumber: tn, AssignmentID: assignment, Carrier: "ACME-EXPRESS", PickupPointID: point})
	})
	sc.Step(`^shipping-and-tracking voids the label of package "([^"]*)"$`, func(tn string) error {
		return w.consumer().ApplyLabelVoided(context.Background(), w.meta("LabelVoided"), usecases.LabelVoided{TrackingNumber: tn})
	})
	sc.Step(`^shipping-and-tracking scans package "([^"]*)" at node "([^"]*)" in state "([^"]*)" at minute (\d+)$`, func(tn, node, state string, minute int) error {
		return w.consumer().ApplyPackageScanned(context.Background(), w.meta("PackageScanned"), usecases.PackageScanned{TrackingNumber: tn, NodeID: node, State: state, At: at(minute)})
	})
	sc.Step(`^shipping-and-tracking raises the exception "([^"]*)" on package "([^"]*)"$`, func(reason, tn string) error {
		return w.consumer().ApplyPackageException(context.Background(), w.meta("PackageExceptionRaised"), usecases.PackageExceptionRaised{TrackingNumber: tn, OrderID: w.orderID, Reason: reason, At: at(150)})
	})
	sc.Step(`^shipping-and-tracking assigns package "([^"]*)" the window from minute (\d+) to minute (\d+) at minute (\d+)$`, func(tn string, from, to, assigned int) error {
		return w.consumer().ApplyPackageEta(context.Background(), w.meta("PackageEtaWindowAssigned"), usecases.PackageEtaWindowAssigned{
			TrackingNumber: tn, OrderID: w.orderID, EtaStart: at(from), EtaEnd: at(to), AssignedAt: at(assigned),
		})
	})
	sc.Step(`^shipping-and-tracking flags the delivery point of package "([^"]*)" as "([^"]*)" at minute (\d+)$`, func(tn, reason string, minute int) error {
		return w.consumer().ApplyDeliveryPointFlag(context.Background(), w.meta("PackageDeliveryPointFlagged"), usecases.PackageDeliveryPointFlagged{
			TrackingNumber: tn, OrderID: w.orderID, Reason: reason, FlaggedAt: at(minute),
		})
	})
	sc.Step(`^shipping-and-tracking reports package "([^"]*)" ready for collection at point "([^"]*)" at minute (\d+) held until minute (\d+)$`, func(tn, point string, ready, hold int) error {
		return w.consumer().ApplyPackageReadyForCollection(context.Background(), w.meta("PackageReadyForCollection"), usecases.PackageReadyForCollection{
			TrackingNumber: tn, OrderID: w.orderID, PickupPointID: point, ReadyAt: at(ready), HoldUntil: at(hold),
		})
	})
	sc.Step(`^shipping-and-tracking reports package "([^"]*)" collected with proof "([^"]*)" at minute (\d+)$`, func(tn, proof string, minute int) error {
		return w.consumer().ApplyPackageCollected(context.Background(), w.meta("PackageCollected"), usecases.PackageCollected{
			TrackingNumber: tn, OrderID: w.orderID, CollectedAt: at(minute), ProofCode: proof,
		})
	})
}

package repocontract

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// policyRows is a publishable document: DELIVERY_POINT_ADVISORY wakes up, every
// other family stays NOBODY.
func policyRows() []notice.Row {
	rows := notice.DefaultRows()
	for i, r := range rows {
		if r.Family == notice.FamilyDeliveryPointAdvisory {
			rows[i].Notifier = notice.NotifierMarketplace
		}
	}
	return rows
}

// nextPolicy builds version current+1 with the document above.
func nextPolicy(t *testing.T, current int) *notice.Policy {
	t.Helper()
	p, err := notice.Publish(current, policyRows(), at(60*(current+1)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// currentVersion reads the highest stored version (0 when none).
func currentVersion(t *testing.T, fx Env) int {
	t.Helper()
	p, err := fx.Policies.Current(context.Background())
	if errors.Is(err, ports.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return p.Version()
}

func savePolicy(t *testing.T, fx Env, p *notice.Policy) {
	t.Helper()
	if err := fx.Policies.Save(context.Background(), p); err != nil {
		t.Fatalf("Save policy v%d: %v", p.Version(), err)
	}
}

// The subtests share ONE Env and run in order: each continues from the version
// the previous one left stored.

func policyMiss(t *testing.T, fx Env) {
	if _, err := fx.Policies.Current(context.Background()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Current of an empty store = %v", err)
	}
}

func policyRoundTrip(t *testing.T, fx Env) {
	p := nextPolicy(t, 0)
	savePolicy(t, fx, p)
	got, err := fx.Policies.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Snapshot(), p.Snapshot()) {
		t.Fatalf("round trip differs:\n got %+v\nwant %+v", got.Snapshot(), p.Snapshot())
	}
	if n := got.Notifier(notice.FamilyDeliveryPointAdvisory); n != notice.NotifierMarketplace {
		t.Fatalf("advisory notifier = %s", n)
	}
	if n := got.Notifier(notice.FamilyGroupLost); n != notice.NotifierNobody {
		t.Fatalf("default notifier = %s", n)
	}
}

func policyVersions(t *testing.T, fx Env) {
	base := currentVersion(t, fx)
	savePolicy(t, fx, nextPolicy(t, base))
	savePolicy(t, fx, nextPolicy(t, base+1))
	if got := currentVersion(t, fx); got != base+2 {
		t.Fatalf("Current = v%d, want v%d", got, base+2)
	}
	// The earlier versions are kept: saving them again conflicts.
	for _, v := range []int{base, base + 1} {
		if err := fx.Policies.Save(context.Background(), nextPolicy(t, v)); !errors.Is(err, ports.ErrConcurrentUpdate) {
			t.Fatalf("saving version %d again = %v", v+1, err)
		}
	}
}

func policyRollback(t *testing.T, fx Env) {
	want := currentVersion(t, fx)
	err := fx.UoW.Do(context.Background(), func(ctx context.Context) error {
		if err := fx.Policies.Save(ctx, nextPolicy(t, want)); err != nil {
			return err
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("the unit must fail")
	}
	if got := currentVersion(t, fx); got != want {
		t.Fatalf("Current after rollback = v%d, want v%d", got, want)
	}
}

// testPolicies is the NotificationPolicyRepository contract (ADR 0017).
func testPolicies(t *testing.T, fx Env) {
	t.Run("a miss is ErrNotFound", func(t *testing.T) { policyMiss(t, fx) })
	t.Run("save and current round trip every row", func(t *testing.T) { policyRoundTrip(t, fx) })
	t.Run("current is the highest version and every version is kept", func(t *testing.T) { policyVersions(t, fx) })
	t.Run("a rolled back unit stores no version", func(t *testing.T) { policyRollback(t, fx) })
}

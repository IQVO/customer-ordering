package http_test

import (
	"fmt"
	"testing"
)

// row builds one policy row of the PUT body.
func row(family, notifier string) string {
	return fmt.Sprintf(`{"family":%q,"notifier":%q}`, family, notifier)
}

// policyBody is a whole document: every family, one notifier for the advisory.
func policyBody(advisory string, extra ...string) string {
	rows := ""
	for _, f := range []string{"DELAY_EXPECTED", "ETA_WINDOW_ASSIGNED", "DELIVERY_POINT_ADVISORY", "GROUP_SHIPPED", "GROUP_DELIVERED", "GROUP_LOST", "GROUP_RETURNED"} {
		n := "NOBODY"
		if f == "DELIVERY_POINT_ADVISORY" {
			n = advisory
		}
		rows += row(f, n) + ","
	}
	for _, e := range extra {
		rows += e + ","
	}
	return `{"rows":[` + trimSuffix(rows, ",") + `]}`
}

func trimSuffix(s, suffix string) string {
	if len(s) > len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}

func TestNotificationPolicyDefaultsToNobodyAndVersionZero(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/notification-policy", "", "")
	if r.Status != 200 {
		t.Fatalf("get = %d %s", r.Status, r.Body)
	}
	o := r.json(t)
	if o["version"] != float64(0) || o["publishedAt"] != nil {
		t.Fatalf("default = %v", o)
	}
	rows := o["rows"].([]any)
	if len(rows) != 7 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, anyRow := range rows {
		m := anyRow.(map[string]any)
		if m["notifier"] != "NOBODY" {
			t.Fatalf("row = %v", m)
		}
	}
}

func TestPutNotificationPolicyPublishesTheWholeDocument(t *testing.T) {
	e := newEnv(t)
	r := e.do("PUT", "/notification-policy", "pol-1", policyBody("MARKETPLACE"))
	if r.Status != 200 {
		t.Fatalf("put = %d %s", r.Status, r.Body)
	}
	o := r.json(t)
	if o["version"] != float64(1) || o["publishedAt"] == nil {
		t.Fatalf("published = %v", o)
	}
	got := e.do("GET", "/notification-policy", "", "").json(t)
	if got["version"] != float64(1) {
		t.Fatalf("get after put = %v", got)
	}
	for _, anyRow := range got["rows"].([]any) {
		m := anyRow.(map[string]any)
		want := "NOBODY"
		if m["family"] == "DELIVERY_POINT_ADVISORY" {
			want = "MARKETPLACE"
		}
		if m["notifier"] != want {
			t.Fatalf("row = %v, want %s", m, want)
		}
	}
	// A replay of the same key and body answers the first response, not a new version.
	again := e.do("PUT", "/notification-policy", "pol-1", policyBody("MARKETPLACE"))
	if again.Status != 200 || again.Header.Get("Idempotency-Replayed") != "true" || again.Body != r.Body {
		t.Fatalf("replay = %d %s", again.Status, again.Body)
	}
	if got := e.do("GET", "/notification-policy", "", "").json(t)["version"]; got != float64(1) {
		t.Fatalf("a replay must not publish twice: version = %v", got)
	}
	// The next PUT derives version 2.
	next := e.do("PUT", "/notification-policy", "pol-2", policyBody("SELLER"))
	if next.json(t)["version"] != float64(2) {
		t.Fatalf("next = %s", next.Body)
	}
}

func TestPutNotificationPolicyRefusesBrokenDocuments(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name string
		body string
	}{
		{"a family missing", `{"rows":[{"family":"GROUP_LOST","notifier":"NOBODY"}]}`},
		{"a family twice", policyBody("NOBODY", row("GROUP_LOST", "SELLER"))},
		{"an unknown family", policyBody("NOBODY", row("GROUP_STOLEN", "NOBODY"))},
		{"an unknown notifier", `{"rows":[{"family":"DELAY_EXPECTED","notifier":"CARRIER"},{"family":"ETA_WINDOW_ASSIGNED","notifier":"NOBODY"},{"family":"DELIVERY_POINT_ADVISORY","notifier":"NOBODY"},{"family":"GROUP_SHIPPED","notifier":"NOBODY"},{"family":"GROUP_DELIVERED","notifier":"NOBODY"},{"family":"GROUP_LOST","notifier":"NOBODY"},{"family":"GROUP_RETURNED","notifier":"NOBODY"}]}`},
		{"an unknown field", `{"rows":[],"nope":1}`},
	}
	for _, c := range cases {
		r := e.do("PUT", "/notification-policy", "k-"+c.name, c.body)
		if r.Status != 422 && r.Status != 400 {
			t.Fatalf("%s = %d %s", c.name, r.Status, r.Body)
		}
	}
	if got := e.do("GET", "/notification-policy", "", "").json(t)["version"]; got != float64(0) {
		t.Fatalf("a refused document must publish nothing: version = %v", got)
	}
}

func TestPutNotificationPolicyRequiresTheKey(t *testing.T) {
	e := newEnv(t)
	r := e.do("PUT", "/notification-policy", "", policyBody("MARKETPLACE"))
	if r.Status != 400 || r.json(t)["type"] != "/problems/idempotency-key-required" {
		t.Fatalf("put without a key = %d %s", r.Status, r.Body)
	}
}

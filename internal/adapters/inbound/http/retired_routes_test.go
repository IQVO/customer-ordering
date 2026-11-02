package http_test

import "testing"

// TestRetiredRoutesAreGone pins the REST guideline cutover: the old custom-method
// route answers 404 or 405, never the command (no alias, no coexistence).
func TestRetiredRoutesAreGone(t *testing.T) {
	e := newEnv(t)
	id := e.checkout("retired-routes").json(t)["orderId"].(string)
	for _, c := range []struct{ method, path string }{
		{"POST", "/orders/" + id + ":cancel"},
		{"POST", "/orders/" + id + "/cancel"},
		{"POST", "/orders/" + id + "/cancellation"},
	} {
		r := e.do(c.method, c.path, "retired-"+c.path, `{"reason":"too late"}`)
		if r.Status != 404 && r.Status != 405 {
			t.Fatalf("%s %s = %d %s, want 404 or 405", c.method, c.path, r.Status, r.Body)
		}
	}
}

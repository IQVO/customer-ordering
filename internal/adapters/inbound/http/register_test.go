package http_test

import "testing"

// A registration body that is not strictly valid JSON (malformed, an unknown
// field, trailing data) is answered by the /shoppers handler itself with 400
// malformed-request — before the Idempotency-Key record or the use case runs.
func TestRegisterShopperMalformedBodiesAreProblems(t *testing.T) {
	e := newEnv(t)
	for i, body := range []string{`{`, `{"email":"a@b.co","nickname":"a"}`, `{"email":"a@b.co"} {}`} {
		r := e.do("POST", "/shoppers", "bad-body-"+string(rune('a'+i)), body)
		p := r.json(t)
		if r.Status != 400 || p["type"] != "/problems/malformed-request" || r.Header.Get("Content-Type") != "application/problem+json" {
			t.Fatalf("body %q = %d %s", body, r.Status, r.Body)
		}
	}
}

package a2a

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const testAgentURL = "http://customer-ordering-a2a.retail-network.svc.cluster.local:8093/"

func testCard() *a2a.AgentCard {
	return Card(Config{AgentURL: testAgentURL, Version: "1.0.0"})
}

// TestCardShape pins the identity, interface and capability surface of the
// Agent Card: the discovery contract other contexts program against.
func TestCardShape(t *testing.T) {
	card := testCard()

	if card.Name != "customer-ordering" {
		t.Errorf("card name = %q, want customer-ordering", card.Name)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("card has %d interfaces, want 1", len(card.SupportedInterfaces))
	}
	iface := card.SupportedInterfaces[0]
	if iface.URL != testAgentURL || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("interface = %+v, want JSONRPC at the configured agent URL", iface)
	}
	if card.Capabilities.Streaming {
		t.Error("streaming must not be advertised: this is a message-only agent")
	}
	if card.SecuritySchemes != nil {
		t.Error("card must declare no security schemes: the surface is unauthenticated fleet-wide")
	}
	if card.Provider == nil || card.Provider.Org != "IQVO" {
		t.Errorf("provider = %+v, want IQVO", card.Provider)
	}
	if card.Version != "1.0.0" {
		t.Errorf("card version = %q, want the configured one", card.Version)
	}
}

// TestCardSkillsAreCuratedAndTruthful pins the two-skill, READ-ONLY surface
// and proves the card never lies about its own contract: every advertised
// example must parse under the SAME strict decoder the executor uses, name its
// own skill, and carry arguments that skill accepts.
func TestCardSkillsAreCuratedAndTruthful(t *testing.T) {
	want := map[string]bool{skillGetOrder: true, skillGetOrderTracking: true}
	skills := testCard().Skills
	if len(skills) != len(want) {
		t.Fatalf("card has %d skills, want exactly %d (curated, goal-shaped, not one per endpoint)", len(skills), len(want))
	}
	for _, s := range skills {
		if !want[s.ID] {
			t.Errorf("unexpected skill %q on the card", s.ID)
			continue
		}
		if s.Name == "" || s.Description == "" || len(s.Examples) == 0 {
			t.Errorf("skill %q lacks a name, description or example", s.ID)
		}
		if !containsTag(s.Tags, "read") || containsTag(s.Tags, "write") {
			t.Errorf("skill %q must be tagged read and never write (this context's MCP surface is read-only), tags = %v", s.ID, s.Tags)
		}
		for _, ex := range s.Examples {
			assertExampleTruthful(t, s.ID, ex)
		}
	}
}

func assertExampleTruthful(t *testing.T, skill, example string) {
	t.Helper()
	var req skillRequest
	if err := decodeStrict([]byte(example), &req); err != nil {
		t.Errorf("skill %q example is not valid under the strict decoder: %v", skill, err)
		return
	}
	if req.Skill != skill {
		t.Errorf("skill %q example names skill %q", skill, req.Skill)
	}
	if id, problem := orderID(req.Arguments); problem != nil || id == "" {
		t.Errorf("skill %q example arguments are rejected by its own validation: %+v", skill, problem)
	}
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// TestCardNeverClaimsToWriteOrToServeTheShipTo keeps the card honest about the
// read-only charter and the PII decision (ADR 0008): no skill id suggests a
// write or the restricted ship-to read, and the description says so.
func TestCardNeverClaimsToWriteOrToServeTheShipTo(t *testing.T) {
	card := testCard()
	for _, s := range card.Skills {
		for _, verb := range []string{"place_", "checkout", "cancel_", "register_", "create_", "update_", "delete_", "get_ship_to"} {
			if strings.Contains(s.ID, verb) {
				t.Errorf("skill %q looks like a write or the ship-to read; this agent offers neither", s.ID)
			}
		}
	}
	for _, phrase := range []string{"read-only", "does not expose the ship-to address"} {
		if !strings.Contains(card.Description, phrase) {
			t.Errorf("the card description must say %q: %q", phrase, card.Description)
		}
	}
}

// TestCardSerializable proves the card marshals to JSON a client can parse
// back, with the discovery fields intact.
func TestCardSerializable(t *testing.T) {
	raw, err := json.Marshal(testCard())
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var back a2a.AgentCard
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("card JSON does not round-trip: %v", err)
	}
	if back.Name != "customer-ordering" || len(back.Skills) != 2 {
		t.Errorf("round-tripped card = %q with %d skills", back.Name, len(back.Skills))
	}
}

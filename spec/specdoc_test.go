package spec

import (
	"testing"

	"github.com/operatorstack/interlock/ir"
)

// sampleSpec exercises every field spec.v1 must round-trip: actors, all resource
// kinds, allow/deny rules, multi-op rules, and every requirement variant.
func sampleSpec() Spec {
	return Spec{
		PolicyID: "sample.v1",
		Actors:   []Actor{{ID: "agent"}, {ID: "sdk-generator"}},
		Resources: []Resource{
			{ID: "source", Kind: ir.KindTree, URI: "repo://src/**"},
			{ID: "generated", Kind: ir.KindTree, URI: "repo://generated/**"},
			{ID: "main", Kind: ir.KindBranch, URI: "repo://branch/main"},
		},
		Rules: []Rule{
			{
				ID: "agent-source", Effect: ir.EffectAllow, Actor: "agent",
				Operations: []ir.Operation{ir.OpRead, ir.OpWrite}, Resource: "source",
				Reason: "the agent may work freely in ordinary source code",
			},
			{
				ID: "deny-agent-generated", Effect: ir.EffectDeny, Actor: "agent",
				Operations: []ir.Operation{ir.OpWrite}, Resource: "generated",
				Reason: "generated files are owned by the build",
			},
			{
				ID: "publish-generated", Effect: ir.EffectAllow, Actor: "sdk-generator",
				Operations: []ir.Operation{ir.OpPublish}, Resource: "generated",
				Requires: []ir.Requirement{
					{Kind: ir.ReqPolicyHashMatch},
					{Kind: ir.ReqStagedHashMatch},
					{Kind: ir.ReqReceiptStatus, Receipt: "sdk-tests", Status: "passed"},
				},
			},
			{
				ID: "push-main", Effect: ir.EffectAllow, Actor: "agent",
				Operations: []ir.Operation{ir.OpPush}, Resource: "main",
				Requires: []ir.Requirement{{Kind: ir.ReqHumanApproval, Approval: "release-main"}},
			},
		},
	}
}

// TestRoundTrip is the load-bearing keystone check: Spec -> spec.v1 bytes ->
// Spec must reconstruct an identical in-memory spec, so the serializable format
// loses nothing the compiler depends on.
func TestRoundTrip(t *testing.T) {
	orig := sampleSpec()

	data, err := Encode(FromSpec(orig))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	got, err := DecodeToSpec(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !specsEqual(orig, got) {
		t.Fatalf("round-trip mismatch:\n orig=%+v\n  got=%+v", orig, got)
	}
}

// TestEncodeStampsProtocol ensures spec.v1 output always carries its schema tag,
// even if the caller built a SpecDoc without setting it.
func TestEncodeStampsProtocol(t *testing.T) {
	data, err := Encode(SpecDoc{PolicyID: "x", Actors: []string{"a"}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if doc, err := Decode(data); err != nil {
		t.Fatalf("decode of encoded doc: %v", err)
	} else if doc.Protocol != Protocol {
		t.Fatalf("protocol not stamped: %q", doc.Protocol)
	}
}

func TestDecodeRejectsWrongProtocol(t *testing.T) {
	// A canonical IR document (interlock.policy.v1) must not decode as spec.v1.
	if _, err := Decode([]byte(`{"protocol":"interlock.policy.v1","policy_id":"x"}`)); err == nil {
		t.Fatal("expected error decoding policy.v1 as spec.v1")
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	// A typo like "requirements" must fail loudly, not silently vanish.
	in := `{"protocol":"interlock.spec.v1","policy_id":"x","requirements":[]}`
	if _, err := Decode([]byte(in)); err == nil {
		t.Fatal("expected error on unknown field")
	}
}

func specsEqual(a, b Spec) bool {
	if a.PolicyID != b.PolicyID || len(a.Actors) != len(b.Actors) ||
		len(a.Resources) != len(b.Resources) || len(a.Rules) != len(b.Rules) {
		return false
	}
	for i := range a.Actors {
		if a.Actors[i] != b.Actors[i] {
			return false
		}
	}
	for i := range a.Resources {
		if a.Resources[i] != b.Resources[i] {
			return false
		}
	}
	for i := range a.Rules {
		ra, rb := a.Rules[i], b.Rules[i]
		if ra.ID != rb.ID || ra.Effect != rb.Effect || ra.Actor != rb.Actor ||
			ra.Resource != rb.Resource || ra.Reason != rb.Reason ||
			len(ra.Operations) != len(rb.Operations) || len(ra.Requires) != len(rb.Requires) {
			return false
		}
		for j := range ra.Operations {
			if ra.Operations[j] != rb.Operations[j] {
				return false
			}
		}
		for j := range ra.Requires {
			if ra.Requires[j] != rb.Requires[j] {
				return false
			}
		}
	}
	return true
}

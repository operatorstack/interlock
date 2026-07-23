package interlock_test

import (
	"testing"

	il "github.com/operatorstack/interlock"
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

// build is the canonical exclusive-publish policy authored via the DSL.
func build() *il.Builder {
	return il.Policy("exclusive-publish.v1").
		Actor("agent").
		Actor("publisher").
		File("artifact", "repo://out/result.json").
		Tree("workspace", "repo://work/**").
		Allow("agent-workspace").By("agent").To(il.Write, il.Delete).On("workspace").
		Because("the agent may work freely in its own workspace").Add().
		Deny("deny-agent-artifact").By("agent").To(il.Write, il.Publish).On("artifact").
		Because("the producing agent may not touch the protected artifact").Add().
		Allow("allow-publisher").By("publisher").To(il.Publish).On("artifact").
		Requiring(il.PolicyHashMatch(), il.ReceiptStatus("deltawire.supervision.receipt.v1", "released")).
		Because("the verified publisher may publish a staged candidate").Add()
}

func TestDSLCompiles(t *testing.T) {
	p, err := build().Compile()
	if err != nil {
		t.Fatalf("DSL policy failed to compile: %v", err)
	}
	if p.Protocol != ir.Protocol || p.PolicyID != "exclusive-publish.v1" {
		t.Fatalf("unexpected IR header: %+v", p)
	}
}

func TestDSLEquivalentBuildsHashEqual(t *testing.T) {
	a, err := build().Emit()
	if err != nil {
		t.Fatal(err)
	}
	b, err := build().Emit()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("two identical DSL builds produced different IR")
	}
}

// TestDSLEndToEnd wires the authored policy through the engine to confirm the
// authoring surface and the execution surface agree.
func TestDSLEndToEnd(t *testing.T) {
	p, err := build().Compile()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := p.Hash()

	// Agent publishing the artifact is denied.
	d := engine.Decide(p, protocol.EffectRequest{
		Protocol: protocol.EffectRequestProtocol, RequestID: "1", RunID: "run1",
		Actor: "agent", Operation: il.Publish,
		Resource: protocol.TargetResource{Kind: il.FileKind, URI: "repo://out/result.json"},
	})
	if d.Outcome != protocol.OutcomeDeny {
		t.Fatalf("agent publish: want deny, got %s", d.Outcome)
	}

	// Publisher with full evidence is allowed.
	d = engine.Decide(p, protocol.EffectRequest{
		Protocol: protocol.EffectRequestProtocol, RequestID: "2", RunID: "run1",
		Actor: "publisher", Operation: il.Publish,
		Resource:          protocol.TargetResource{Kind: il.FileKind, URI: "repo://out/result.json"},
		ClaimedPolicyHash: h,
		Evidence: []protocol.Evidence{
			{Kind: ir.ReqReceiptStatus, Receipt: "deltawire.supervision.receipt.v1", Status: "released"},
		},
	})
	if d.Outcome != protocol.OutcomeAllow {
		t.Fatalf("publisher publish: want allow, got %s (%s)", d.Outcome, d.Reason)
	}
}

// TestArbitraryConstructionSameIR proves arbitrary Go may construct a policy
// (here a loop adding resources) yet still lower to a canonical, hashable IR.
func TestArbitraryConstructionSameIR(t *testing.T) {
	mk := func() ([]byte, error) {
		b := il.Policy("loop.v1").Actor("agent")
		for _, name := range []string{"a", "b", "c"} {
			b = b.File("f-"+name, "repo://out/"+name+".json")
		}
		b = b.Deny("deny-all").By("agent").To(il.Write).On("f-a").Add()
		return b.Emit()
	}
	a, err := mk()
	if err != nil {
		t.Fatal(err)
	}
	b, err := mk()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("loop-constructed policy is not deterministic")
	}
}

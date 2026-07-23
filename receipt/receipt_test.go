package receipt

import (
	"testing"

	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

func policy() ir.Policy {
	return ir.Policy{
		Protocol: ir.Protocol, PolicyID: "p.v1",
		Actors:    []string{"agent"},
		Resources: []ir.Resource{{ID: "artifact", Kind: ir.KindFile, URI: "repo://out/x.json"}},
		Rules: []ir.Rule{
			{ID: "deny-agent", Effect: ir.EffectDeny, Actor: "agent", Operations: []ir.Operation{ir.OpWrite}, Resource: "artifact"},
		},
	}
}

func requests() []protocol.EffectRequest {
	mk := func(id string) protocol.EffectRequest {
		return protocol.EffectRequest{
			Protocol: protocol.EffectRequestProtocol, RequestID: id, RunID: "run1",
			Actor: "agent", Operation: ir.OpWrite,
			Resource: protocol.TargetResource{Kind: ir.KindFile, URI: "repo://out/x.json"},
		}
	}
	return []protocol.EffectRequest{mk("r0"), mk("r1"), mk("r2")}
}

func buildChain(t *testing.T, p ir.Policy, reqs []protocol.EffectRequest) []Receipt {
	t.Helper()
	c := NewChain("run1")
	for _, r := range reqs {
		if _, err := c.Append(p, r, engine.Decide(p, r)); err != nil {
			t.Fatal(err)
		}
	}
	return c.Receipts
}

func TestReplayHappyPath(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	if err := Replay(p, reqs, rc); err != nil {
		t.Fatalf("clean chain rejected: %v", err)
	}
}

func TestReplayFailsOnChangedPolicy(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	p2 := policy()
	p2.PolicyID = "tampered"
	if err := Replay(p2, reqs, rc); err == nil {
		t.Fatal("expected failure on changed policy")
	}
}

func TestReplayFailsOnReorder(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	rc[1], rc[2] = rc[2], rc[1]
	reqs[1], reqs[2] = reqs[2], reqs[1]
	if err := Replay(p, reqs, rc); err == nil {
		t.Fatal("expected failure on reorder")
	}
}

func TestReplayFailsOnMissingLink(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	if err := Replay(p, reqs[:2], rc[:2]); err != nil {
		t.Fatalf("prefix should still verify: %v", err)
	}
	// Drop the middle receipt but keep all requests → length + link break.
	if err := Replay(p, reqs, append(rc[:1:1], rc[2:]...)); err == nil {
		t.Fatal("expected failure on missing link")
	}
}

func TestReplayFailsOnTamperedEvidence(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	rc[1].EvidenceHashes = []string{"sha256:injected"}
	if err := Replay(p, reqs, rc); err == nil {
		t.Fatal("expected failure on tampered evidence hashes")
	}
}

func TestReplayFailsOnCrossRun(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	rc[2].RunID = "run2"
	if err := Replay(p, reqs, rc); err == nil {
		t.Fatal("expected failure on cross-run receipt")
	}
}

func TestReplayFailsOnDuplicateSequence(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	rc[2].Sequence = 1
	if err := Replay(p, reqs, rc); err == nil {
		t.Fatal("expected failure on duplicate sequence")
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	p, reqs := policy(), requests()
	rc := buildChain(t, p, reqs)
	if err := VerifyChain(rc); err != nil {
		t.Fatalf("clean chain rejected: %v", err)
	}
	rc[1].Outcome = protocol.OutcomeAllow // committed field, no self-hash update
	if err := VerifyChain(rc); err == nil {
		t.Fatal("expected self-hash mismatch")
	}
}

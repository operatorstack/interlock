package engine

import (
	"reflect"
	"testing"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

func policy() ir.Policy {
	p := ir.Policy{
		Protocol: ir.Protocol,
		PolicyID: "p.v1",
		Actors:   []string{"agent", "publisher"},
		Resources: []ir.Resource{
			{ID: "artifact", Kind: ir.KindFile, URI: "repo://out/x.json"},
			{ID: "workspace", Kind: ir.KindTree, URI: "repo://work/**"},
		},
		Rules: []ir.Rule{
			{ID: "agent-workspace", Effect: ir.EffectAllow, Actor: "agent", Operations: []ir.Operation{ir.OpWrite}, Resource: "workspace"},
			{ID: "deny-agent-artifact", Effect: ir.EffectDeny, Actor: "agent", Operations: []ir.Operation{ir.OpWrite, ir.OpPublish}, Resource: "artifact"},
			{ID: "allow-publish", Effect: ir.EffectAllow, Actor: "publisher", Operations: []ir.Operation{ir.OpPublish}, Resource: "artifact",
				Requires: []ir.Requirement{{Kind: ir.ReqPolicyHashMatch}, {Kind: ir.ReqReceiptStatus, Receipt: "deltawire.supervision.receipt.v1", Status: "released"}}},
		},
	}
	return p
}

func req(actor string, op ir.Operation, kind ir.ResourceKind, uri string) protocol.EffectRequest {
	return protocol.EffectRequest{
		Protocol: protocol.EffectRequestProtocol, RequestID: "r1", RunID: "run1",
		Actor: actor, Operation: op, Resource: protocol.TargetResource{Kind: kind, URI: uri},
	}
}

func TestFault(t *testing.T) {
	d := Decide(policy(), req("agent", "filesystem.teleport", ir.KindFile, "repo://out/x.json"))
	if d.Outcome != protocol.OutcomeFault {
		t.Fatalf("want fault, got %s", d.Outcome)
	}
	d = Decide(policy(), req("agent", ir.OpWrite, "socket", "repo://out/x.json"))
	if d.Outcome != protocol.OutcomeFault {
		t.Fatalf("want fault for bad kind, got %s", d.Outcome)
	}
}

func TestDefaultDeny(t *testing.T) {
	d := Decide(policy(), req("stranger", ir.OpWrite, ir.KindFile, "repo://out/x.json"))
	if d.Outcome != protocol.OutcomeDeny || d.RuleID != "" {
		t.Fatalf("want default deny with empty rule, got %s/%q", d.Outcome, d.RuleID)
	}
}

func TestDenyRule(t *testing.T) {
	d := Decide(policy(), req("agent", ir.OpWrite, ir.KindFile, "repo://out/x.json"))
	if d.Outcome != protocol.OutcomeDeny || d.RuleID != "deny-agent-artifact" {
		t.Fatalf("want deny by rule, got %s/%q", d.Outcome, d.RuleID)
	}
}

func TestTreeAllow(t *testing.T) {
	d := Decide(policy(), req("agent", ir.OpWrite, ir.KindTree, "repo://work/tmp/scratch.txt"))
	if d.Outcome != protocol.OutcomeAllow || d.RuleID != "agent-workspace" {
		t.Fatalf("want allow in workspace, got %s/%q", d.Outcome, d.RuleID)
	}
}

func TestRequireWhenEvidenceMissing(t *testing.T) {
	r := req("publisher", ir.OpPublish, ir.KindFile, "repo://out/x.json")
	d := Decide(policy(), r)
	if d.Outcome != protocol.OutcomeRequire {
		t.Fatalf("want require, got %s", d.Outcome)
	}
	if len(d.MissingEvidence) != 2 {
		t.Fatalf("want 2 missing predicates, got %d", len(d.MissingEvidence))
	}
}

func TestAllowWhenEvidencePresent(t *testing.T) {
	p := policy()
	h, _ := p.Hash()
	r := req("publisher", ir.OpPublish, ir.KindFile, "repo://out/x.json")
	r.ClaimedPolicyHash = h
	r.Evidence = []protocol.Evidence{
		{Kind: ir.ReqReceiptStatus, Receipt: "deltawire.supervision.receipt.v1", Status: "released"},
	}
	d := Decide(p, r)
	if d.Outcome != protocol.OutcomeAllow {
		t.Fatalf("want allow, got %s (%s) missing=%+v", d.Outcome, d.Reason, d.MissingEvidence)
	}
}

func TestWrongPolicyHashStillRequires(t *testing.T) {
	p := policy()
	r := req("publisher", ir.OpPublish, ir.KindFile, "repo://out/x.json")
	r.ClaimedPolicyHash = "sha256:wrong"
	r.Evidence = []protocol.Evidence{
		{Kind: ir.ReqReceiptStatus, Receipt: "deltawire.supervision.receipt.v1", Status: "released"},
	}
	d := Decide(p, r)
	if d.Outcome != protocol.OutcomeRequire {
		t.Fatalf("want require on wrong policy hash, got %s", d.Outcome)
	}
}

func TestDeterministic(t *testing.T) {
	p := policy()
	r := req("agent", ir.OpWrite, ir.KindFile, "repo://out/x.json")
	first := Decide(p, r)
	for i := 0; i < 50; i++ {
		if !reflect.DeepEqual(Decide(p, r), first) {
			t.Fatal("engine is not deterministic")
		}
	}
}

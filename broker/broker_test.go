package broker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/receipt"
	"github.com/operatorstack/interlock/workspace"
)

// exclusivePublishPolicy is the DeltaWire-shaped policy: the agent may not write
// the protected artifact; a verified publisher may publish a staged candidate
// when the live policy hash matches and a released supervision receipt exists.
func exclusivePublishPolicy() ir.Policy {
	return ir.Policy{
		Protocol: ir.Protocol, PolicyID: "exclusive-publish.v1",
		Actors: []string{"agent", "publisher"},
		Resources: []ir.Resource{
			{ID: "artifact", Kind: ir.KindFile, URI: "repo://out/result.json"},
		},
		Rules: []ir.Rule{
			{ID: "deny-agent", Effect: ir.EffectDeny, Actor: "agent",
				Operations: []ir.Operation{ir.OpWrite, ir.OpPublish}, Resource: "artifact"},
			{ID: "allow-publisher", Effect: ir.EffectAllow, Actor: "publisher",
				Operations: []ir.Operation{ir.OpPublish}, Resource: "artifact",
				Requires: []ir.Requirement{
					{Kind: ir.ReqPolicyHashMatch},
					{Kind: ir.ReqReceiptStatus, Receipt: "deltawire.supervision.receipt.v1", Status: "released"},
				}},
		},
	}
}

func stage(t *testing.T, ws workspace.Layout, name, content string) string {
	t.Helper()
	p := ws.StagedPath(name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newWS(t *testing.T) workspace.Layout {
	t.Helper()
	ws, err := workspace.New(filepath.Join(t.TempDir(), "run"))
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func goodReq(ws workspace.Layout, staged string) PublishRequest {
	return PublishRequest{
		RunID: "run1", RequestID: "pub1", Actor: "publisher",
		ResourceURI: "repo://out/result.json", Kind: ir.KindFile,
		StagedPath: staged, TargetPath: ws.ProtectedPath("result.json"),
		Upstream: []UpstreamReceipt{{Schema: "deltawire.supervision.receipt.v1", Status: "released", RunID: "run1"}},
	}
}

func TestPublisherWithEvidenceSucceeds(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", `{"ok":true}`)
	res, err := Publish(p, goodReq(ws, staged), receipt.NewChain("run1"))
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	got, _ := os.ReadFile(res.PublishedTo)
	if string(got) != `{"ok":true}` {
		t.Fatalf("published wrong bytes: %q", got)
	}
	if res.PublishedHash != res.StagedHash {
		t.Fatal("published hash != staged hash")
	}
}

func TestAgentCannotPublish(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(ws, staged)
	req.Actor = "agent"
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("agent publish should be denied, got %v", err)
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact was created despite denial")
	}
}

func TestMissingEvidenceFailsClosed(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(ws, staged)
	req.Upstream = nil // no supervision receipt
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("publish without evidence should fail")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact created despite missing evidence")
	}
}

func TestCrossRunEvidenceRejected(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(ws, staged)
	req.Upstream[0].RunID = "otherrun"
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("cross-run evidence should be rejected")
	}
}

func TestRefusesToOverwriteUnexpectedTarget(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "new")
	// Pre-existing target with no ExpectedTargetHash → refuse.
	if err := os.WriteFile(ws.ProtectedPath("result.json"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Publish(p, goodReq(ws, staged), receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("should refuse to overwrite an unexpected target")
	}
	got, _ := os.ReadFile(ws.ProtectedPath("result.json"))
	if string(got) != "old" {
		t.Fatalf("target was modified on a fail-safe path: %q", got)
	}
}

func TestStaleTargetHashFailsClosed(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "new")
	if err := os.WriteFile(ws.ProtectedPath("result.json"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := goodReq(ws, staged)
	req.ExpectedTargetHash = ir.HashBytes([]byte("stale-not-current"))
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("stale expected target hash should fail closed")
	}
}

func TestReplaceWhenExpectedHashMatches(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "new")
	if err := os.WriteFile(ws.ProtectedPath("result.json"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := goodReq(ws, staged)
	req.ExpectedTargetHash = ir.HashBytes([]byte("current"))
	if _, err := Publish(p, req, receipt.NewChain("run1")); err != nil {
		t.Fatalf("expected-hash replace should succeed: %v", err)
	}
	got, _ := os.ReadFile(ws.ProtectedPath("result.json"))
	if string(got) != "new" {
		t.Fatalf("expected replacement, got %q", got)
	}
}

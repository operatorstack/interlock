package broker

import (
	"encoding/json"
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

// writeEnvelope writes an arbitrary upstream evidence envelope into the run root
// and returns its path. Tests use it to forge the good case and every negative
// (wrong run, unbound artifact hash, missing/malformed file, bad status).
func writeEnvelope(t *testing.T, ws workspace.Layout, name string, env upstreamEnvelope) string {
	t.Helper()
	p := filepath.Join(ws.Root, name)
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// boundEnvelope writes a well-formed envelope hash-bound to the staged file's
// current bytes (the honest happy-path shape DeltaWire itself produces).
func boundEnvelope(t *testing.T, ws workspace.Layout, name, schema, status, runID, stagedPath string) string {
	t.Helper()
	b, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	return writeEnvelope(t, ws, name, upstreamEnvelope{
		Schema:         schema,
		RunID:          runID,
		Status:         status,
		ArtifactSHA256: ir.HashBytes(b),
	})
}

func goodReq(t *testing.T, ws workspace.Layout, staged string) PublishRequest {
	t.Helper()
	env := boundEnvelope(t, ws, "envelope.json", "deltawire.supervision.receipt.v1", "released", "run1", staged)
	return PublishRequest{
		RunID: "run1", RequestID: "pub1", Actor: "publisher",
		ResourceURI: "repo://out/result.json", Kind: ir.KindFile,
		StagedPath: staged, TargetPath: ws.ProtectedPath("result.json"),
		Upstream: []UpstreamReceipt{{Path: env}},
	}
}

func TestPublisherWithEvidenceSucceeds(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", `{"ok":true}`)
	res, err := Publish(p, goodReq(t, ws, staged), receipt.NewChain("run1"))
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
	req := goodReq(t, ws, staged)
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
	req := goodReq(t, ws, staged)
	req.Upstream = nil // no supervision receipt
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("publish without evidence should fail")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact created despite missing evidence")
	}
}

// An upstream receipt that names no durable envelope path must fail closed — the
// inline backdoor (trust a caller-claimed status) is locked shut.
func TestUpstreamWithoutEnvelopePathFailsClosed(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(t, ws, staged)
	req.Upstream = []UpstreamReceipt{{Path: ""}}
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("upstream receipt without an envelope path should fail closed")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact created despite missing envelope path")
	}
}

func TestCrossRunEvidenceRejected(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(t, ws, staged)
	// Envelope correlates to a different run than the request.
	env := boundEnvelope(t, ws, "cross-run-envelope.json", "deltawire.supervision.receipt.v1", "released", "otherrun", staged)
	req.Upstream = []UpstreamReceipt{{Path: env}}
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("cross-run evidence should be rejected")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact created despite cross-run evidence")
	}
}

// The envelope must be hash-bound to the exact staged bytes: an envelope that
// records a different artifact hash (e.g. copied from another candidate) fails.
func TestUnboundArtifactHashRejected(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(t, ws, staged)
	env := writeEnvelope(t, ws, "unbound-envelope.json", upstreamEnvelope{
		Schema: "deltawire.supervision.receipt.v1", RunID: "run1", Status: "released",
		ArtifactSHA256: ir.HashBytes([]byte("some other candidate")),
	})
	req.Upstream = []UpstreamReceipt{{Path: env}}
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("an envelope not bound to the staged bytes should be rejected")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact created despite unbound artifact hash")
	}
}

// A bare-hex artifact hash (DeltaWire's internal hashBytes format) is not the
// tagged "sha256:"+hex the broker computes, so it can never match — proving the
// strict single-format rule fails closed rather than silently accepting.
func TestBareHexArtifactHashRejected(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(t, ws, staged)
	tagged := ir.HashBytes([]byte("x"))
	bare := tagged[len("sha256:"):]
	env := writeEnvelope(t, ws, "bare-hex-envelope.json", upstreamEnvelope{
		Schema: "deltawire.supervision.receipt.v1", RunID: "run1", Status: "released",
		ArtifactSHA256: bare,
	})
	req.Upstream = []UpstreamReceipt{{Path: env}}
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("a bare-hex artifact hash should be rejected (wrong format)")
	}
}

func TestMissingEnvelopeFileFailsClosed(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(t, ws, staged)
	req.Upstream = []UpstreamReceipt{{Path: filepath.Join(ws.Root, "does-not-exist.json")}}
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("a missing envelope file should fail closed")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact created despite missing envelope file")
	}
}

func TestMalformedEnvelopeFailsClosed(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(t, ws, staged)
	bad := filepath.Join(ws.Root, "malformed-envelope.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	req.Upstream = []UpstreamReceipt{{Path: bad}}
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("a malformed envelope should fail closed")
	}
}

// The receipt status is taken from the envelope file, not any caller field: an
// envelope recording a non-released status does not satisfy the policy.
func TestStatusComesFromEnvelopeNotCaller(t *testing.T) {
	p := exclusivePublishPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "result.json", "x")
	req := goodReq(t, ws, staged)
	env := boundEnvelope(t, ws, "draft-envelope.json", "deltawire.supervision.receipt.v1", "draft", "run1", staged)
	req.Upstream = []UpstreamReceipt{{Path: env}}
	_, err := Publish(p, req, receipt.NewChain("run1"))
	if err == nil {
		t.Fatal("a non-released envelope status must not satisfy the policy")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("result.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected artifact created despite non-released status")
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
	_, err := Publish(p, goodReq(t, ws, staged), receipt.NewChain("run1"))
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
	req := goodReq(t, ws, staged)
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
	req := goodReq(t, ws, staged)
	req.ExpectedTargetHash = ir.HashBytes([]byte("current"))
	if _, err := Publish(p, req, receipt.NewChain("run1")); err != nil {
		t.Fatalf("expected-hash replace should succeed: %v", err)
	}
	got, _ := os.ReadFile(ws.ProtectedPath("result.json"))
	if string(got) != "new" {
		t.Fatalf("expected replacement, got %q", got)
	}
}

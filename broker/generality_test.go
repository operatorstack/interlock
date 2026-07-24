package broker

// These tests are the M3 generality proof: a SECOND, non-DeltaWire artifact
// driven through the exact same Publish path. If the broker or engine carried any
// DeltaWire-specific branch, this policy — different artifact URI, different
// actors, a different upstream receipt schema — would behave differently. It does
// not: the same allow/deny/fail-closed guarantees hold, tenant-agnostically.

import (
	"errors"
	"os"
	"testing"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/receipt"
	"github.com/operatorstack/interlock/workspace"
)

const releaseAttestationSchema = "release.attestation.v1"

// releaseManifestPolicy mirrors examples/release-manifest: a release bot may
// publish a manifest only with a matching policy hash, matching staged bytes, and
// an approved release attestation for the run. No DeltaWire schema appears.
func releaseManifestPolicy() ir.Policy {
	return ir.Policy{
		Protocol: ir.Protocol, PolicyID: "release-manifest.v1",
		Actors: []string{"build-runner", "release-bot"},
		Resources: []ir.Resource{
			{ID: "manifest", Kind: ir.KindFile, URI: "repo://dist/release-manifest.json"},
		},
		Rules: []ir.Rule{
			{ID: "deny-runner-manifest", Effect: ir.EffectDeny, Actor: "build-runner",
				Operations: []ir.Operation{ir.OpWrite, ir.OpPublish}, Resource: "manifest"},
			{ID: "allow-release-bot", Effect: ir.EffectAllow, Actor: "release-bot",
				Operations: []ir.Operation{ir.OpPublish}, Resource: "manifest",
				Requires: []ir.Requirement{
					{Kind: ir.ReqPolicyHashMatch},
					{Kind: ir.ReqStagedHashMatch},
					{Kind: ir.ReqReceiptStatus, Receipt: releaseAttestationSchema, Status: "approved"},
				}},
		},
	}
}

func releaseReq(t *testing.T, ws workspace.Layout, staged string) PublishRequest {
	t.Helper()
	env := boundEnvelope(t, ws, "rel-envelope.json", releaseAttestationSchema, "approved", "rel1", staged)
	return PublishRequest{
		RunID: "rel1", RequestID: "pub1", Actor: "release-bot",
		ResourceURI: "repo://dist/release-manifest.json", Kind: ir.KindFile,
		StagedPath: staged, TargetPath: ws.ProtectedPath("release-manifest.json"),
		Upstream: []UpstreamReceipt{{Path: env}},
	}
}

func TestGeneralityReleaseBotPublishes(t *testing.T) {
	p := releaseManifestPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "release-manifest.json", `{"version":"1.2.3"}`)
	res, err := Publish(p, releaseReq(t, ws, staged), receipt.NewChain("rel1"))
	if err != nil {
		t.Fatalf("release publish failed: %v", err)
	}
	got, _ := os.ReadFile(res.PublishedTo)
	if string(got) != `{"version":"1.2.3"}` {
		t.Fatalf("published wrong bytes: %q", got)
	}
	if res.PublishedHash != res.StagedHash {
		t.Fatal("published hash != staged hash")
	}
}

func TestGeneralityBuildRunnerCannotPublish(t *testing.T) {
	p := releaseManifestPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "release-manifest.json", "x")
	req := releaseReq(t, ws, staged)
	req.Actor = "build-runner"
	_, err := Publish(p, req, receipt.NewChain("rel1"))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("build-runner publish should be denied, got %v", err)
	}
	if _, statErr := os.Stat(ws.ProtectedPath("release-manifest.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected manifest was created despite denial")
	}
}

func TestGeneralityMissingAttestationFailsClosed(t *testing.T) {
	p := releaseManifestPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "release-manifest.json", "x")
	req := releaseReq(t, ws, staged)
	req.Upstream = nil // no release attestation
	_, err := Publish(p, req, receipt.NewChain("rel1"))
	if err == nil {
		t.Fatal("publish without an attestation should fail")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("release-manifest.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected manifest created despite missing attestation")
	}
}

// The broker matches the receipt schema the policy names — a DeltaWire receipt is
// not accepted for a release-manifest policy. This is the crux of the generality
// claim: the schema is data the policy carries, not a value baked into the core.
func TestGeneralityWrongSchemaFailsClosed(t *testing.T) {
	p := releaseManifestPolicy()
	ws := newWS(t)
	staged := stage(t, ws, "release-manifest.json", "x")
	req := releaseReq(t, ws, staged)
	// A DeltaWire-schema envelope (hash-bound and correlated) still must not
	// satisfy a release-manifest policy — the schema is policy data, not core.
	env := boundEnvelope(t, ws, "wrong-schema-envelope.json", "deltawire.supervision.receipt.v1", "released", "rel1", staged)
	req.Upstream = []UpstreamReceipt{{Path: env}}
	_, err := Publish(p, req, receipt.NewChain("rel1"))
	if err == nil {
		t.Fatal("a foreign receipt schema should not satisfy this policy")
	}
	if _, statErr := os.Stat(ws.ProtectedPath("release-manifest.json")); !os.IsNotExist(statErr) {
		t.Fatal("protected manifest created despite wrong-schema evidence")
	}
}

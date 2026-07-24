package main

// Slice 2 of the interface-optimization exercise: a SECOND, non-DeltaWire tenant
// publishes through the exact same interlock/publish façade DeltaWire uses. The
// only tenant-specific inputs are this tenant's own policy, schema, status, actor,
// and resource — there is no DeltaWire branch anywhere in the façade or broker.
// This proves the M3 generality claim at the ergonomic layer, not just the core.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/operatorstack/interlock/publish"
)

// releaseManifestPolicy compiles this example's builder to canonical IR and loads
// it through the same exported loader a tenant would use.
func releaseManifestPolicy(t *testing.T) publish.Policy {
	t.Helper()
	ir, err := Build().Emit()
	if err != nil {
		t.Fatalf("emit policy: %v", err)
	}
	p, err := publish.LoadPolicy(ir)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return p
}

// The release bot publishes an attested manifest through the shared façade, using
// its own schema and status — no DeltaWire assumptions.
func TestReleaseManifestPublishesViaFacade(t *testing.T) {
	policy := releaseManifestPolicy(t)
	dir := t.TempDir()

	const content = `{"version":"1.2.3"}`
	staged := filepath.Join(dir, "staging", "release-manifest.json")
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "dist", "release-manifest.json")
	evidenceDir := filepath.Join(dir, "evidence")

	res, err := publish.Publish(publish.Request{
		Policy:      policy,
		RunID:       "rel-run",
		RequestID:   "rel-1",
		Actor:       "release-bot",
		ResourceURI: "repo://dist/release-manifest.json",
		Kind:        publish.KindFile,
		StagedPath:  staged,
		TargetPath:  target,
		// Target must not exist yet.
		Upstream: []publish.Evidence{{
			Schema: ReleaseAttestationSchema,
			Status: "approved",
		}},
		EvidenceDir: evidenceDir,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read published: %v", err)
	}
	if string(got) != content {
		t.Fatalf("published bytes = %q, want %q", got, content)
	}
	if res.PublishedHash != publish.HashBytes([]byte(content)) {
		t.Fatalf("published hash %q != content hash", res.PublishedHash)
	}
}

// A foreign receipt schema must fail closed even through the ergonomic façade:
// schema is policy data, and the façade never interprets or privileges it.
func TestReleaseManifestForeignSchemaFailsClosed(t *testing.T) {
	policy := releaseManifestPolicy(t)
	dir := t.TempDir()

	const content = `{"version":"1.2.3"}`
	staged := filepath.Join(dir, "staging", "release-manifest.json")
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "dist", "release-manifest.json")

	_, err := publish.Publish(publish.Request{
		Policy:      policy,
		RunID:       "rel-run",
		RequestID:   "rel-2",
		Actor:       "release-bot",
		ResourceURI: "repo://dist/release-manifest.json",
		Kind:        publish.KindFile,
		StagedPath:  staged,
		TargetPath:  target,
		Upstream: []publish.Evidence{{
			// DeltaWire's schema — foreign to this tenant's policy.
			Schema: "deltawire.supervision.receipt.v1",
			Status: "released",
		}},
		EvidenceDir: filepath.Join(dir, "evidence"),
	})
	if err == nil {
		t.Fatal("publish accepted a foreign receipt schema")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("fail-closed publish still created the target")
	}
}

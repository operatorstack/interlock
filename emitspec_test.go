package interlock

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/operatorstack/interlock/compiler"
	"github.com/operatorstack/interlock/spec"
)

// build is the repository-policy shape, exercising actors, every resource kind
// used in the flagships, allow/deny ordering, and all requirement variants.
func build() *Builder {
	return Policy("emitspec.v1").
		Actor("agent").
		Actor("sdk-generator").
		Tree("source", "repo://src/**").
		Tree("generated", "repo://generated/**").
		Branch("main", "repo://branch/main").
		Allow("agent-source").By("agent").
		To(Read, Write, Delete, RenameFrom, RenameTo).On("source").
		Because("the agent may work freely in ordinary source code").Add().
		Deny("deny-agent-generated").By("agent").
		To(Write, Delete, RenameTo).On("generated").
		Because("generated files must be produced by the verified SDK generator").Add().
		Allow("publish-generated").By("sdk-generator").
		To(Publish).On("generated").
		Requiring(PolicyHashMatch(), StagedHashMatch(), ReceiptStatus("sdk-tests", "passed")).
		Because("the verified generator may publish generated files on passing tests").Add().
		Deny("deny-force-push-main").By("agent").
		To(ForcePush).On("main").
		Because("force-pushing the protected branch is not permitted").Add().
		Allow("push-main").By("agent").
		To(Push).On("main").
		Requiring(HumanApproval("release-main")).
		Because("pushing the protected branch requires human approval").Add()
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestEmitSpecCompilesToSameHash is the load-bearing guarantee for the Go
// authoring frontend: emitting spec.v1 and compiling it back must reproduce the
// exact canonical IR (and hash) that Emit() produces directly. spec.v1 is a
// lossless authoring layer over the one authority, not a second one.
func TestEmitSpecCompilesToSameHash(t *testing.T) {
	direct, err := build().Emit()
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	specBytes, err := build().EmitSpec()
	if err != nil {
		t.Fatalf("EmitSpec: %v", err)
	}

	decoded, err := spec.DecodeToSpec(specBytes)
	if err != nil {
		t.Fatalf("decode spec.v1: %v", err)
	}
	pol, err := compiler.Compile(decoded)
	if err != nil {
		t.Fatalf("compile decoded spec.v1: %v", err)
	}
	viaSpec, err := pol.CanonicalBytes()
	if err != nil {
		t.Fatalf("canonical bytes: %v", err)
	}

	if hashBytes(direct) != hashBytes(viaSpec) {
		t.Fatalf("hash mismatch:\n  Emit()          = %s\n  spec.v1→compile = %s", hashBytes(direct), hashBytes(viaSpec))
	}
	if string(direct) != string(viaSpec) {
		t.Fatal("canonical bytes differ between Emit() and spec.v1 round-trip")
	}
}

// TestEmitSpecRejectsInvalid ensures EmitSpec runs the compiler first, so a
// structurally broken policy fails at emit time rather than producing a spec.v1
// document that only breaks a downstream consumer.
func TestEmitSpecRejectsInvalid(t *testing.T) {
	// References an undeclared actor — compiler must reject.
	b := Policy("bad.v1").
		File("f", "repo://f").
		Allow("r").By("ghost").To(Write).On("f").Add()
	if _, err := b.EmitSpec(); err == nil {
		t.Fatal("expected EmitSpec to reject a policy with an undeclared actor")
	}
}

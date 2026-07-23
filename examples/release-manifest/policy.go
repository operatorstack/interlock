// Command release-manifest is a SECOND, non-DeltaWire tenant of the exact same
// broker. It exists to prove the M3 generality claim: the broker and engine carry
// no DeltaWire-specific branches. Nothing here mentions DeltaWire — a different
// artifact, different actors, and a different upstream receipt schema flow through
// the identical Publish path.
//
// The scenario: a release pipeline publishes a signed release manifest. A build
// runner may assemble the manifest in its workspace but may not write or publish
// the protected artifact; only the release bot may publish it, and only when the
// live policy hash matches, the staged bytes match, and an *approved* release
// attestation exists for the run.
//
// Running this program emits the canonical IR on stdout — what `interlock compile`
// consumes.
package main

import (
	"fmt"
	"os"

	il "github.com/operatorstack/interlock"
)

// ReleaseAttestationSchema is this tenant's own upstream receipt schema. It is
// deliberately unrelated to DeltaWire's supervision receipt — the broker treats
// whatever schema the policy names, with no built-in knowledge of either.
const ReleaseAttestationSchema = "release.attestation.v1"

// Build constructs the release-manifest policy. As with every Interlock policy,
// arbitrary Go may run here; only the emitted IR decides requests at runtime.
func Build() *il.Builder {
	return il.Policy("release-manifest.v1").
		Actor("build-runner").
		Actor("release-bot").
		File("manifest", "repo://dist/release-manifest.json").
		Tree("staging", "repo://build/**").
		Allow("runner-staging").By("build-runner").To(il.Write, il.Delete).On("staging").
		Because("the build runner may assemble the manifest in its own staging tree").Add().
		Deny("deny-runner-manifest").By("build-runner").To(il.Write, il.Publish).On("manifest").
		Because("the build runner may not touch the protected release manifest").Add().
		Allow("allow-release-bot").By("release-bot").To(il.Publish).On("manifest").
		Requiring(
			il.PolicyHashMatch(),
			il.StagedHashMatch(),
			il.ReceiptStatus(ReleaseAttestationSchema, "approved"),
		).
		Because("the verified release bot may publish an attested manifest").Add()
}

func main() {
	b, err := Build().Emit()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(b)
}

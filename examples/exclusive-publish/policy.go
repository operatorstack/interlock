// Command exclusive-publish is the DeltaWire-shaped example policy: the producing
// agent may work freely in its workspace but may NOT write or publish the
// protected artifact; only a verified publisher may publish a staged candidate,
// and only when the live policy hash matches and a released DeltaWire supervision
// receipt exists. Running this program emits the canonical IR on stdout — that is
// what `interlock compile` consumes.
package main

import (
	"fmt"
	"os"

	il "github.com/operatorstack/interlock"
)

// Build constructs the policy. Arbitrary Go may run here; only the emitted IR
// decides requests at runtime.
func Build() *il.Builder {
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
		Requiring(
			il.PolicyHashMatch(),
			il.StagedHashMatch(),
			il.ReceiptStatus("deltawire.supervision.receipt.v1", "released"),
		).
		Because("the verified publisher may publish a staged candidate").Add()
}

func main() {
	b, err := Build().Emit()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(b)
}

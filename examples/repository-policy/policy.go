// Command repository-policy is the flagship coding-agent policy: it makes a
// repository's ownership conventions executable instead of advisory. A coding
// agent works freely in source, but generated files are owned by the build (only
// a verified generator may publish them, and only on correlated evidence), and
// the protected branch cannot be force-pushed or pushed to without human
// approval. The Go here only *constructs* the policy; the emitted IR is a plain,
// canonical, hashable decision table.
package main

import (
	"fmt"
	"os"

	il "github.com/operatorstack/interlock"
)

// Build assembles the repository policy. Rule order is significant (the decision
// table is first-match), so the denies that carve exceptions out of a broad
// allow are authored before the allows they must override.
func Build() *il.Builder {
	return il.Policy("repository-policy.v1").
		Actor("agent").
		Actor("sdk-generator").
		Tree("source", "repo://src/**").
		Tree("generated", "repo://generated/**").
		Branch("main", "repo://branch/main").
		// The agent works freely in ordinary source code.
		Allow("agent-source").By("agent").
		To(il.Read, il.Write, il.Delete, il.RenameFrom, il.RenameTo).On("source").
		Because("the agent may work freely in ordinary source code").Add().
		// Generated files are owned by the build, not the agent.
		Deny("deny-agent-generated").By("agent").
		To(il.Write, il.Delete, il.RenameTo).On("generated").
		Because("generated files must be produced by the verified SDK generator").Add().
		// Only the generator may publish them, and only with current evidence.
		Allow("publish-generated").By("sdk-generator").
		To(il.Publish).On("generated").
		Requiring(
			il.PolicyHashMatch(),
			il.StagedHashMatch(),
			il.ReceiptStatus("sdk-tests", "passed"),
		).
		Because("the verified generator may publish generated files on passing tests").Add().
		// Ordinary branch safety.
		Deny("deny-force-push-main").By("agent").
		To(il.ForcePush).On("main").
		Because("force-pushing the protected branch is not permitted").Add().
		// A push to main is allowed only with human approval; absent the claim the
		// engine returns require, so the push fails closed at the enforcement point.
		Allow("push-main").By("agent").
		To(il.Push).On("main").
		Requiring(il.HumanApproval("release-main")).
		Because("pushing the protected branch requires human approval").Add()
}

func main() {
	out, err := Build().Emit()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

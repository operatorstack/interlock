// Command generated-file-protection demonstrates that arbitrary Go may run at
// construction time to build a policy — here a helper loops over a list of
// generated-output globs and denies the agent write access to each — while the
// emitted IR remains a plain, canonical, hashable decision table. The Go is gone
// by decision time; only the lowered rules remain.
package main

import (
	"fmt"
	"os"
	"sort"

	il "github.com/operatorstack/interlock"
)

// protectGenerated adds, for each protected tree, a deny rule against the agent
// and an evidence-gated allow for the publisher. This is ordinary Go — loops,
// slices, helpers — none of which survives into the IR. Keys are visited in
// sorted order because rule order is semantically meaningful (the decision table
// is first-match), so the emitted IR must not depend on map iteration order.
func protectGenerated(b *il.Builder, trees map[string]string) *il.Builder {
	ids := make([]string, 0, len(trees))
	for id := range trees {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		uri := trees[id]
		b = b.Tree(id, uri)
		b = b.Deny("deny-agent-"+id).By("agent").
			To(il.Write, il.Delete, il.Publish).On(id).
			Because("generated tree " + id + " is owned by the build, not the agent").Add()
		b = b.Allow("publish-"+id).By("publisher").
			To(il.Publish).On(id).
			Requiring(il.PolicyHashMatch(), il.StagedHashMatch()).
			Because("the build publisher may refresh " + id).Add()
	}
	return b
}

// Build constructs the policy over a deterministically ordered set of trees.
func Build() *il.Builder {
	b := il.Policy("generated-file-protection.v1").
		Actor("agent").
		Actor("publisher")
	// protectGenerated visits keys in sorted order, so the emitted IR is
	// deterministic regardless of map iteration. (See TestArbitraryConstructionSameIR.)
	return protectGenerated(b, map[string]string{
		"pb-descriptors": "repo://gen/pb/**",
		"openapi":        "repo://gen/openapi/**",
	})
}

func main() {
	out, err := Build().Emit()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

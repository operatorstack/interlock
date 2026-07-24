package scaffold

import (
	il "github.com/operatorstack/interlock"
	"github.com/operatorstack/interlock/protocol"
)

// Demo is a built-in, self-contained policy showcase behind `interlock demo`. It
// reuses Template's machinery — an in-process il builder plus labeled vectors —
// so a demo is compiled by the real compiler and every narrated scenario is a
// live engine.Decide result, not a canned string. Demos are kept separate from
// Templates() so the `interlock init` menu is unaffected: a demo is something you
// watch, a template is something you scaffold.
type Demo = Template

// Demos returns the ordered set of built-in policy showcases.
func Demos() []Demo {
	return []Demo{demoRepositoryPolicy}
}

// DemoByKey returns the demo with the given key.
func DemoByKey(key string) (Demo, bool) {
	for _, d := range Demos() {
		if d.Key == key {
			return d, true
		}
	}
	return Demo{}, false
}

// DemoKeys returns the demo keys in order.
func DemoKeys() []string {
	ds := Demos()
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Key
	}
	return out
}

// demoRepositoryPolicy mirrors examples/repository-policy: a coding agent works
// freely in source, generated files are owned by a verified SDK generator (and
// only refreshed on correlated evidence), and the protected branch cannot be
// force-pushed or pushed to without human approval. The narrated scenarios cover
// all three outcomes — allow, deny, and require (fail-closed on missing
// evidence) — so a just-installed binary demonstrates the engine end to end.
var demoRepositoryPolicy = Demo{
	Key:     "repository-policy",
	Title:   "Repository policy (the flagship coding-agent policy)",
	Summary: "A coding agent works freely in source, generated files are owned by the build, and the protected branch is force-push-proof and approval-gated.",
	build: func(string) *il.Builder {
		return il.Policy("repository-policy.v1").
			Actor("agent").
			Actor("sdk-generator").
			Tree("source", "repo://src/**").
			Tree("generated", "repo://generated/**").
			Branch("main", "repo://branch/main").
			Allow("agent-source").By("agent").
			To(il.Read, il.Write, il.Delete, il.RenameFrom, il.RenameTo).On("source").
			Because("the agent may work freely in ordinary source code").Add().
			Deny("deny-agent-generated").By("agent").
			To(il.Write, il.Delete, il.RenameTo).On("generated").
			Because("generated files must be produced by the verified SDK generator").Add().
			Allow("publish-generated").By("sdk-generator").
			To(il.Publish).On("generated").
			Requiring(
				il.PolicyHashMatch(),
				il.StagedHashMatch(),
				il.ReceiptStatus("sdk-tests", "passed"),
			).
			Because("the verified generator may publish generated files on passing tests").Add().
			Deny("deny-force-push-main").By("agent").
			To(il.ForcePush).On("main").
			Because("force-pushing the protected branch is not permitted").Add().
			Allow("push-main").By("agent").
			To(il.Push).On("main").
			Requiring(il.HumanApproval("release-main")).
			Because("pushing the protected branch requires human approval").Add()
	},
	vectors: func(string) []Vector {
		return []Vector{
			{Name: "agent edits source", Request: req("agent", il.Write, il.TreeKind, "repo://src/app.ts"), Expect: protocol.OutcomeAllow, ExpectRuleID: "agent-source"},
			{Name: "agent edits a generated file", Request: req("agent", il.Write, il.TreeKind, "repo://generated/client.ts"), Expect: protocol.OutcomeDeny, ExpectRuleID: "deny-agent-generated"},
			{Name: "generator publishes without evidence", Request: req("sdk-generator", il.Publish, il.TreeKind, "repo://generated/client.ts"), Expect: protocol.OutcomeRequire, ExpectRuleID: "publish-generated"},
			{Name: "agent force-pushes main", Request: req("agent", il.ForcePush, il.BranchKind, "repo://branch/main"), Expect: protocol.OutcomeDeny, ExpectRuleID: "deny-force-push-main"},
			{Name: "agent pushes main without approval", Request: req("agent", il.Push, il.BranchKind, "repo://branch/main"), Expect: protocol.OutcomeRequire, ExpectRuleID: "push-main"},
			{Name: "agent pushes main with approval", Request: req("agent", il.Push, il.BranchKind, "repo://branch/main", approval("release-main")), Expect: protocol.OutcomeAllow, ExpectRuleID: "push-main"},
		}
	},
	rules: func(string) []string {
		return []string{
			"`agent` may read and write anything under `repo://src/**`.",
			"`agent` may not write, delete, or rename anything under `repo://generated/**`.",
			"`sdk-generator` may publish under `repo://generated/**` only with a matching policy hash, staged hash, and a passing `sdk-tests` receipt.",
			"`agent` may not force-push `repo://branch/main`.",
			"`agent` may push `repo://branch/main` only with a `release-main` human approval; absent it the push fails closed (require).",
		}
	},
}

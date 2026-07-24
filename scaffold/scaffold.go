// Package scaffold holds the declarative starter templates behind
// `interlock init` (the no-toolchain JSON authoring path). Each template produces
// three things from data alone: a canonical policy (built in-process via the il
// builder, so no `go run` is needed), a set of labeled test vectors, and a README.
//
// The templates are the same shapes the examples/ modules encode, re-expressed as
// in-process builder calls (the examples are package main and cannot be imported).
// Because a template's policy is compiled by the real compiler and its vectors are
// re-decided by the real engine (see scaffold_test.go), a freshly `init`'d project
// is guaranteed green on `interlock test` on day one — the printed PASS lines are
// real decisions, not decoration.
package scaffold

import (
	il "github.com/operatorstack/interlock"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

// Vector is one row of .interlock/tests.jsonl: a human-readable label, the effect
// request to evaluate, and the outcome it must produce. It intentionally does NOT
// inline the policy (unlike conformance.Case) — `interlock test` loads the sibling
// policy.json once and decides every vector against it.
type Vector struct {
	Name          string                 `json:"name"`
	Request       protocol.EffectRequest `json:"request"`
	UsePolicyHash bool                   `json:"use_policy_hash,omitempty"`
	Expect        protocol.Outcome       `json:"expect"`
	ExpectRuleID  string                 `json:"expect_rule_id,omitempty"`
}

// Template is one entry in the "What are you protecting?" menu.
type Template struct {
	// Key is the stable --template value (e.g. "main-branch"); Title is the menu
	// label; Summary is a one-line description used in the README.
	Key, Title, Summary string

	build   func(path string) *il.Builder
	vectors func(path string) []Vector
	rules   func(path string) []string // plain-English rule descriptions for the README
}

// Policy returns the template's canonical policy bytes. path is used only by the
// "custom" template (the protected glob); other templates ignore it.
func (t Template) Policy(path string) ([]byte, error) { return t.build(path).Emit() }

// Vectors returns the template's test vectors for the given custom path.
func (t Template) Vectors(path string) []Vector { return t.vectors(path) }

// Rules returns the template's plain-English rule descriptions.
func (t Template) Rules(path string) []string { return t.rules(path) }

// Readme renders the template's README.md.
func (t Template) Readme(path string) string {
	return renderReadme(t, path)
}

// Templates returns the ordered menu of starter templates.
func Templates() []Template {
	return []Template{
		templateGenerated,
		templateMainBranch,
		templateReleaseArtifact,
		templateCustom,
		templateEmpty,
	}
}

// ByKey returns the template with the given key.
func ByKey(key string) (Template, bool) {
	for _, t := range Templates() {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}

// Keys returns the template keys in menu order.
func Keys() []string {
	ts := Templates()
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Key
	}
	return out
}

// --- request helper -------------------------------------------------------

// req builds an effect request. The engine ignores RequestID/RunID/Protocol for
// decisions, but stamping the protocol keeps the emitted tests.jsonl honest.
func req(actor string, op ir.Operation, kind ir.ResourceKind, uri string, ev ...protocol.Evidence) protocol.EffectRequest {
	return protocol.EffectRequest{
		Protocol:  protocol.EffectRequestProtocol,
		RunID:     "test",
		Actor:     actor,
		Operation: op,
		Resource:  protocol.TargetResource{Kind: kind, URI: uri},
		Evidence:  ev,
	}
}

func approval(id string) protocol.Evidence {
	return protocol.Evidence{Kind: ir.ReqHumanApproval, Value: id}
}

// sampleURI turns a protected-tree glob into a concrete member URI for a test
// request (e.g. "repo://protected/**" → "repo://protected/file").
func sampleURI(path string) string {
	if n := len(path); n >= 2 && path[n-2:] == "**" {
		return path[:n-2] + "file"
	}
	return path
}

// --- template 1: generated files -----------------------------------------

var templateGenerated = Template{
	Key:     "generated",
	Title:   "Generated files",
	Summary: "Generated files are owned by the build: the agent may not touch them, and only a verified publisher may refresh them on evidence.",
	build: func(string) *il.Builder {
		return il.Policy("generated-file-protection.v1").
			Actor("agent").
			Actor("publisher").
			Tree("source", "repo://src/**").
			Tree("generated", "repo://generated/**").
			Allow("agent-source").By("agent").
			To(il.Read, il.Write, il.Delete, il.RenameFrom, il.RenameTo).On("source").
			Because("the agent may work freely in ordinary source code").Add().
			Deny("deny-agent-generated").By("agent").
			To(il.Write, il.Delete, il.Publish).On("generated").
			Because("generated files are produced by the build, not the agent").Add().
			Allow("publish-generated").By("publisher").
			To(il.Publish).On("generated").
			Requiring(il.PolicyHashMatch(), il.StagedHashMatch()).
			Because("the verified publisher may refresh generated files on evidence").Add()
	},
	vectors: func(string) []Vector {
		return []Vector{
			{Name: "agent may edit src/**", Request: req("agent", il.Write, il.TreeKind, "repo://src/app.ts"), Expect: protocol.OutcomeAllow, ExpectRuleID: "agent-source"},
			{Name: "agent may not edit generated/**", Request: req("agent", il.Write, il.TreeKind, "repo://generated/client.ts"), Expect: protocol.OutcomeDeny, ExpectRuleID: "deny-agent-generated"},
			{Name: "publisher must supply evidence to refresh generated/**", Request: req("publisher", il.Publish, il.TreeKind, "repo://generated/client.ts"), Expect: protocol.OutcomeRequire, ExpectRuleID: "publish-generated"},
		}
	},
	rules: func(string) []string {
		return []string{
			"`agent` may read and write anything under `repo://src/**`.",
			"`agent` may not write, delete, or publish anything under `repo://generated/**`.",
			"`publisher` may publish under `repo://generated/**` only with a matching policy hash and staged hash.",
		}
	},
}

// --- template 2: main branch ---------------------------------------------

var templateMainBranch = Template{
	Key:     "main-branch",
	Title:   "Main branch",
	Summary: "The protected branch cannot be force-pushed, and a push requires human approval.",
	build: func(string) *il.Builder {
		return il.Policy("main-branch-protection.v1").
			Actor("agent").
			Tree("source", "repo://src/**").
			Branch("main", "repo://branch/main").
			Allow("agent-source").By("agent").
			To(il.Read, il.Write, il.Delete, il.RenameFrom, il.RenameTo).On("source").
			Because("the agent may work freely in ordinary source code").Add().
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
			{Name: "agent may edit src/**", Request: req("agent", il.Write, il.TreeKind, "repo://src/app.ts"), Expect: protocol.OutcomeAllow, ExpectRuleID: "agent-source"},
			{Name: "agent may not force-push main", Request: req("agent", il.ForcePush, il.BranchKind, "repo://branch/main"), Expect: protocol.OutcomeDeny, ExpectRuleID: "deny-force-push-main"},
			{Name: "push to main requires release-main approval", Request: req("agent", il.Push, il.BranchKind, "repo://branch/main"), Expect: protocol.OutcomeRequire, ExpectRuleID: "push-main"},
			{Name: "push to main with approval is allowed", Request: req("agent", il.Push, il.BranchKind, "repo://branch/main", approval("release-main")), Expect: protocol.OutcomeAllow, ExpectRuleID: "push-main"},
		}
	},
	rules: func(string) []string {
		return []string{
			"`agent` may read and write anything under `repo://src/**`.",
			"`agent` may not force-push `repo://branch/main`.",
			"`agent` may push `repo://branch/main` only with a `release-main` human approval; absent it the push fails closed (require).",
		}
	},
}

// --- template 3: release artifact ----------------------------------------

var templateReleaseArtifact = Template{
	Key:     "release-artifact",
	Title:   "A release artifact",
	Summary: "A release artifact is produced by the agent but published only by a verified publisher on evidence.",
	build: func(string) *il.Builder {
		return il.Policy("release-artifact-protection.v1").
			Actor("agent").
			Actor("publisher").
			Tree("workspace", "repo://work/**").
			File("artifact", "repo://out/result.json").
			Allow("agent-workspace").By("agent").
			To(il.Read, il.Write, il.Delete).On("workspace").
			Because("the agent may work freely in its workspace").Add().
			Deny("deny-agent-artifact").By("agent").
			To(il.Write, il.Publish).On("artifact").
			Because("the producing agent may not publish the protected artifact").Add().
			Allow("publish-artifact").By("publisher").
			To(il.Publish).On("artifact").
			Requiring(il.PolicyHashMatch(), il.StagedHashMatch()).
			Because("the verified publisher may publish a staged candidate on evidence").Add()
	},
	vectors: func(string) []Vector {
		return []Vector{
			{Name: "agent may work in the workspace", Request: req("agent", il.Write, il.TreeKind, "repo://work/tmp/scratch.txt"), Expect: protocol.OutcomeAllow, ExpectRuleID: "agent-workspace"},
			{Name: "agent may not publish the release artifact", Request: req("agent", il.Publish, il.FileKind, "repo://out/result.json"), Expect: protocol.OutcomeDeny, ExpectRuleID: "deny-agent-artifact"},
			{Name: "publisher must supply evidence to publish", Request: req("publisher", il.Publish, il.FileKind, "repo://out/result.json"), Expect: protocol.OutcomeRequire, ExpectRuleID: "publish-artifact"},
		}
	},
	rules: func(string) []string {
		return []string{
			"`agent` may read and write anything under `repo://work/**`.",
			"`agent` may not write or publish `repo://out/result.json`.",
			"`publisher` may publish `repo://out/result.json` only with a matching policy hash and staged hash.",
		}
	},
}

// --- template 4: custom protected path -----------------------------------

const defaultCustomPath = "repo://protected/**"

var templateCustom = Template{
	Key:     "custom",
	Title:   "A custom protected path",
	Summary: "A path of your choosing is off-limits to the agent, while ordinary source stays editable.",
	build: func(path string) *il.Builder {
		if path == "" {
			path = defaultCustomPath
		}
		return il.Policy("custom-path-protection.v1").
			Actor("agent").
			Tree("source", "repo://src/**").
			Tree("protected", path).
			Allow("agent-source").By("agent").
			To(il.Read, il.Write, il.Delete, il.RenameFrom, il.RenameTo).On("source").
			Because("the agent may work freely in ordinary source code").Add().
			Deny("deny-agent-protected").By("agent").
			To(il.Write, il.Delete, il.Publish).On("protected").
			Because("the protected path is off-limits to the agent").Add()
	},
	vectors: func(path string) []Vector {
		if path == "" {
			path = defaultCustomPath
		}
		return []Vector{
			{Name: "agent may edit src/**", Request: req("agent", il.Write, il.TreeKind, "repo://src/app.ts"), Expect: protocol.OutcomeAllow, ExpectRuleID: "agent-source"},
			{Name: "agent may not edit " + path, Request: req("agent", il.Write, il.TreeKind, sampleURI(path)), Expect: protocol.OutcomeDeny, ExpectRuleID: "deny-agent-protected"},
		}
	},
	rules: func(path string) []string {
		if path == "" {
			path = defaultCustomPath
		}
		return []string{
			"`agent` may read and write anything under `repo://src/**`.",
			"`agent` may not write, delete, or publish anything under `" + path + "`.",
		}
	},
}

// --- template 5: empty ----------------------------------------------------

var templateEmpty = Template{
	Key:     "empty",
	Title:   "Start from an empty policy",
	Summary: "An empty policy that denies everything by default — add your own rules.",
	build: func(string) *il.Builder {
		return il.Policy("empty.v1").Actor("agent")
	},
	vectors: func(string) []Vector {
		return []Vector{
			{Name: "unmatched requests default-deny", Request: req("agent", il.Write, il.FileKind, "repo://anything"), Expect: protocol.OutcomeDeny},
		}
	},
	rules: func(string) []string {
		return []string{
			"No rules yet. Every request has no matching rule, so the engine returns the default `deny` (fail-closed).",
			"Add resources and rules to `policy.json`, then add matching vectors to `tests.jsonl`.",
		}
	},
}

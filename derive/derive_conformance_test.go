package derive

// control-law: derivation-proposes-never-enforces
//
// Boundary: the transition from advisory repository intent (prose + machine
// config) to enforceable Interlock authority (a rule in the active canonical
// policy the engine decides on). The boundary has a checkpoint (derive → a
// candidate, never active) and a gate (explicit human promotion → compiler.Compile
// → active).
//
// Control law: a derived artifact may only be a candidate — grounded in cited
// evidence, expressed strictly within the closed V1 vocabulary, neither broader
// nor narrower than its source. Derivation never activates policy, never
// manufactures authority from advisory language, and never weakens an existing
// policy.
//
// This suite realizes the five conformance categories (positive, negative,
// relation, bypass, failure-state) plus determinism/parity, each asserting one or
// more of the decomposed invariants. It exercises the REAL compiler and engine —
// no mock authority — so a passing candidate is one the production decision path
// accepts.

import (
	"strings"
	"testing"

	"github.com/operatorstack/interlock/compiler"
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/spec"
)

// --- POSITIVE: a grounded prohibition becomes a proven deny rule -------------

func TestConformance_Positive_GroundedProhibition(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"AGENTS.md": "# Agent rules\n\n- Never force-push the main branch.\n",
	})
	res := mustDerive(t, root, nil)

	rec := findProposed(res.Derivation, func(r Record) bool {
		return r.Effect == ir.EffectDeny && hasOp(r.Operations, ir.OpForcePush) && r.ResourceURI == "repo://branch/main"
	})
	if rec == nil {
		t.Fatalf("expected a proposed deny on force_push@repo://branch/main, got records: %+v", res.Derivation.Records)
	}
	// Invariant 1: no emitted rule without source{path,line,sha256} provenance.
	if rec.Source.Path == "" || rec.Source.LineStart == 0 || !strings.HasPrefix(rec.Source.SHA256, "sha256:") {
		t.Fatalf("emitted rule lacks provenance: %+v", rec.Source)
	}
	if res.Candidate.RuleCount != 1 {
		t.Fatalf("want exactly 1 emitted rule, got %d", res.Candidate.RuleCount)
	}
	// Invariant 9: the candidate compiles through the real compiler and its
	// vectors pass the real engine (blocking + scoping).
	decideVectors(t, res.Candidate)
}

// --- NEGATIVE: advisory/domain/ambiguous never become authority --------------

func TestConformance_Negative_NoUnfoundedAuthority(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"AGENTS.md": strings.Join([]string{
			"# Agent rules",
			"",
			"- Prefer functional components over class components.", // advisory (invariant 3)
			"- The API uses OAuth 2.0 for authentication.",          // domain
			"- Ask before publishing a release.",                    // effect, but resource ambiguous (invariant 4)
			"",
		}, "\n"),
	})
	res := mustDerive(t, root, nil)

	if res.Candidate.RuleCount != 0 {
		t.Fatalf("no rule should be emitted from advisory/domain/ambiguous text, got %d", res.Candidate.RuleCount)
	}
	// Advisory and domain lines must not surface as effect rules at all.
	for _, r := range res.Derivation.Records {
		if r.Class == ClassAdvisoryGuidance || r.Class == ClassDomainKnowledge {
			if r.Status == StatusProposed {
				t.Fatalf("advisory/domain became proposed: %+v", r)
			}
		}
	}
	// The ambiguous approval line must be an unresolved question, not a rule.
	q := findRecord(res.Derivation, func(r Record) bool {
		return r.Class == ClassHumanDecision && r.Status == StatusUnresolved
	})
	if q == nil || q.Question == "" {
		t.Fatalf("ambiguous approval statement should be an unresolved question, got: %+v", res.Derivation.Records)
	}
}

// --- RELATION: request → boundary → decision reaches the real authorities ----

func TestConformance_Relation_EndToEndReachesEngine(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"AGENTS.md": "- Do not edit generated files.\n",
	})
	res := mustDerive(t, root, nil)
	if res.Candidate.RuleCount == 0 {
		t.Fatal("expected a rule from an explicit prohibition")
	}
	// Compile the candidate spec through the REAL compiler, then decide a request
	// that the derived rule must block — proving the artifact reaches production
	// authority, not a stand-in.
	pol := compileCandidate(t, res.Candidate)
	d := engine.Decide(pol, request("agent", ir.OpWrite, ir.KindTree, "repo://generated/client.ts"))
	if d.Outcome != protocol.OutcomeDeny {
		t.Fatalf("derived rule did not block a generated-file write: %s", d.Outcome)
	}
}

// --- BYPASS: a candidate cannot reach the active table except via the compiler -

func TestConformance_Bypass_NeverWritesActivePolicy(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"AGENTS.md": "- Never force-push the main branch.\n",
	})
	res := mustDerive(t, root, nil)
	files, err := res.Files()
	if err != nil {
		t.Fatal(err)
	}
	// Invariant 8: derive emits candidates only; no artifact is a policy.json.
	for name := range files {
		if name == "policy.json" || strings.HasSuffix(name, "/policy.json") {
			t.Fatalf("derive emitted an active policy file %q", name)
		}
	}
	if _, ok := files[FileCandidatePolicy]; !ok {
		t.Fatal("candidate policy missing from output")
	}
	// The candidate is authoring input (spec.v1), not canonical IR — it only
	// becomes authority by running through compiler.Compile.
	if _, err := ir.LoadPolicy(files[FileCandidatePolicy]); err == nil {
		t.Fatal("candidate.policy.json decoded as canonical IR; it must be spec.v1 that requires compilation")
	}
	if _, err := spec.DecodeToSpec(files[FileCandidatePolicy]); err != nil {
		t.Fatalf("candidate.policy.json is not valid spec.v1: %v", err)
	}
}

// --- FAILURE-STATE: rejection/conflict leaves existing authority untouched ----

func TestConformance_FailureState_ConflictRejectsBothNoEmit(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"AGENTS.md": strings.Join([]string{
			"- Never force-push the main branch.",
			"- Require approval to force-push the main branch.",
			"",
		}, "\n"),
	})
	res := mustDerive(t, root, nil)

	// Invariant 7: conflicting sources → both rejected, no inferred winner.
	if res.Candidate.RuleCount != 0 {
		t.Fatalf("conflict must not emit any rule, got %d", res.Candidate.RuleCount)
	}
	rejected := 0
	for _, r := range res.Derivation.Records {
		if r.Status == StatusRejected {
			rejected++
			if r.RejectReason == "" {
				t.Fatalf("rejected record missing reason: %+v", r)
			}
		}
	}
	if rejected < 2 {
		t.Fatalf("expected both conflicting records rejected, got %d rejected", rejected)
	}
}

func TestConformance_FailureState_ExistingPolicyUnchanged(t *testing.T) {
	// An active policy that denies the agent writing generated files.
	existing := activeGeneratedPolicy(t)
	hashBefore, err := existing.Hash()
	if err != nil {
		t.Fatal(err)
	}
	canon, err := existing.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	root := writeRepo(t, map[string]string{
		"AGENTS.md":              "- Never force-push the main branch.\n",
		".interlock/policy.json": string(canon),
	})

	// Derive reads the active policy (for the weakening check) but writes nothing.
	_ = mustDerive(t, root, nil)

	after := readFile(t, root, ".interlock/policy.json")
	reloaded, err := ir.LoadPolicy(after)
	if err != nil {
		t.Fatal(err)
	}
	hashAfter, err := reloaded.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if hashBefore != hashAfter {
		t.Fatalf("active policy hash changed: %s -> %s", hashBefore, hashAfter)
	}
	if string(after) != string(canon) {
		t.Fatal("active policy bytes changed during derive")
	}
}

// --- DETERMINISM / PARITY -----------------------------------------------------

func TestConformance_Determinism_StableAndOrderIndependent(t *testing.T) {
	files := map[string]string{
		"AGENTS.md": "- Never force-push the main branch.\n",
		"CLAUDE.md": "- Do not edit generated files.\n",
	}
	root := writeRepo(t, files)

	// Repeated derive → byte-identical candidate + derivation (invariant 10).
	a := mustDerive(t, root, nil)
	b := mustDerive(t, root, nil)
	if string(a.Candidate.Spec) != string(b.Candidate.Spec) {
		t.Fatal("candidate spec not deterministic across runs")
	}
	da, _ := encodeDerivation(a.Derivation)
	db, _ := encodeDerivation(b.Derivation)
	if string(da) != string(db) {
		t.Fatal("derivation not deterministic across runs")
	}

	// --from order independence.
	o1 := mustDerive(t, root, []string{"AGENTS.md", "CLAUDE.md"})
	o2 := mustDerive(t, root, []string{"CLAUDE.md", "AGENTS.md"})
	if string(o1.Candidate.Spec) != string(o2.Candidate.Spec) {
		t.Fatal("candidate spec depends on --from order")
	}
}

// --- shared helpers -----------------------------------------------------------

func decideVectors(t *testing.T, cand Candidate) {
	t.Helper()
	pol := compileCandidate(t, cand)
	for _, v := range cand.Vectors {
		req := v.Request
		if v.UsePolicyHash {
			h, err := pol.Hash()
			if err != nil {
				t.Fatal(err)
			}
			req.ClaimedPolicyHash = h
		}
		d := engine.Decide(pol, req)
		if d.Outcome != v.Expect {
			t.Fatalf("vector %q: outcome %s, want %s", v.Name, d.Outcome, v.Expect)
		}
		if v.ExpectRuleID != "" && d.RuleID != v.ExpectRuleID {
			t.Fatalf("vector %q: rule %q, want %q", v.Name, d.RuleID, v.ExpectRuleID)
		}
		// A scoping vector (empty ExpectRuleID, deny) must be default-deny — proof
		// the deny rule does not over-reach (invariant 2).
		if v.ExpectRuleID == "" && v.Expect == protocol.OutcomeDeny && d.RuleID != "" {
			t.Fatalf("vector %q: scoping request matched rule %q; deny over-reaches", v.Name, d.RuleID)
		}
	}
}

func compileCandidate(t *testing.T, cand Candidate) ir.Policy {
	t.Helper()
	s, err := spec.DecodeToSpec(cand.Spec)
	if err != nil {
		t.Fatalf("candidate spec.v1 invalid: %v", err)
	}
	pol, err := compiler.Compile(s)
	if err != nil {
		t.Fatalf("candidate does not compile through the real compiler: %v", err)
	}
	return pol
}

func activeGeneratedPolicy(t *testing.T) ir.Policy {
	t.Helper()
	s := spec.Spec{
		PolicyID: "active.v1",
		Actors:   []spec.Actor{{ID: "agent"}},
		Resources: []spec.Resource{
			{ID: "generated", Kind: ir.KindTree, URI: "repo://generated/**"},
		},
		Rules: []spec.Rule{
			{ID: "deny-generated", Effect: ir.EffectDeny, Actor: "agent", Operations: []ir.Operation{ir.OpWrite}, Resource: "generated", Reason: "active policy"},
		},
	}
	pol, err := compiler.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return pol
}

func hasOp(ops []ir.Operation, want ir.Operation) bool {
	for _, o := range ops {
		if o == want {
			return true
		}
	}
	return false
}

package derive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/operatorstack/interlock/ir"
)

// derive_test.go holds the unit tests for the pure pipeline stages (classify,
// ground, conflicts, weakening, review) plus the shared test helpers the
// conformance suite also uses. Everything here runs against in-memory bytes; no
// stage reaches the network or a model.

// --- classify ---------------------------------------------------------------

func TestClassify_Precedence(t *testing.T) {
	c := deterministicClassifier{}
	cases := []struct {
		text string
		want Class
	}{
		{"Never force-push the main branch.", ClassEnforceableEffect},
		{"Do not edit generated files.", ClassEnforceableEffect},
		{"Require approval before publishing.", ClassHumanDecision},
		{"Run tests before pushing.", ClassVerificationRequirement},
		{"Prefer functional components over class components.", ClassAdvisoryGuidance},
		{"The API uses OAuth 2.0 for authentication.", ClassDomainKnowledge},
	}
	for _, tc := range cases {
		if got := c.Classify(tc.text); got != tc.want {
			t.Errorf("Classify(%q) = %s, want %s", tc.text, got, tc.want)
		}
	}
}

func TestClassify_SuggestOverrides(t *testing.T) {
	c := deterministicClassifier{}
	raw := RawStatement{Text: "Never force-push main.", Suggest: ClassUnresolved}
	if got := classify(raw, c); got != ClassUnresolved {
		t.Fatalf("Suggest should override classifier, got %s", got)
	}
}

// --- ground -----------------------------------------------------------------

func TestGround_Effect_Prohibition(t *testing.T) {
	rec := Record{Class: ClassEnforceableEffect, Excerpt: "Never force-push the main branch."}
	ground(&rec, "")
	if rec.Status != StatusProposed {
		t.Fatalf("grounded prohibition should be proposed, got %s (%s)", rec.Status, rec.Question)
	}
	if rec.Effect != ir.EffectDeny {
		t.Fatalf("effect = %s, want deny", rec.Effect)
	}
	if !hasOp(rec.Operations, ir.OpForcePush) {
		t.Fatalf("operations = %v, want force_push", rec.Operations)
	}
	if rec.ResourceKind != ir.KindBranch || rec.ResourceURI != "repo://branch/main" {
		t.Fatalf("resource = %s %s, want branch repo://branch/main", rec.ResourceKind, rec.ResourceURI)
	}
}

func TestGround_HumanDecision_Grounded(t *testing.T) {
	rec := Record{Class: ClassHumanDecision, Excerpt: "Require approval to force-push the main branch."}
	ground(&rec, "")
	if rec.Status != StatusProposed {
		t.Fatalf("grounded approval should be proposed, got %s (%s)", rec.Status, rec.Question)
	}
	if rec.Effect != ir.EffectAllow {
		t.Fatalf("effect = %s, want allow", rec.Effect)
	}
	if rec.Requirement == nil || rec.Requirement.Kind != ir.ReqHumanApproval {
		t.Fatalf("requirement = %+v, want human_approval", rec.Requirement)
	}
}

func TestGround_HumanDecision_AmbiguousResource(t *testing.T) {
	rec := Record{Class: ClassHumanDecision, Excerpt: "Ask before publishing a release."}
	ground(&rec, "")
	if rec.Status != StatusUnresolved {
		t.Fatalf("approval with no citable resource should be unresolved, got %s", rec.Status)
	}
	if rec.Question == "" {
		t.Fatal("unresolved record must carry a question")
	}
}

func TestGround_Verification_AlwaysUnresolved(t *testing.T) {
	// v1 never infers a verifier — a verification requirement is always a question.
	rec := Record{Class: ClassVerificationRequirement, Excerpt: "All changes must pass the test suite."}
	ground(&rec, "")
	if rec.Status != StatusUnresolved {
		t.Fatalf("verification should be unresolved in v1, got %s", rec.Status)
	}
}

func TestGround_Advisory_NotEmittable(t *testing.T) {
	rec := Record{Class: ClassAdvisoryGuidance, Excerpt: "Prefer functional components."}
	ground(&rec, "")
	if rec.Status == StatusProposed {
		t.Fatal("advisory guidance must never be proposed")
	}
}

// --- conflicts / weakening --------------------------------------------------

func TestDetectConflicts_RejectsBoth(t *testing.T) {
	records := []Record{
		{ID: "r1", Status: StatusProposed, Class: ClassEnforceableEffect, Actor: "agent",
			Effect: ir.EffectDeny, Operations: []ir.Operation{ir.OpForcePush}, ResourceURI: "repo://branch/main"},
		{ID: "r2", Status: StatusProposed, Class: ClassHumanDecision, Actor: "agent",
			Effect: ir.EffectAllow, Operations: []ir.Operation{ir.OpForcePush}, ResourceURI: "repo://branch/main"},
	}
	detectConflicts(records)
	for _, r := range records {
		if r.Status != StatusRejected || r.RejectReason == "" {
			t.Fatalf("record %s not rejected on conflict: %+v", r.ID, r)
		}
	}
}

func TestCheckWeakening_RejectsAllowOverExistingDeny(t *testing.T) {
	existing := activeGeneratedPolicy(t) // denies agent write on repo://generated/**
	records := []Record{
		{ID: "r1", Status: StatusProposed, Class: ClassHumanDecision, Actor: "agent",
			Effect: ir.EffectAllow, Operations: []ir.Operation{ir.OpWrite}, ResourceURI: "repo://generated/**"},
	}
	checkWeakening(records, existing)
	if records[0].Status != StatusRejected || records[0].RejectReason == "" {
		t.Fatalf("allow over an existing deny must be rejected: %+v", records[0])
	}
}

func TestCheckWeakening_LeavesUnrelatedAllow(t *testing.T) {
	existing := activeGeneratedPolicy(t)
	records := []Record{
		{ID: "r1", Status: StatusProposed, Class: ClassHumanDecision, Actor: "agent",
			Effect: ir.EffectAllow, Operations: []ir.Operation{ir.OpWrite}, ResourceURI: "repo://src/**"},
	}
	checkWeakening(records, existing)
	if records[0].Status != StatusProposed {
		t.Fatalf("unrelated allow should be untouched, got %s", records[0].Status)
	}
}

// --- review -----------------------------------------------------------------

func TestApplyAnswers_GroundsUnresolvedResource(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"AGENTS.md": "- Ask before publishing a release.\n",
	})
	res := mustDerive(t, root, nil)
	q := findRecord(res.Derivation, func(r Record) bool { return r.Status == StatusUnresolved })
	if q == nil {
		t.Fatal("expected an unresolved approval record")
	}
	// Answer with a concrete resource; ApplyAnswers should ground it to proposed.
	updated := ApplyAnswers(res.Derivation, map[string]string{q.ID: "repo://dist/**"})
	got := findRecord(updated, func(r Record) bool { return r.ID == q.ID })
	if got == nil || got.Status != StatusProposed {
		t.Fatalf("answered record should become proposed, got %+v", got)
	}
	// And the rebuilt candidate must now compile + decide cleanly.
	rebuilt, err := Rebuild(updated)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Candidate.RuleCount != 1 {
		t.Fatalf("rebuilt candidate should have 1 rule, got %d", rebuilt.Candidate.RuleCount)
	}
	decideVectors(t, rebuilt.Candidate)
}

func TestApplyAnswers_BlankSkips(t *testing.T) {
	root := writeRepo(t, map[string]string{"AGENTS.md": "- Ask before publishing a release.\n"})
	res := mustDerive(t, root, nil)
	q := findRecord(res.Derivation, func(r Record) bool { return r.Status == StatusUnresolved })
	if q == nil {
		t.Fatal("expected an unresolved record")
	}
	updated := ApplyAnswers(res.Derivation, map[string]string{q.ID: ""})
	got := findRecord(updated, func(r Record) bool { return r.ID == q.ID })
	if got == nil || got.Status != StatusUnresolved {
		t.Fatalf("blank answer should leave record unresolved, got %+v", got)
	}
}

// --- report round-trip ------------------------------------------------------

func TestDerivation_EncodeDecodeRoundTrip(t *testing.T) {
	root := writeRepo(t, map[string]string{"AGENTS.md": "- Never force-push the main branch.\n"})
	res := mustDerive(t, root, nil)
	b, err := encodeDerivation(res.Derivation)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeDerivation(b)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if back.Schema != res.Derivation.Schema || len(back.Records) != len(res.Derivation.Records) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", back, res.Derivation)
	}
}

func TestDecodeDerivation_FailsClosedOnUnknownField(t *testing.T) {
	if _, err := decodeDerivation([]byte(`{"schema":"interlock.derivation.v1","surprise":true}`)); err == nil {
		t.Fatal("decode should reject unknown fields")
	}
}

// --- shared helpers ---------------------------------------------------------

// writeRepo materializes a temp repository from a path→content map and returns
// its root. Nested paths (e.g. ".interlock/policy.json") are created as needed.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readFile(t *testing.T, root, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustDerive(t *testing.T, root string, from []string) Result {
	t.Helper()
	res, err := Derive(root, from)
	if err != nil {
		t.Fatalf("Derive(%s): %v", root, err)
	}
	return res
}

func findRecord(d Derivation, pred func(Record) bool) *Record {
	for i := range d.Records {
		if pred(d.Records[i]) {
			return &d.Records[i]
		}
	}
	return nil
}

func findProposed(d Derivation, pred func(Record) bool) *Record {
	return findRecord(d, func(r Record) bool { return r.Status == StatusProposed && pred(r) })
}

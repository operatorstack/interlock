package derive

import (
	"strings"

	"github.com/operatorstack/interlock/ir"
)

// review.go implements the pure answer-application step behind `--review`. It has
// no I/O: the command layer reads derivation.json, collects answers from stdin,
// calls ApplyAnswers, and re-emits the candidate. Keeping it pure makes it
// unit- and conformance-testable and deterministic (same answers → same output).
//
// ApplyAnswers only ever moves a record unresolved → proposed (or adds a
// reviewer-authored baseline allow). It never activates policy and never
// weakens one — promotion is still the separate compile step.

// ApplyAnswers returns a new Derivation with the given answers applied. answers
// is keyed by record ID (as printed in QUESTIONS.md); the special key "baseline"
// adds a reviewer-authored allow rule for the deny-only freeze case. An empty or
// "skip" answer leaves a record unresolved. The input is not mutated.
func ApplyAnswers(d Derivation, answers map[string]string) Derivation {
	out := Derivation{Schema: d.Schema, PolicyID: d.PolicyID}
	out.Records = make([]Record, len(d.Records))
	copy(out.Records, d.Records)

	for i := range out.Records {
		r := &out.Records[i]
		if r.Status != StatusUnresolved {
			continue
		}
		ans := strings.TrimSpace(answers[r.ID])
		if ans == "" || strings.EqualFold(ans, "skip") {
			continue
		}
		applyAnswer(r, ans)
	}

	if base := strings.TrimSpace(answers["baseline"]); base != "" && !strings.EqualFold(base, "skip") {
		if rec, ok := baselineRecord(base); ok {
			out.Records = append(out.Records, rec)
		}
	}
	return out
}

// applyAnswer fills a single unresolved record's missing fields from one answer,
// then re-checks completeness. The answer is interpreted by what is missing:
// verifier → "schema:status"; resource → a repo:// URI or path; operation → an
// operation keyword.
func applyAnswer(r *Record, ans string) {
	missing := map[string]bool{}
	for _, m := range r.Missing {
		missing[m] = true
	}

	if missing["verifier"] {
		schema, status := parseVerifier(ans)
		req := ir.Requirement{Kind: ir.ReqReceiptStatus, Receipt: schema, Status: status}
		r.Requirement = &req
		delete(missing, "verifier")
		// A verifier answer resolves only the verifier; other fields, if still
		// missing, keep the record unresolved below.
	} else if missing["resource"] {
		if kind, uri, ok := pathToResource(ans); ok {
			r.ResourceKind, r.ResourceURI = kind, uri
			delete(missing, "resource")
		}
	} else if missing["operation"] {
		if ops := parseOperations(ans); len(ops) > 0 {
			r.Operations = ops
			delete(missing, "operation")
		}
	}

	// Rebuild the remaining-missing list in a stable order.
	r.Missing = r.Missing[:0]
	for _, m := range []string{"verifier", "operation", "resource"} {
		if missing[m] {
			r.Missing = append(r.Missing, m)
		}
	}
	if len(r.Missing) > 0 {
		return
	}

	// Fully grounded: attach a human_approval requirement if this was a human
	// decision and none is set, refresh the reason, and mark proposed.
	if r.Class == ClassHumanDecision && r.Requirement == nil {
		req := ir.Requirement{Kind: ir.ReqHumanApproval, Approval: approvalID(r.Operations)}
		r.Requirement = &req
	}
	if r.Reason == "" {
		r.Reason = deriveReason(r)
	}
	r.Question = ""
	r.Status = StatusProposed
}

// baselineRecord builds the reviewer-authored allow rule for the freeze case. Its
// provenance is the human answer itself (path "review:baseline"), hash-bound like
// any other source — authority the reviewer explicitly granted, not invented by
// derive.
func baselineRecord(ans string) (Record, bool) {
	kind, uri, ok := pathToResource(ans)
	if !ok {
		return Record{}, false
	}
	excerpt := "baseline allow: " + ans
	rec := Record{
		ID:           "baseline",
		Source:       Source{Path: "review:baseline", SHA256: ir.HashBytes([]byte(excerpt))},
		Excerpt:      excerpt,
		Class:        ClassEnforceableEffect,
		Strength:     StrengthStrong,
		Status:       StatusProposed,
		Actor:        defaultActor,
		Operations:   []ir.Operation{ir.OpRead, ir.OpWrite},
		Effect:       ir.EffectAllow,
		ResourceKind: kind,
		ResourceURI:  uri,
		Reason:       "baseline access granted by reviewer via --review",
	}
	return rec, true
}

// parseVerifier splits a "schema:status" answer; a bare schema defaults to
// status "pass".
func parseVerifier(ans string) (schema, status string) {
	if i := strings.Index(ans, ":"); i >= 0 {
		return strings.TrimSpace(ans[:i]), strings.TrimSpace(ans[i+1:])
	}
	return ans, "pass"
}

// parseOperations reads an operation answer: either a full ir.Operation value or
// a keyword the detector understands (e.g. "publish", "force-push").
func parseOperations(ans string) []ir.Operation {
	for _, op := range ir.Operations {
		if string(op) == ans {
			return []ir.Operation{op}
		}
	}
	return detectOperations(ans)
}

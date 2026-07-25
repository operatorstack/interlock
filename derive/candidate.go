package derive

import (
	"sort"
	"strings"

	il "github.com/operatorstack/interlock"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/scaffold"
)

// candidate.go assembles the grounded, proposed records into the SAME artifacts
// interlock init emits: an interlock.spec.v1 document (via il.Builder.EmitSpec,
// which runs the real compiler to validate) and scaffold.Vector test rows. This
// is the crux of "propose, never enforce": the candidate is authoring input that
// only becomes authority when a human runs it back through compiler.Compile.

// Vector re-exports scaffold.Vector: derive emits the exact same test-row shape
// interlock init does, so `interlock test` reads a derived candidate unchanged.
type Vector = scaffold.Vector

// Candidate is the emittable output of a derivation.
type Candidate struct {
	Spec    []byte
	Vectors []Vector
	// FreezeWarning is set when the candidate contains deny rules but no allow
	// rule. Under Interlock's default-deny, such a policy would block everything,
	// so derive surfaces a baseline question rather than inventing an allow rule
	// (which would be unprovenanced authority).
	FreezeWarning bool
	RuleCount     int
}

// buildCandidate compiles the proposed records into a Candidate. A record is
// emitted only if it is StatusProposed and its class is Emittable — every other
// status (unresolved, rejected) is excluded, and every emitted rule carries a
// reason citing its source (invariant 1).
func buildCandidate(d Derivation) (Candidate, error) {
	b := il.Policy(d.PolicyID).Actor(defaultActor)

	// Deterministic resource registry keyed by URI.
	reg := newResReg()
	var emitted []Record
	for _, rec := range d.Records {
		if rec.Status != StatusProposed || !rec.Class.Emittable() {
			continue
		}
		if rec.ResourceURI == "" || len(rec.Operations) == 0 {
			continue // defensive: never emit a rule missing scope
		}
		emitted = append(emitted, rec)
		reg.add(rec.ResourceKind, rec.ResourceURI)
	}

	// Declare resources in stable (kind-order, then URI) order.
	for _, r := range reg.declared() {
		switch r.kind {
		case ir.KindFile:
			b.File(r.id, r.uri)
		case ir.KindTree:
			b.Tree(r.id, r.uri)
		case ir.KindProcess:
			b.Process(r.id, r.uri)
		case ir.KindBranch:
			b.Branch(r.id, r.uri)
		}
	}

	var vectors []scaffold.Vector
	hasAllow, hasDeny := false, false
	for _, rec := range emitted {
		resID := reg.idFor(rec.ResourceURI)
		ruleID := ruleID(rec, resID)
		rb := ruleBuilder(b, rec, ruleID, resID)
		rb.Add()
		if rec.Effect == ir.EffectDeny {
			hasDeny = true
		} else {
			hasAllow = true
		}
		vectors = append(vectors, vectorsFor(rec, ruleID)...)
	}

	specBytes, err := b.EmitSpec()
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		Spec:          specBytes,
		Vectors:       vectors,
		FreezeWarning: hasDeny && !hasAllow,
		RuleCount:     len(emitted),
	}, nil
}

// ruleBuilder maps a record onto the fluent builder in the closed vocabulary.
func ruleBuilder(b *il.Builder, rec Record, ruleID, resID string) *il.RuleBuilder {
	var rb *il.RuleBuilder
	if rec.Effect == ir.EffectDeny {
		rb = b.Deny(ruleID)
	} else {
		rb = b.Allow(ruleID)
	}
	rb = rb.By(rec.Actor).To(rec.Operations...).On(resID)
	if rec.Requirement != nil {
		rb = rb.Requiring(*rec.Requirement)
	}
	return rb.Because(rec.Reason)
}

// ruleID builds a unique, readable rule id: effect-resource-recordID. The record
// id suffix guarantees uniqueness even when two rules touch one resource.
func ruleID(rec Record, resID string) string {
	return string(rec.Effect) + "-" + resID + "-" + rec.ID
}

// vectorsFor generates the conformance vectors for one emitted rule (invariant 5).
//
//   - A deny rule gets a blocking vector (the cited effect → deny, attributed to
//     this rule) and a scoping vector (a sibling URI this rule must NOT catch →
//     default-deny with no rule id), proving the rule neither under- nor
//     over-reaches its cited scope.
//   - An allow+require rule gets a require vector (no evidence → require) and an
//     allowed vector (with the approval evidence → allow), proving the gate both
//     holds and opens.
func vectorsFor(rec Record, ruleID string) []scaffold.Vector {
	op := rec.Operations[0]
	member := memberURI(rec.ResourceKind, rec.ResourceURI)

	if rec.Effect == ir.EffectDeny {
		return []scaffold.Vector{
			{
				Name:         "derived: " + rec.ID + " blocks " + string(op),
				Request:      request(rec.Actor, op, rec.ResourceKind, member),
				Expect:       protocol.OutcomeDeny,
				ExpectRuleID: ruleID,
			},
			{
				Name:    "derived: " + rec.ID + " does not over-reach",
				Request: request(rec.Actor, op, rec.ResourceKind, scopingURI(rec.ResourceKind)),
				Expect:  protocol.OutcomeDeny, // default-deny, NOT attributed to this rule
			},
		}
	}

	// allow + human_approval
	approval := ""
	if rec.Requirement != nil {
		approval = rec.Requirement.Approval
	}
	return []scaffold.Vector{
		{
			Name:         "derived: " + rec.ID + " requires approval for " + string(op),
			Request:      request(rec.Actor, op, rec.ResourceKind, member),
			Expect:       protocol.OutcomeRequire,
			ExpectRuleID: ruleID,
		},
		{
			Name:         "derived: " + rec.ID + " allows " + string(op) + " with approval",
			Request:      request(rec.Actor, op, rec.ResourceKind, member, evidenceApproval(approval)),
			Expect:       protocol.OutcomeAllow,
			ExpectRuleID: ruleID,
		},
	}
}

// request builds a protocol.EffectRequest (local copy of scaffold's unexported
// req helper).
func request(actor string, op ir.Operation, kind ir.ResourceKind, uri string, ev ...protocol.Evidence) protocol.EffectRequest {
	return protocol.EffectRequest{
		Protocol:  protocol.EffectRequestProtocol,
		RunID:     "derive",
		Actor:     actor,
		Operation: op,
		Resource:  protocol.TargetResource{Kind: kind, URI: uri},
		Evidence:  ev,
	}
}

func evidenceApproval(id string) protocol.Evidence {
	return protocol.Evidence{Kind: ir.ReqHumanApproval, Value: id}
}

// memberURI turns a tree glob into a concrete member for a request; exact
// resources (files, branches) are used verbatim.
func memberURI(kind ir.ResourceKind, uri string) string {
	if kind == ir.KindTree && strings.HasSuffix(uri, "**") {
		return uri[:len(uri)-2] + "member"
	}
	return uri
}

// scopingURI is a URI outside any declared resource, used to prove a deny rule
// does not over-reach. It matches nothing, so the engine returns default-deny
// with an empty rule id.
func scopingURI(kind ir.ResourceKind) string {
	if kind == ir.KindBranch {
		return "repo://branch/__derive_unscoped__"
	}
	return "repo://__derive_unscoped__/probe"
}

// --- resource registry ----------------------------------------------------

type resEntry struct {
	id, uri string
	kind    ir.ResourceKind
}

type resReg struct {
	byURI map[string]resEntry
	ids   map[string]bool
}

func newResReg() *resReg {
	return &resReg{byURI: map[string]resEntry{}, ids: map[string]bool{}}
}

func (r *resReg) add(kind ir.ResourceKind, uri string) {
	if _, ok := r.byURI[uri]; ok {
		return
	}
	id := uniqueID(r.ids, resourceSlug(uri))
	r.ids[id] = true
	r.byURI[uri] = resEntry{id: id, uri: uri, kind: kind}
}

func (r *resReg) idFor(uri string) string { return r.byURI[uri].id }

// declared returns the resource entries in a stable order (by kind order, then
// URI) so the emitted policy is deterministic.
func (r *resReg) declared() []resEntry {
	out := make([]resEntry, 0, len(r.byURI))
	for _, e := range r.byURI {
		out = append(out, e)
	}
	kindRank := map[ir.ResourceKind]int{ir.KindFile: 0, ir.KindTree: 1, ir.KindProcess: 2, ir.KindBranch: 3}
	sort.Slice(out, func(i, j int) bool {
		if kindRank[out[i].kind] != kindRank[out[j].kind] {
			return kindRank[out[i].kind] < kindRank[out[j].kind]
		}
		return out[i].uri < out[j].uri
	})
	return out
}

// resourceSlug derives a readable resource id from a URI.
func resourceSlug(uri string) string {
	s := strings.TrimPrefix(uri, "repo://")
	s = strings.TrimSuffix(s, "/**")
	s = strings.TrimSuffix(s, "/")
	var b strings.Builder
	prevDash := false
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "resource"
	}
	return slug
}

func uniqueID(taken map[string]bool, base string) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := base + "-" + itoa(i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

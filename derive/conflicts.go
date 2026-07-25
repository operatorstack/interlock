package derive

import (
	"github.com/operatorstack/interlock/ir"
)

// conflicts.go enforces two fail-closed invariants:
//   - Conflicting sources produce a conflict, never an inferred winner (7).
//   - An existing active policy is never silently weakened (6).
// Both work by demoting affected records to StatusRejected with a reason, so they
// are recorded transparently in derivation.json but never emitted.

// detectConflicts scans the proposed records for the same (actor, operation,
// resource URI) governed by opposing effects. When found, BOTH records are
// rejected — derive does not pick a winner. Mutates records in place.
func detectConflicts(records []Record) {
	// key -> effects seen and the record indexes that produced them.
	type acc struct {
		effects map[ir.Effect]bool
		idx     []int
	}
	groups := map[string]*acc{}
	for i := range records {
		r := &records[i]
		if r.Status != StatusProposed || !r.Class.Emittable() {
			continue
		}
		for _, op := range r.Operations {
			k := r.Actor + "|" + string(op) + "|" + r.ResourceURI
			g := groups[k]
			if g == nil {
				g = &acc{effects: map[ir.Effect]bool{}}
				groups[k] = g
			}
			g.effects[r.Effect] = true
			g.idx = append(g.idx, i)
		}
	}
	for _, g := range groups {
		if len(g.effects) < 2 {
			continue
		}
		for _, i := range g.idx {
			records[i].Status = StatusRejected
			records[i].RejectReason = "conflicting sources declare both allow and deny for the same actor/operation/resource; derive does not infer a winner"
		}
	}
}

// checkWeakening rejects any proposed allow that would loosen a rule the existing
// active policy already denies. Derivation may only add restriction on top of an
// existing policy, never remove it. Mutates records in place.
func checkWeakening(records []Record, existing ir.Policy) {
	// Build the set of (actor, operation, resourceURI) the existing policy denies.
	denied := map[string]bool{}
	uriByID := map[string]string{}
	for _, res := range existing.Resources {
		uriByID[res.ID] = res.URI
	}
	for _, rule := range existing.Rules {
		if rule.Effect != ir.EffectDeny {
			continue
		}
		uri := uriByID[rule.Resource]
		for _, op := range rule.Operations {
			denied[rule.Actor+"|"+string(op)+"|"+uri] = true
		}
	}
	if len(denied) == 0 {
		return
	}
	for i := range records {
		r := &records[i]
		if r.Status != StatusProposed || r.Effect != ir.EffectAllow {
			continue
		}
		for _, op := range r.Operations {
			if denied[r.Actor+"|"+string(op)+"|"+r.ResourceURI] {
				r.Status = StatusRejected
				r.RejectReason = "would weaken the existing active policy, which denies this actor/operation/resource; derivation never loosens an existing rule"
				break
			}
		}
	}
}

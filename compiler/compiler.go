// Package compiler lowers a typed spec.Spec into a canonical ir.Policy. It is
// where a policy's structural validity is decided: an invalid policy fails here,
// before any request is ever evaluated. Compilation is deterministic — the same
// spec always produces the same IR bytes and hash — so equivalent policies are
// interchangeable by identity.
package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/spec"
)

// Diagnostics is a collection of compile errors. It is returned when a spec is
// structurally invalid and lists every problem found, sorted for stability.
type Diagnostics struct {
	Errors []string
}

func (d *Diagnostics) add(format string, args ...any) {
	d.Errors = append(d.Errors, fmt.Sprintf(format, args...))
}

func (d *Diagnostics) Error() string {
	return fmt.Sprintf("interlock: %d policy error(s):\n  - %s",
		len(d.Errors), strings.Join(d.Errors, "\n  - "))
}

// Compile validates s and returns its canonical IR. On any structural error it
// returns a *Diagnostics listing every problem.
func Compile(s spec.Spec) (ir.Policy, error) {
	var diag Diagnostics

	if strings.TrimSpace(s.PolicyID) == "" {
		diag.add("policy_id must be non-empty")
	}

	// Actors: reject duplicates and blanks; build the declared set.
	actorSet := map[string]bool{}
	for _, a := range s.Actors {
		if strings.TrimSpace(a.ID) == "" {
			diag.add("actor id must be non-empty")
			continue
		}
		if actorSet[a.ID] {
			diag.add("duplicate actor id: %q", a.ID)
			continue
		}
		actorSet[a.ID] = true
	}

	// Resources: reject duplicates, blanks, invalid kinds; build the set.
	resSet := map[string]ir.Resource{}
	for _, r := range s.Resources {
		if strings.TrimSpace(r.ID) == "" {
			diag.add("resource id must be non-empty")
			continue
		}
		if _, dup := resSet[r.ID]; dup {
			diag.add("duplicate resource id: %q", r.ID)
			continue
		}
		if !ir.ValidResourceKind(r.Kind) {
			diag.add("resource %q has unknown kind: %q", r.ID, r.Kind)
		}
		if strings.TrimSpace(r.URI) == "" {
			diag.add("resource %q must have a non-empty uri", r.ID)
		}
		resSet[r.ID] = ir.Resource{ID: r.ID, Kind: r.Kind, URI: r.URI}
	}

	// Rules: reject duplicate ids, unknown actor/resource/op, bad effect, empty
	// op sets, and require-on-deny. Track allow/deny conflicts and unreachable
	// rules shadowed by an earlier identical matcher.
	ruleIDs := map[string]bool{}
	type matcher struct {
		actor    string
		resource string
		ops      map[ir.Operation]bool
		effect   ir.Effect
		id       string
	}
	var seen []matcher

	for _, rule := range s.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			diag.add("rule id must be non-empty")
			continue
		}
		if ruleIDs[rule.ID] {
			diag.add("duplicate rule id: %q", rule.ID)
			continue
		}
		ruleIDs[rule.ID] = true

		if !ir.ValidEffect(rule.Effect) {
			diag.add("rule %q has invalid effect: %q", rule.ID, rule.Effect)
		}
		if !actorSet[rule.Actor] {
			diag.add("rule %q references undeclared actor: %q", rule.ID, rule.Actor)
		}
		if _, ok := resSet[rule.Resource]; !ok {
			diag.add("rule %q references undeclared resource: %q", rule.ID, rule.Resource)
		}
		if len(rule.Operations) == 0 {
			diag.add("rule %q must specify at least one operation", rule.ID)
		}
		opSet := map[ir.Operation]bool{}
		for _, op := range rule.Operations {
			if !ir.ValidOperation(op) {
				diag.add("rule %q has unknown operation: %q", rule.ID, op)
				continue
			}
			if opSet[op] {
				diag.add("rule %q lists operation %q more than once", rule.ID, op)
				continue
			}
			opSet[op] = true
		}
		if rule.Effect == ir.EffectDeny && len(rule.Requires) > 0 {
			diag.add("rule %q is a deny rule and cannot carry requirements", rule.ID)
		}
		for _, req := range rule.Requires {
			if !validRequirement(req) {
				diag.add("rule %q has invalid requirement: %+v", rule.ID, req)
			}
		}

		// Conflict / unreachability against earlier rules with overlapping ops.
		for _, prev := range seen {
			if prev.actor != rule.Actor || prev.resource != rule.Resource {
				continue
			}
			if !opsOverlap(prev.ops, opSet) {
				continue
			}
			if prev.effect != rule.Effect {
				diag.add("rule %q conflicts with earlier rule %q (same actor/resource/op, opposite effect)", rule.ID, prev.id)
			} else {
				diag.add("rule %q is unreachable: shadowed by earlier rule %q", rule.ID, prev.id)
			}
		}
		seen = append(seen, matcher{
			actor: rule.Actor, resource: rule.Resource, ops: opSet, effect: rule.Effect, id: rule.ID,
		})
	}

	if len(diag.Errors) > 0 {
		sort.Strings(diag.Errors)
		return ir.Policy{}, &diag
	}

	return lower(s), nil
}

// lower builds the canonical IR from a validated spec. Actors and resources are
// sorted for a stable canonical form; rules preserve authored order because the
// decision table is first-match and order is semantically meaningful.
func lower(s spec.Spec) ir.Policy {
	actors := make([]string, 0, len(s.Actors))
	for _, a := range s.Actors {
		actors = append(actors, a.ID)
	}
	sort.Strings(actors)

	resources := make([]ir.Resource, 0, len(s.Resources))
	for _, r := range s.Resources {
		resources = append(resources, ir.Resource{ID: r.ID, Kind: r.Kind, URI: r.URI})
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })

	rules := make([]ir.Rule, 0, len(s.Rules))
	for _, r := range s.Rules {
		ops := append([]ir.Operation(nil), r.Operations...)
		sort.Slice(ops, func(i, j int) bool { return ops[i] < ops[j] })
		rules = append(rules, ir.Rule{
			ID:         r.ID,
			Effect:     r.Effect,
			Actor:      r.Actor,
			Operations: ops,
			Resource:   r.Resource,
			Requires:   append([]ir.Requirement(nil), r.Requires...),
			Reason:     r.Reason,
		})
	}

	return ir.Policy{
		Protocol:  ir.Protocol,
		PolicyID:  s.PolicyID,
		Actors:    actors,
		Resources: resources,
		Rules:     rules,
	}
}

func validRequirement(req ir.Requirement) bool {
	switch req.Kind {
	case ir.ReqReceiptStatus:
		return req.Receipt != "" && req.Status != ""
	case ir.ReqStagedHashMatch, ir.ReqPolicyHashMatch, ir.ReqTargetHashMatch:
		return true
	default:
		return false
	}
}

func opsOverlap(a, b map[ir.Operation]bool) bool {
	for op := range a {
		if b[op] {
			return true
		}
	}
	return false
}

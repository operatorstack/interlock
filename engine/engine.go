// Package engine is the pure Interlock decision engine. Decide is a total
// function of (policy, request) with no I/O, no clock, no randomness, and no
// dependency on broker/pitot/deltawire — enforced by check-runtime-boundary.sh.
// The same inputs always yield the same decision, which is what makes decisions
// hashable and replayable.
package engine

import (
	"strings"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

// Decide evaluates req against policy and returns a decision. The evaluation is:
//
//  1. Vocabulary validation. An operation or resource kind outside the V1
//     vocabulary is a fault (the request is malformed, not merely denied).
//  2. First-match over the ordered rule table: a rule matches when its actor,
//     operation set, and resource all match the request.
//  3. No match → default deny.
//  4. A matching deny rule → deny.
//  5. A matching allow rule with no requirements → allow; otherwise every
//     requirement must be satisfied by the request's claimed evidence, else
//     require (listing the missing predicates).
//
// The policy hash is stamped on every decision; a caller that cannot compute it
// (malformed policy) still receives a decision with an empty hash.
func Decide(policy ir.Policy, req protocol.EffectRequest) protocol.Decision {
	policyHash, _ := policy.Hash()

	d := protocol.Decision{
		Protocol:   protocol.DecisionProtocol,
		RequestID:  req.RequestID,
		PolicyHash: policyHash,
	}

	// 1. Vocabulary validation.
	if !ir.ValidOperation(req.Operation) {
		d.Outcome = protocol.OutcomeFault
		d.Reason = "unknown operation: " + string(req.Operation)
		return d
	}
	if !ir.ValidResourceKind(req.Resource.Kind) {
		d.Outcome = protocol.OutcomeFault
		d.Reason = "unknown resource kind: " + string(req.Resource.Kind)
		return d
	}

	// Index resources by ID for rule resolution.
	resByID := make(map[string]ir.Resource, len(policy.Resources))
	for _, r := range policy.Resources {
		resByID[r.ID] = r
	}

	// 2. First-match over the ordered rule table.
	for _, rule := range policy.Rules {
		res, ok := resByID[rule.Resource]
		if !ok {
			// A rule referencing an undeclared resource never matches. The
			// compiler rejects these, so this is defense in depth.
			continue
		}
		if !ruleMatches(rule, res, req) {
			continue
		}

		// 4. Deny rule.
		if rule.Effect == ir.EffectDeny {
			d.Outcome = protocol.OutcomeDeny
			d.RuleID = rule.ID
			d.Reason = ruleReason(rule, "denied by rule")
			return d
		}

		// 5. Allow rule: check requirements against claimed evidence.
		missing := unsatisfied(rule, req, policyHash)
		if len(missing) == 0 {
			d.Outcome = protocol.OutcomeAllow
			d.RuleID = rule.ID
			d.Reason = ruleReason(rule, "allowed by rule")
			return d
		}
		d.Outcome = protocol.OutcomeRequire
		d.RuleID = rule.ID
		d.Reason = ruleReason(rule, "allow requires additional evidence")
		d.MissingEvidence = missing
		return d
	}

	// 3. Default deny.
	d.Outcome = protocol.OutcomeDeny
	d.Reason = "no matching rule (default deny)"
	return d
}

// ruleMatches reports whether rule (whose resource is res) matches req.
func ruleMatches(rule ir.Rule, res ir.Resource, req protocol.EffectRequest) bool {
	if rule.Actor != req.Actor {
		return false
	}
	if !containsOp(rule.Operations, req.Operation) {
		return false
	}
	if res.Kind != req.Resource.Kind {
		return false
	}
	return resourceMatches(res, req.Resource.URI)
}

// resourceMatches reports whether a request URI falls under a declared resource.
//
//   - file / process: exact URI equality.
//   - tree: prefix match. A trailing "**" (optionally after a separator) or the
//     scheme-only form "repo://" matches any URI sharing the scope; otherwise
//     the declared URI is treated as a directory prefix.
func resourceMatches(res ir.Resource, uri string) bool {
	switch res.Kind {
	case ir.KindTree:
		scope := res.URI
		switch {
		case strings.HasSuffix(scope, "**"):
			scope = strings.TrimSuffix(scope, "**")
			return strings.HasPrefix(uri, scope)
		case strings.HasSuffix(scope, "://"):
			// Scheme-only scope (e.g. "repo://") matches any URI in the scheme.
			return strings.HasPrefix(uri, scope)
		default:
			if uri == scope {
				return true
			}
			prefix := scope
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			return strings.HasPrefix(uri, prefix)
		}
	default: // file, process
		return uri == res.URI
	}
}

// unsatisfied returns the rule requirements not met by the request's evidence.
// policyHash is the live policy's hash, used to check policy_hash_match against
// the request's claim.
func unsatisfied(rule ir.Rule, req protocol.EffectRequest, policyHash string) []ir.Requirement {
	var missing []ir.Requirement
	for _, want := range rule.Requires {
		if !satisfied(want, req, policyHash) {
			missing = append(missing, want)
		}
	}
	return missing
}

// satisfied reports whether the request meets requirement want. For
// policy_hash_match the engine checks the request's claimed hash against the
// live policy hash it computed. For every other kind it compares claimed
// evidence fields — the engine trusts the claims; the broker guarantees they are
// truthful.
func satisfied(want ir.Requirement, req protocol.EffectRequest, policyHash string) bool {
	if want.Kind == ir.ReqPolicyHashMatch {
		return req.ClaimedPolicyHash != "" && req.ClaimedPolicyHash == policyHash
	}
	for _, ev := range req.Evidence {
		if ev.Kind != want.Kind {
			continue
		}
		if want.Receipt != "" && ev.Receipt != want.Receipt {
			continue
		}
		if want.Status != "" && ev.Status != want.Status {
			continue
		}
		return true
	}
	return false
}

func containsOp(ops []ir.Operation, op ir.Operation) bool {
	for _, o := range ops {
		if o == op {
			return true
		}
	}
	return false
}

func ruleReason(rule ir.Rule, fallback string) string {
	if rule.Reason != "" {
		return rule.Reason
	}
	return fallback
}

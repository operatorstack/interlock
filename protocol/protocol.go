// Package protocol defines the wire types exchanged with the Interlock decision
// engine: an EffectRequest describing an intended effect, and the Decision the
// engine returns. Both are plain JSON value types with no behavior and no I/O.
//
// Trust boundary: the engine TRUSTS the evidence claims carried on a request —
// it compares claimed evidence against a policy's requirements but does not
// verify that the evidence is real. Producing truthful, correlated evidence is
// the broker's job (see package broker). This keeps the engine pure and
// replayable.
package protocol

import "github.com/operatorstack/interlock/ir"

// Schema tags stamped on the wire types.
const (
	EffectRequestProtocol = "interlock.effect.v1"
	DecisionProtocol      = "interlock.decision.v1"
)

// Fidelity describes how faithfully an observation reflects the real effect.
// It is advisory metadata for auditing; the engine does not branch on it, but
// recording it keeps the enforcement model honest (an opaque process.execute is
// not the same evidence as a host-visible file write).
type Fidelity string

const (
	// FidelityObserved: a truthful native action was seen before execution.
	FidelityObserved Fidelity = "observed"
	// FidelityOpaque: only a coarse signal (e.g. a bare command) was seen.
	FidelityOpaque Fidelity = "opaque"
	// FidelityBrokered: the effect is performed by the broker itself.
	FidelityBrokered Fidelity = "brokered"
)

// Observation records where an effect signal came from and how faithful it is.
type Observation struct {
	Source   string   `json:"source"`
	Fidelity Fidelity `json:"fidelity"`
}

// TargetResource identifies what an effect acts on.
type TargetResource struct {
	Kind ir.ResourceKind `json:"kind"`
	URI  string          `json:"uri"`
}

// Evidence is a single claim attached to a request, checked against a rule's
// requirements. The engine compares fields; it does not authenticate them.
type Evidence struct {
	Kind    ir.RequirementKind `json:"kind"`
	Receipt string             `json:"receipt,omitempty"`
	Status  string             `json:"status,omitempty"`
	Value   string             `json:"value,omitempty"` // e.g. a claimed hash
}

// EffectRequest is the input to engine.Decide.
type EffectRequest struct {
	Protocol          string         `json:"protocol"`
	RequestID         string         `json:"request_id"`
	RunID             string         `json:"run_id"`
	Actor             string         `json:"actor"`
	Operation         ir.Operation   `json:"operation"`
	Resource          TargetResource `json:"resource"`
	Observation       Observation    `json:"observation"`
	ClaimedPolicyHash string         `json:"claimed_policy_hash,omitempty"`
	Evidence          []Evidence     `json:"evidence,omitempty"`
}

// Outcome is the disposition the engine returns for a request.
type Outcome string

const (
	// OutcomeAllow: a matching allow rule fired and its requirements are met.
	OutcomeAllow Outcome = "allow"
	// OutcomeDeny: a matching deny rule fired, or no rule matched (default deny).
	OutcomeDeny Outcome = "deny"
	// OutcomeRequire: a matching allow rule fired but evidence is missing.
	OutcomeRequire Outcome = "require"
	// OutcomeFault: the request was malformed against the vocabulary.
	OutcomeFault Outcome = "fault"
)

// Decision is the output of engine.Decide.
type Decision struct {
	Protocol        string           `json:"protocol"`
	RequestID       string           `json:"request_id"`
	PolicyHash      string           `json:"policy_hash"`
	Outcome         Outcome          `json:"outcome"`
	RuleID          string           `json:"rule_id,omitempty"`
	Reason          string           `json:"reason"`
	MissingEvidence []ir.Requirement `json:"missing_evidence,omitempty"`
}

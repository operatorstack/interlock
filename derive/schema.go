// Package derive is Interlock's policy-authoring frontend. It reads the intent a
// repository already encodes — agent instructions, skills, CODEOWNERS, CI
// workflows, package scripts, generated-file markers — and produces a *candidate*
// Interlock policy for a human to review, never an active one.
//
// Control law (control-law: derivation-proposes-never-enforces): a derived
// artifact may only be a candidate. Every emitted rule is grounded in cited
// repository evidence, expressed strictly within the closed V1 vocabulary
// (ir.Operations / ir.ResourceKinds / ir.RequirementKinds), and neither broader
// nor narrower than its source. Derivation never activates policy, never
// manufactures authority from advisory language, and never weakens an existing
// policy. Promotion to the active decision table happens only through a separate,
// explicit human step that re-runs the real compiler (compiler.Compile) and
// engine (engine.Decide).
//
// This package is strictly upstream of the deterministic authorities. It depends
// on ir/spec/scaffold for vocabulary and emission, but engine/compiler/broker
// never depend on it: a bug here can at worst produce a bad *draft*, which the
// human review + compile gate rejects.
package derive

import "github.com/operatorstack/interlock/ir"

// DerivationSchema tags the derivation.json document.
const DerivationSchema = "interlock.derivation.v1"

// DefaultPolicyID is the policy_id stamped on a derived candidate. It is
// deliberately generic: derivation proposes structure, the human names it.
const DefaultPolicyID = "derived-policy.v1"

// Class is the classification of an extracted statement. Only the first three
// classes may become candidate policy rules; the rest are recorded (or dropped)
// but never emitted as authority.
type Class string

const (
	// ClassEnforceableEffect is an explicit prohibition or permission on an
	// effect (e.g. "never force-push main") — maps to an allow/deny rule.
	ClassEnforceableEffect Class = "enforceable_effect"
	// ClassVerificationRequirement demands evidence before an effect (e.g. "run
	// tests before pushing") — maps to an allow rule with a receipt requirement.
	ClassVerificationRequirement Class = "verification_requirement"
	// ClassHumanDecision demands human sign-off (e.g. "ask before publishing") —
	// maps to an allow rule with a human_approval requirement.
	ClassHumanDecision Class = "human_decision"
	// ClassAdvisoryGuidance is a preference ("prefer functional components") —
	// never an Interlock rule.
	ClassAdvisoryGuidance Class = "advisory_guidance"
	// ClassDomainKnowledge is a fact ("the API uses OAuth") — never a rule.
	ClassDomainKnowledge Class = "domain_knowledge"
	// ClassUnresolved is effect-related but not yet groundable — becomes a
	// question, never a rule until resolved.
	ClassUnresolved Class = "unresolved"
)

// Emittable reports whether a class may become a candidate policy rule.
func (c Class) Emittable() bool {
	switch c {
	case ClassEnforceableEffect, ClassVerificationRequirement, ClassHumanDecision:
		return true
	default:
		return false
	}
}

// Status is the lifecycle state of a derivation record.
type Status string

const (
	// StatusProposed: fully grounded and emitted into the candidate.
	StatusProposed Status = "proposed"
	// StatusUnresolved: effect-related but missing a material decision; carries a
	// Question and is NOT emitted.
	StatusUnresolved Status = "unresolved"
	// StatusRejected: cannot be admitted (conflict, or would weaken an existing
	// policy). Carries a RejectReason and is NOT emitted.
	StatusRejected Status = "rejected"
)

// Strength grades how much authority a source carries. Machine-readable config
// is strong; aspirational prose is weak. Weak evidence may create a suggestion
// (a question) but never, on its own, an activated rule.
type Strength string

const (
	StrengthStrong Strength = "strong"
	StrengthWeak   Strength = "weak"
)

// Source is fail-closed provenance that binds a record to the exact source bytes
// it was derived from. It mirrors broker/envelope.go's conventions: the SHA256 is
// always computed internally via ir.HashBytes (tagged "sha256:"+hex, never a
// caller-supplied value), it carries no timestamps (replay-safe), and it is
// decoded with DisallowUnknownFields (see report.go). Like the broker envelope,
// this is HASH-BINDING, not authenticity: it proves the record refers to these
// exact excerpt bytes at this path, not that a trusted author wrote them.
type Source struct {
	Path      string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	SHA256    string `json:"sha256"`
}

// RawStatement is a single unit of intent an adapter extracts from a source. The
// adapter fills Text, the line span, and Strength; evidence.go computes the
// provenance hash; classify/ground turn it into a Record.
//
// Suggest and Note are the adapter's optional structured hints. A prose adapter
// leaves them empty and lets classify.go read the language. A machine-config
// adapter (CODEOWNERS, workflows) that knows the *shape* of what it found but not
// the operator's intent sets Suggest=ClassUnresolved and a Note explaining the
// decision the human must make — this is how "ownership" or "a CI check exists"
// becomes a QUESTIONS.md entry rather than a silently widened rule (invariant 2).
type RawStatement struct {
	Path      string
	LineStart int
	LineEnd   int
	Text      string
	Strength  Strength
	Suggest   Class
	Note      string
}

// Record is one reviewed unit written to derivation.json: the cited source, the
// classification, and — when emittable and grounded — the proposed rule fields.
type Record struct {
	ID       string   `json:"id"`
	Source   Source   `json:"source"`
	Excerpt  string   `json:"excerpt"`
	Class    Class    `json:"class"`
	Strength Strength `json:"strength"`
	Status   Status   `json:"status"`

	// Proposed rule (populated only for grounded, emittable records).
	Actor        string          `json:"actor,omitempty"`
	Operations   []ir.Operation  `json:"operations,omitempty"`
	Effect       ir.Effect       `json:"effect,omitempty"`
	ResourceKind ir.ResourceKind `json:"resource_kind,omitempty"`
	ResourceURI  string          `json:"resource_uri,omitempty"`
	Requirement  *ir.Requirement `json:"requirement,omitempty"`
	Reason       string          `json:"reason,omitempty"`

	// Resolution.
	Missing      []string `json:"missing,omitempty"`
	Question     string   `json:"question,omitempty"`
	RejectReason string   `json:"reject_reason,omitempty"`
}

// Derivation is the typed derivation.json document.
type Derivation struct {
	Schema   string   `json:"schema"`
	PolicyID string   `json:"policy_id"`
	Records  []Record `json:"records"`
}

// Classifier is the seam for an optional semantic (LLM-assisted) classifier. V1
// ships only the deterministic implementation (see classify.go) and never makes a
// network call. Any future model implementation returns a proposal that ground.go
// still validates against the closed vocabulary — no model output becomes a rule
// without passing the same deterministic grounding + compiler gate.
type Classifier interface {
	Classify(text string) Class
}

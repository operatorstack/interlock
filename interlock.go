package interlock

import (
	"github.com/operatorstack/interlock/compiler"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/spec"
)

// Re-exported vocabulary so a policy author writes only against this package.
type (
	// Operation is a member of the fixed V1 operation vocabulary.
	Operation = ir.Operation
	// ResourceKind is a member of the fixed V1 resource-kind vocabulary.
	ResourceKind = ir.ResourceKind
	// Requirement is an evidence predicate on an allow rule.
	Requirement = ir.Requirement
)

// Operation vocabulary.
const (
	Read       = ir.OpRead
	Write      = ir.OpWrite
	Delete     = ir.OpDelete
	RenameFrom = ir.OpRenameFrom
	RenameTo   = ir.OpRenameTo
	Execute    = ir.OpExecute
	Publish    = ir.OpPublish
	Push       = ir.OpPush
	ForcePush  = ir.OpForcePush
)

// Resource-kind vocabulary.
const (
	FileKind    = ir.KindFile
	TreeKind    = ir.KindTree
	ProcessKind = ir.KindProcess
	BranchKind  = ir.KindBranch
)

// Builder accumulates a policy via fluent, typed constructors. Construction-time
// Go is unrestricted; the resulting Spec is what compiles to canonical IR.
type Builder struct {
	s spec.Spec
}

// Policy starts a new policy builder with the given stable policy id.
func Policy(policyID string) *Builder {
	return &Builder{s: spec.Spec{PolicyID: policyID}}
}

// Actor declares a principal that rules may bind to. Declaring actors up front
// lets the compiler reject rules that reference an unknown actor.
func (b *Builder) Actor(id string) *Builder {
	b.s.Actors = append(b.s.Actors, spec.Actor{ID: id})
	return b
}

// File declares a resource matched by exact URI.
func (b *Builder) File(id, uri string) *Builder {
	b.s.Resources = append(b.s.Resources, spec.Resource{ID: id, Kind: ir.KindFile, URI: uri})
	return b
}

// Tree declares a resource matched by prefix/glob (see ir.resourceMatches).
func (b *Builder) Tree(id, uri string) *Builder {
	b.s.Resources = append(b.s.Resources, spec.Resource{ID: id, Kind: ir.KindTree, URI: uri})
	return b
}

// Process declares an executable-class resource matched by exact URI.
func (b *Builder) Process(id, uri string) *Builder {
	b.s.Resources = append(b.s.Resources, spec.Resource{ID: id, Kind: ir.KindProcess, URI: uri})
	return b
}

// Branch declares a branch-ref resource matched by exact URI (e.g.
// "repo://branch/main"), the target of vcs.push / vcs.force_push operations.
func (b *Builder) Branch(id, uri string) *Builder {
	b.s.Resources = append(b.s.Resources, spec.Resource{ID: id, Kind: ir.KindBranch, URI: uri})
	return b
}

// RuleBuilder configures one rule before it is appended to the policy.
type RuleBuilder struct {
	parent *Builder
	r      spec.Rule
}

// Allow begins an allow rule with the given id.
func (b *Builder) Allow(id string) *RuleBuilder {
	return &RuleBuilder{parent: b, r: spec.Rule{ID: id, Effect: ir.EffectAllow}}
}

// Deny begins a deny rule with the given id.
func (b *Builder) Deny(id string) *RuleBuilder {
	return &RuleBuilder{parent: b, r: spec.Rule{ID: id, Effect: ir.EffectDeny}}
}

// By binds the rule to an actor.
func (r *RuleBuilder) By(actor string) *RuleBuilder {
	r.r.Actor = actor
	return r
}

// To adds one or more operations to the rule.
func (r *RuleBuilder) To(ops ...Operation) *RuleBuilder {
	r.r.Operations = append(r.r.Operations, ops...)
	return r
}

// On binds the rule to a declared resource id.
func (r *RuleBuilder) On(resourceID string) *RuleBuilder {
	r.r.Resource = resourceID
	return r
}

// Requiring attaches evidence requirements (only meaningful on allow rules).
func (r *RuleBuilder) Requiring(reqs ...Requirement) *RuleBuilder {
	r.r.Requires = append(r.r.Requires, reqs...)
	return r
}

// Because sets a human-readable reason surfaced in decisions.
func (r *RuleBuilder) Because(reason string) *RuleBuilder {
	r.r.Reason = reason
	return r
}

// Add finalizes the rule and returns the parent builder for chaining.
func (r *RuleBuilder) Add() *Builder {
	r.parent.s.Rules = append(r.parent.s.Rules, r.r)
	return r.parent
}

// Requirement constructors.

// ReceiptStatus requires a receipt of the given schema reporting the status.
func ReceiptStatus(schema, status string) Requirement {
	return ir.Requirement{Kind: ir.ReqReceiptStatus, Receipt: schema, Status: status}
}

// StagedHashMatch requires the staged candidate hash to match the request claim.
func StagedHashMatch() Requirement {
	return ir.Requirement{Kind: ir.ReqStagedHashMatch}
}

// PolicyHashMatch requires the request's claimed policy hash to equal the live
// policy hash (checked in the engine against the compiled policy).
func PolicyHashMatch() Requirement {
	return ir.Requirement{Kind: ir.ReqPolicyHashMatch}
}

// TargetHashMatch requires the target's prior hash to match the request claim.
func TargetHashMatch() Requirement {
	return ir.Requirement{Kind: ir.ReqTargetHashMatch}
}

// HumanApproval requires the request to carry a human-approval claim naming the
// given approval id (e.g. "release-main"). Absent the claim, a matching allow
// rule yields the require outcome — fail-closed at the enforcement point.
func HumanApproval(id string) Requirement {
	return ir.Requirement{Kind: ir.ReqHumanApproval, Approval: id}
}

// Spec returns the underlying spec.Spec, e.g. for source-level tests.
func (b *Builder) Spec() spec.Spec {
	return b.s
}

// Compile lowers the builder's spec to canonical IR, returning structural
// diagnostics for an invalid policy.
func (b *Builder) Compile() (ir.Policy, error) {
	return compiler.Compile(b.s)
}

// Emit compiles and returns the canonical policy bytes, the intended output of a
// policy module's deterministic entrypoint (`interlock compile` runs it).
func (b *Builder) Emit() ([]byte, error) {
	p, err := b.Compile()
	if err != nil {
		return nil, err
	}
	return p.CanonicalBytes()
}

// EmitSpec compiles-to-validate, then returns the policy as serialized
// interlock.spec.v1 (the neutral authoring format), not canonical IR. It runs the
// compiler first so a structurally invalid policy fails here rather than emitting
// a spec.v1 document that only breaks downstream. This is the Go frontend's path
// to the same authoring target the JSON/TS/Python frontends emit.
func (b *Builder) EmitSpec() ([]byte, error) {
	if _, err := b.Compile(); err != nil {
		return nil, err
	}
	return spec.Encode(spec.FromSpec(b.s))
}

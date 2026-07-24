package spec

// specdoc.go defines interlock.spec.v1: the serializable, versioned authoring
// format every language frontend emits. It is the neutral target that sits one
// level above the canonical IR (interlock.policy.v1). A SpecDoc is *authoring*
// data — human- or machine-written, not canonicalized; determinism and identity
// belong to the IR the compiler lowers it to. Decoding a SpecDoc yields the
// in-memory spec.Spec the compiler already consumes unchanged, so spec.v1 adds a
// (de)serialization layer around the existing authority, not a second authority.

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/operatorstack/interlock/ir"
)

// Protocol is the schema tag stamped on every spec.v1 document. It is distinct
// from ir.Protocol ("interlock.policy.v1"): spec.v1 is the authoring input,
// policy.v1 is the compiled, canonical output.
const Protocol = "interlock.spec.v1"

// SpecDoc is the serializable form of a policy under construction. Its JSON field
// names deliberately match the IR vocabulary already frozen in the corpus (e.g.
// "filesystem.write", "receipt_status") so a spec.v1 document reads naturally
// next to the policy.v1 it compiles to. Actors are a plain string array — the
// no-code authoring ergonomics win over the nested {"id": ...} shape, and the
// bridge to spec.Spec restores the typed form the compiler expects.
type SpecDoc struct {
	Protocol  string        `json:"protocol"`
	PolicyID  string        `json:"policy_id"`
	Actors    []string      `json:"actors"`
	Resources []ResourceDoc `json:"resources"`
	Rules     []RuleDoc     `json:"rules"`
}

// ResourceDoc is the serializable form of a declared capability target.
type ResourceDoc struct {
	ID   string          `json:"id"`
	Kind ir.ResourceKind `json:"kind"`
	URI  string          `json:"uri"`
}

// RuleDoc is the serializable form of one decision-table entry. Requirements
// reuse ir.Requirement, whose JSON tags (kind/receipt/status/approval) are
// already the frozen vocabulary.
type RuleDoc struct {
	ID         string           `json:"id"`
	Effect     ir.Effect        `json:"effect"`
	Actor      string           `json:"actor"`
	Operations []ir.Operation   `json:"operations"`
	Resource   string           `json:"resource"`
	Requires   []ir.Requirement `json:"requires,omitempty"`
	Reason     string           `json:"reason,omitempty"`
}

// FromSpec projects a typed spec.Spec into its serializable spec.v1 form,
// stamping the protocol tag. It is the inverse of (SpecDoc).ToSpec.
func FromSpec(s Spec) SpecDoc {
	doc := SpecDoc{
		Protocol: Protocol,
		PolicyID: s.PolicyID,
	}
	for _, a := range s.Actors {
		doc.Actors = append(doc.Actors, a.ID)
	}
	for _, r := range s.Resources {
		doc.Resources = append(doc.Resources, ResourceDoc{ID: r.ID, Kind: r.Kind, URI: r.URI})
	}
	for _, r := range s.Rules {
		doc.Rules = append(doc.Rules, RuleDoc{
			ID:         r.ID,
			Effect:     r.Effect,
			Actor:      r.Actor,
			Operations: append([]ir.Operation(nil), r.Operations...),
			Resource:   r.Resource,
			Requires:   append([]ir.Requirement(nil), r.Requires...),
			Reason:     r.Reason,
		})
	}
	return doc
}

// FromPolicy projects a compiled canonical IR policy back into spec.v1 authoring
// form. Because the compiler's lowering is idempotent (it re-sorts actors,
// resources, and each rule's operations, and preserves rule order), recompiling
// the result reproduces the exact same canonical bytes and hash. This makes a
// frozen policy a drift-proof source for its own spec.v1 parity input: the corpus
// spec.v1 documents are derived from the frozen IR, so they can never disagree
// with it.
func FromPolicy(p ir.Policy) SpecDoc {
	doc := SpecDoc{
		Protocol: Protocol,
		PolicyID: p.PolicyID,
		Actors:   append([]string(nil), p.Actors...),
	}
	for _, r := range p.Resources {
		doc.Resources = append(doc.Resources, ResourceDoc{ID: r.ID, Kind: r.Kind, URI: r.URI})
	}
	for _, r := range p.Rules {
		doc.Rules = append(doc.Rules, RuleDoc{
			ID:         r.ID,
			Effect:     r.Effect,
			Actor:      r.Actor,
			Operations: append([]ir.Operation(nil), r.Operations...),
			Resource:   r.Resource,
			Requires:   append([]ir.Requirement(nil), r.Requires...),
			Reason:     r.Reason,
		})
	}
	return doc
}

// ToSpec bridges a decoded spec.v1 document back to the in-memory spec.Spec the
// compiler consumes. It performs no validation beyond the shape already enforced
// by Decode — structural validity (unknown actors, bad effects, unreachable
// rules) is the compiler's job, so spec.v1 authoring and Go authoring hit the
// exact same authority.
func (d SpecDoc) ToSpec() Spec {
	s := Spec{PolicyID: d.PolicyID}
	for _, a := range d.Actors {
		s.Actors = append(s.Actors, Actor{ID: a})
	}
	for _, r := range d.Resources {
		s.Resources = append(s.Resources, Resource{ID: r.ID, Kind: r.Kind, URI: r.URI})
	}
	for _, r := range d.Rules {
		s.Rules = append(s.Rules, Rule{
			ID:         r.ID,
			Effect:     r.Effect,
			Actor:      r.Actor,
			Operations: append([]ir.Operation(nil), r.Operations...),
			Resource:   r.Resource,
			Requires:   append([]ir.Requirement(nil), r.Requires...),
			Reason:     r.Reason,
		})
	}
	return s
}

// Encode renders a SpecDoc as indented JSON with a trailing newline. This is the
// authoring form — readable and diff-friendly — NOT the canonical IR encoding.
// Canonicalization (sorted keys, no whitespace) applies only to the compiled
// policy.v1 the compiler produces, never to the spec.v1 input.
func Encode(doc SpecDoc) ([]byte, error) {
	if doc.Protocol == "" {
		doc.Protocol = Protocol
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("interlock/spec: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode parses spec.v1 JSON into a SpecDoc, rejecting a missing or wrong
// protocol tag and any unknown field. Unknown-field rejection catches authoring
// typos (e.g. "requirements" for "requires") at decode time rather than letting
// them silently vanish — a no-code author gets a precise error, not a policy
// that quietly means something else.
func Decode(data []byte) (SpecDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc SpecDoc
	if err := dec.Decode(&doc); err != nil {
		return SpecDoc{}, fmt.Errorf("interlock/spec: decode: %w", err)
	}
	if doc.Protocol != Protocol {
		return SpecDoc{}, fmt.Errorf("interlock/spec: unexpected protocol %q, want %q", doc.Protocol, Protocol)
	}
	return doc, nil
}

// DecodeToSpec is the common path: parse spec.v1 JSON and bridge straight to the
// in-memory spec.Spec ready for compiler.Compile.
func DecodeToSpec(data []byte) (Spec, error) {
	doc, err := Decode(data)
	if err != nil {
		return Spec{}, err
	}
	return doc.ToSpec(), nil
}

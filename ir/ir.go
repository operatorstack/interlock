// Package ir defines the canonical Effect Policy IR: the execution language of
// Interlock. A Policy carries stable bytes (Canonical) and a stable identity
// (Hash). Two policies that mean the same thing MUST canonicalize to identical
// bytes and hash identically, regardless of how the authoring Go constructed
// them. This package has no internal dependencies and performs no I/O.
package ir

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Protocol is the schema tag stamped on every canonical policy.
const Protocol = "interlock.policy.v1"

// Operation is a member of the fixed V1 operation vocabulary. The set is small
// by design; it can be extended later without an IR redesign.
type Operation string

const (
	OpRead       Operation = "filesystem.read"
	OpWrite      Operation = "filesystem.write"
	OpDelete     Operation = "filesystem.delete"
	OpRenameFrom Operation = "filesystem.rename_from"
	OpRenameTo   Operation = "filesystem.rename_to"
	OpExecute    Operation = "process.execute"
	OpPublish    Operation = "artifact.publish"
	OpPush       Operation = "vcs.push"
	OpForcePush  Operation = "vcs.force_push"
)

// Operations is the closed set of V1 operations, in canonical order.
var Operations = []Operation{
	OpRead, OpWrite, OpDelete, OpRenameFrom, OpRenameTo, OpExecute, OpPublish,
	OpPush, OpForcePush,
}

// ValidOperation reports whether op is a member of the V1 vocabulary.
func ValidOperation(op Operation) bool {
	for _, known := range Operations {
		if op == known {
			return true
		}
	}
	return false
}

// ResourceKind is a member of the fixed V1 resource-kind vocabulary.
type ResourceKind string

const (
	KindFile    ResourceKind = "file"
	KindTree    ResourceKind = "tree"
	KindProcess ResourceKind = "process"
	KindBranch  ResourceKind = "branch"
)

// ResourceKinds is the closed set of V1 resource kinds, in canonical order.
var ResourceKinds = []ResourceKind{KindFile, KindTree, KindProcess, KindBranch}

// ValidResourceKind reports whether k is a member of the V1 vocabulary.
func ValidResourceKind(k ResourceKind) bool {
	switch k {
	case KindFile, KindTree, KindProcess, KindBranch:
		return true
	default:
		return false
	}
}

// Effect is the disposition a rule declares for the requests it matches. Only
// allow and deny are authored on rules; require and fault are decision outcomes
// the engine derives (see package engine).
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// ValidEffect reports whether e is an authorable rule effect.
func ValidEffect(e Effect) bool {
	return e == EffectAllow || e == EffectDeny
}

// RequirementKind names a class of evidence a rule can demand before an allow
// takes effect. The engine treats requirements opaquely: it compares the
// request's claimed evidence against the rule's requirements. The broker is
// responsible for producing truthful evidence.
type RequirementKind string

const (
	// ReqReceiptStatus demands a receipt of a given schema reporting a status.
	ReqReceiptStatus RequirementKind = "receipt_status"
	// ReqStagedHashMatch demands the staged candidate hash equal a claim.
	ReqStagedHashMatch RequirementKind = "staged_hash_match"
	// ReqPolicyHashMatch demands the request's policy hash equal the live one.
	ReqPolicyHashMatch RequirementKind = "policy_hash_match"
	// ReqTargetHashMatch demands the target's prior hash equal a claim.
	ReqTargetHashMatch RequirementKind = "target_hash_match"
	// ReqHumanApproval demands the request carry a matching human-approval claim
	// (identified by Requirement.Approval). Like every requirement, the engine
	// trusts the claim; an out-of-band approver or the broker makes it truthful.
	ReqHumanApproval RequirementKind = "human_approval"
)

// Resource is a declared, addressable capability target.
//
//   - kind=file: URI is an exact match target.
//   - kind=tree: URI is a prefix/glob; a trailing "**" or the scheme-only form
//     "repo://" matches any URI under that scope.
//   - kind=process: URI names an executable class.
//   - kind=branch: URI names a specific branch ref (e.g. "repo://branch/main"),
//     matched by exact equality — branch glob patterns are not a V1 feature.
type Resource struct {
	ID   string       `json:"id"`
	Kind ResourceKind `json:"kind"`
	URI  string       `json:"uri"`
}

// Requirement is a single evidence predicate attached to an allow rule.
type Requirement struct {
	Kind     RequirementKind `json:"kind"`
	Receipt  string          `json:"receipt,omitempty"`  // required receipt schema, for receipt_status
	Status   string          `json:"status,omitempty"`   // required status value, for receipt_status
	Approval string          `json:"approval,omitempty"` // required approval id, for human_approval
}

// Rule is one entry in the ordered decision table.
type Rule struct {
	ID         string        `json:"id"`
	Effect     Effect        `json:"effect"`
	Actor      string        `json:"actor"`
	Operations []Operation   `json:"operations"`
	Resource   string        `json:"resource"` // Resource.ID reference
	Requires   []Requirement `json:"requires,omitempty"`
	Reason     string        `json:"reason,omitempty"`
}

// Policy is the whole canonical decision table.
type Policy struct {
	Protocol  string     `json:"protocol"`
	PolicyID  string     `json:"policy_id"`
	Actors    []string   `json:"actors"`
	Resources []Resource `json:"resources"`
	Rules     []Rule     `json:"rules"`
}

// Canonical renders v to canonical JSON bytes: object keys sorted recursively,
// numbers preserved via json.Number, no insignificant whitespace, trailing
// newline. Equivalent values produce byte-identical output.
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("interlock/ir: marshal: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("interlock/ir: decode: %w", err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, tree); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// writeCanonical emits v with recursively sorted object keys and no extra space.
func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		// Scalars (string, bool, json.Number, nil) marshal deterministically.
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
}

// Hash returns the canonical identity of the policy: "sha256:" + hex of the
// SHA-256 of its canonical bytes.
func (p Policy) Hash() (string, error) {
	b, err := Canonical(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// CanonicalBytes returns the policy's canonical JSON encoding.
func (p Policy) CanonicalBytes() ([]byte, error) {
	return Canonical(p)
}

// HashBytes returns "sha256:"+hex of arbitrary content, using the same tagging
// convention as Policy.Hash so callers can compare artifact and policy hashes.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// LoadPolicy decodes canonical policy bytes and verifies the protocol tag. It
// does not re-canonicalize: policy identity is still established by Policy.Hash
// at decide time, so round-tripping already-canonical bytes changes no canonical
// output. This is the exported loader every tenant needs; without it each one
// hand-rolls the same json.Unmarshal + protocol check.
func LoadPolicy(b []byte) (Policy, error) {
	var p Policy
	if err := json.Unmarshal(b, &p); err != nil {
		return Policy{}, fmt.Errorf("interlock/ir: decode policy: %w", err)
	}
	if p.Protocol != Protocol {
		return Policy{}, fmt.Errorf("interlock/ir: policy protocol %q != %q", p.Protocol, Protocol)
	}
	return p, nil
}

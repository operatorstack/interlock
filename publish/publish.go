// Package publish is Interlock's high-level publishing façade: the smallest
// interface a tenant needs to publish a verified staged candidate to a protected
// target through the broker. It collapses the glue every tenant would otherwise
// re-implement — writing the upstream evidence envelope, assembling the many-field
// broker.PublishRequest, creating and retaining the receipt chain, and persisting
// the decision evidence — around the ONE authoritative broker.Publish call.
//
// This is a leaf package by design: it does NOT pull in the authoring toolchain
// (compiler/spec) that the root interlock package carries, so a tenant can import
// publishing alone. A tenant that imports interlock/publish need not import
// interlock/broker, interlock/ir, or interlock/receipt directly (the loader,
// resource kinds, and policy type are re-exported below).
//
// Authority boundary — what this façade MUST NOT do. The honest guarantee lives
// in the broker: it hashes the real staged bytes, re-reads each upstream envelope
// from disk taking schema+status FROM THE FILE, verifies target prior-state, and
// fails closed. This façade may construct, resolve, compute expected state,
// serialize the envelope, and retain the receipt chain. It never calls the engine,
// never reads or writes the target path, never invents evidence, and never
// interprets what a tenant's upstream status means — status flows through
// uninterpreted into the envelope the broker re-reads.
package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/operatorstack/interlock/broker"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/receipt"
)

// Re-exports so a tenant imports interlock/publish alone. These are the low-level
// stable symbols a publish caller still legitimately names.
type (
	// Policy is an alias for ir.Policy — the compiled decision table.
	Policy = ir.Policy
	// ResourceKind is an alias for ir.ResourceKind (file, tree, …).
	ResourceKind = ir.ResourceKind
)

// KindFile re-exports ir.KindFile, the common publish resource kind.
const KindFile = ir.KindFile

// LoadPolicy re-exports ir.LoadPolicy: decode canonical policy bytes + verify the
// protocol tag.
func LoadPolicy(b []byte) (Policy, error) { return ir.LoadPolicy(b) }

// HashBytes re-exports ir.HashBytes: the tagged content hash ("sha256:"+hex)
// Interlock uses, so a tenant can verify published bytes without importing
// interlock/ir.
func HashBytes(b []byte) string { return ir.HashBytes(b) }

// Evidence is one durable upstream receipt a tenant attaches to a publish. The
// tenant owns the meaning of Schema and Status; Interlock never interprets
// either. No hash field: the façade binds each envelope to the staged bytes.
type Evidence struct {
	Schema string
	Status string
}

// Request is one logical publication. It carries the same fields the broker
// ultimately needs, but the tenant supplies them once, by name, instead of
// assembling broker.PublishRequest, writing envelopes, and managing a chain.
type Request struct {
	Policy      Policy
	RunID       string
	RequestID   string
	Actor       string
	ResourceURI string
	Kind        ResourceKind
	StagedPath  string
	TargetPath  string

	// ExpectedTargetHash is the prior hash the target must currently have. Empty
	// means the target must NOT exist — mirroring the broker, so there is no
	// implicit overwrite. Use TargetHashOf to adopt the current target state.
	ExpectedTargetHash string

	Upstream []Evidence

	// EvidenceDir is where the upstream envelope(s), broker decision result, and
	// retained receipt chain are persisted as durable run evidence.
	EvidenceDir string
}

// Result wraps the broker's decision result and the retained receipt chain.
type Result struct {
	broker.Result
	Chain *receipt.Chain
}

// upstreamEnvelopeName is the on-disk name of the single upstream evidence
// envelope. Multiple upstream receipts are indexed to keep names stable.
const upstreamEnvelopeName = "interlock-upstream-envelope.json"

// Publish performs one publication through the broker. It reads the staged bytes
// once, writes an upstream evidence envelope per Evidence (status flows through
// uninterpreted), assembles the broker request, creates and retains a receipt
// chain, and calls the one authoritative broker.Publish. On any outcome that
// produced a decision it persists the decision result and chain as run evidence;
// unlike a hand-rolled integration it surfaces — rather than swallows — an
// evidence-persistence failure, since an unaudited effect must not report success.
func Publish(req Request) (Result, error) {
	staged, err := os.ReadFile(req.StagedPath)
	if err != nil {
		return Result{}, fmt.Errorf("interlock/publish: read staged candidate: %w", err)
	}

	upstream := make([]broker.UpstreamReceipt, 0, len(req.Upstream))
	for i, ev := range req.Upstream {
		name := upstreamEnvelopeName
		if i > 0 {
			name = fmt.Sprintf("interlock-upstream-envelope.%d.json", i)
		}
		path := filepath.Join(req.EvidenceDir, name)
		if err := broker.WriteUpstreamEnvelope(path, broker.UpstreamEvidence{
			Schema: ev.Schema,
			RunID:  req.RunID,
			Status: ev.Status,
		}, staged); err != nil {
			return Result{}, err
		}
		upstream = append(upstream, broker.UpstreamReceipt{Path: path})
	}

	chain := receipt.NewChain(req.RunID)
	res, pubErr := broker.Publish(req.Policy, broker.PublishRequest{
		RunID:              req.RunID,
		RequestID:          req.RequestID,
		Actor:              req.Actor,
		ResourceURI:        req.ResourceURI,
		Kind:               req.Kind,
		StagedPath:         req.StagedPath,
		TargetPath:         req.TargetPath,
		ExpectedTargetHash: req.ExpectedTargetHash,
		Upstream:           upstream,
	}, chain)

	// Persist evidence whenever the engine ran (a receipt was appended) — this
	// covers both allow and deny; pre-decision fail-closed faults append nothing
	// and leave no misleading partial evidence, matching prior behavior.
	var persistErr error
	if req.EvidenceDir != "" && len(chain.Receipts) > 0 {
		persistErr = persistEvidence(req.EvidenceDir, res, chain)
	}

	result := Result{Result: res, Chain: chain}
	if pubErr != nil {
		// The broker outcome is primary: return it verbatim so the caller sees the
		// exact fail-closed reason (denied/required/fault/stale/missing-envelope).
		return result, pubErr
	}
	if persistErr != nil {
		return result, fmt.Errorf("interlock/publish: publish succeeded but persisting evidence failed: %w", persistErr)
	}
	return result, nil
}

// persistEvidence writes the broker decision result and the retained receipt
// chain as durable run evidence, atomically.
func persistEvidence(dir string, res broker.Result, chain *receipt.Chain) error {
	resultData, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("interlock/publish: marshal broker result: %w", err)
	}
	if err := broker.WriteFileAtomic(filepath.Join(dir, "interlock-broker-receipt.json"), append(resultData, '\n'), 0o600); err != nil {
		return err
	}
	chainData, err := json.MarshalIndent(chain, "", "  ")
	if err != nil {
		return fmt.Errorf("interlock/publish: marshal receipt chain: %w", err)
	}
	if err := broker.WriteFileAtomic(filepath.Join(dir, "interlock-receipt-chain.json"), append(chainData, '\n'), 0o600); err != nil {
		return err
	}
	return nil
}

// TargetHashOf returns the current target's content hash in Interlock's tagged
// format, or "" if the target does not exist yet — the value to pass as
// Request.ExpectedTargetHash to adopt the current state (or require absence).
func TargetHashOf(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("interlock/publish: read target: %w", err)
	}
	return ir.HashBytes(b), nil
}

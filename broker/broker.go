// Package broker performs Interlock's one protected effect: publishing a staged
// candidate file to a final target path, but only for a request the policy
// engine allows on truthful, broker-produced evidence. This is where the honest
// guarantee lives: the engine decides on claims, but the broker verifies the
// claims against reality (the actual staged bytes, the actual upstream receipts,
// the live policy hash) before it ever touches the target. Any mismatch fails
// closed — the target is never modified.
package broker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
)

// UpstreamReceipt is an evidence receipt handed to the broker (e.g. a DeltaWire
// supervision receipt). The broker correlates it by run and turns it into
// truthful engine evidence; it does not trust the producer to have done so.
type UpstreamReceipt struct {
	Schema string `json:"schema"`
	Status string `json:"status"`
	RunID  string `json:"run_id"`
}

// PublishRequest is a request to publish a staged candidate to a target.
type PublishRequest struct {
	RunID       string          `json:"run_id"`
	RequestID   string          `json:"request_id"`
	Actor       string          `json:"actor"`
	ResourceURI string          `json:"resource_uri"` // must match a declared resource in the policy
	Kind        ir.ResourceKind `json:"kind"`         // file or tree
	StagedPath  string          `json:"staged_path"`  // candidate to publish (broker reads this)
	TargetPath  string          `json:"target_path"`  // final path (broker writes this)

	// ExpectedTargetHash, if set, is the prior hash the target must currently
	// have (target_hash_match). Empty means "target must not exist".
	ExpectedTargetHash string `json:"expected_target_hash,omitempty"`

	Upstream []UpstreamReceipt `json:"upstream,omitempty"`
}

// Result is the outcome of a successful publish.
type Result struct {
	Decision      protocol.Decision `json:"decision"`
	Receipt       receipt.Receipt   `json:"receipt"`
	PublishedTo   string            `json:"published_to,omitempty"`
	PublishedHash string            `json:"published_hash,omitempty"`
	StagedHash    string            `json:"staged_hash,omitempty"`
}

// ErrDenied is returned when the engine does not allow the publish. The target
// is guaranteed untouched.
var ErrDenied = errors.New("interlock/broker: publish denied by policy")

// Publish evaluates req against policy and, only on an allow, atomically
// promotes the staged file to the target. chain records the decision receipt.
//
// The broker builds a truthful EffectRequest: it hashes the real staged bytes,
// stamps the live policy hash, correlates upstream receipts by run, and only
// then calls the engine. It fails closed if the policy hash cannot be computed,
// the staged file is unreadable, the target's prior state is not as expected, or
// the engine returns anything other than allow.
func Publish(policy ir.Policy, req PublishRequest, chain *receipt.Chain) (Result, error) {
	livePolicyHash, err := policy.Hash()
	if err != nil {
		return Result{}, fmt.Errorf("interlock/broker: policy hash: %w", err)
	}

	// Read and hash the real staged bytes — truthful staged_hash evidence.
	staged, err := os.ReadFile(req.StagedPath)
	if err != nil {
		return Result{}, fmt.Errorf("interlock/broker: read staged: %w", err)
	}
	stagedHash := ir.HashBytes(staged)

	// Verify the target's prior state matches the request's expectation. This is
	// checked against reality before any allow so a stale expectation fails safe.
	if err := verifyTargetState(req.TargetPath, req.ExpectedTargetHash); err != nil {
		return Result{}, err
	}

	// Correlate upstream receipts by run and lower them to truthful evidence.
	evidence := []protocol.Evidence{
		{Kind: ir.ReqStagedHashMatch, Value: stagedHash},
		{Kind: ir.ReqTargetHashMatch, Value: req.ExpectedTargetHash},
	}
	for _, u := range req.Upstream {
		if u.RunID != req.RunID {
			return Result{}, fmt.Errorf("interlock/broker: upstream receipt run %q does not match request run %q", u.RunID, req.RunID)
		}
		evidence = append(evidence, protocol.Evidence{
			Kind:    ir.ReqReceiptStatus,
			Receipt: u.Schema,
			Status:  u.Status,
		})
	}

	effReq := protocol.EffectRequest{
		Protocol:          protocol.EffectRequestProtocol,
		RequestID:         req.RequestID,
		RunID:             req.RunID,
		Actor:             req.Actor,
		Operation:         ir.OpPublish,
		Resource:          protocol.TargetResource{Kind: req.Kind, URI: req.ResourceURI},
		Observation:       protocol.Observation{Source: "interlock.broker", Fidelity: protocol.FidelityBrokered},
		ClaimedPolicyHash: livePolicyHash,
		Evidence:          evidence,
	}

	decision := engine.Decide(policy, effReq)

	// Record the decision on the chain regardless of outcome — a denied publish
	// is still an auditable event.
	rcpt, rerr := chain.Append(policy, effReq, decision)
	if rerr != nil {
		return Result{}, fmt.Errorf("interlock/broker: append receipt: %w", rerr)
	}

	if decision.Outcome != protocol.OutcomeAllow {
		return Result{Decision: decision, Receipt: rcpt}, fmt.Errorf("%w: %s (%s)", ErrDenied, decision.Outcome, decision.Reason)
	}

	// Allowed: atomically promote via a same-directory temp file + rename.
	publishedHash, err := atomicPublish(req.TargetPath, staged)
	if err != nil {
		return Result{Decision: decision, Receipt: rcpt}, err
	}
	if publishedHash != stagedHash {
		// Defensive: the bytes we wrote must hash to what we decided on.
		return Result{Decision: decision, Receipt: rcpt}, fmt.Errorf("interlock/broker: post-publish hash mismatch")
	}

	return Result{
		Decision:      decision,
		Receipt:       rcpt,
		PublishedTo:   req.TargetPath,
		PublishedHash: publishedHash,
		StagedHash:    stagedHash,
	}, nil
}

// verifyTargetState confirms the target matches the expected prior hash. Empty
// expected means the target must not exist yet.
func verifyTargetState(targetPath, expectedHash string) error {
	b, err := os.ReadFile(targetPath)
	if errors.Is(err, os.ErrNotExist) {
		if expectedHash != "" {
			return fmt.Errorf("interlock/broker: target missing but expected hash %s", expectedHash)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("interlock/broker: stat target: %w", err)
	}
	if expectedHash == "" {
		return fmt.Errorf("interlock/broker: target already exists but no prior hash expected (refusing to overwrite)")
	}
	if got := ir.HashBytes(b); got != expectedHash {
		return fmt.Errorf("interlock/broker: target hash %s does not match expected %s", got, expectedHash)
	}
	return nil
}

// atomicPublish writes content to a temp file in the target's directory and
// renames it into place (atomic on the same filesystem). Returns the hash of the
// bytes actually written.
func atomicPublish(targetPath string, content []byte) (string, error) {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("interlock/broker: mkdir target dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".interlock-publish-*")
	if err != nil {
		return "", fmt.Errorf("interlock/broker: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return "", fmt.Errorf("interlock/broker: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("interlock/broker: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("interlock/broker: close temp: %w", err)
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return "", fmt.Errorf("interlock/broker: atomic rename: %w", err)
	}

	written, err := os.ReadFile(targetPath)
	if err != nil {
		return "", fmt.Errorf("interlock/broker: reread published: %w", err)
	}
	return ir.HashBytes(written), nil
}

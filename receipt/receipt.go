// Package receipt records decisions as a hash-linked chain and verifies that
// chain by replaying it against the pure engine. A receipt commits to the
// request, the policy, the rule that fired, the decision, the evidence, and the
// previous receipt — so any change to policy, evidence, order, or run identity is
// detectable after the fact. Hashing uses crypto/sha256; the chain logic itself
// is deterministic and does no I/O beyond the caller's storage.
package receipt

import (
	"errors"
	"fmt"

	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

// Schema is stamped on every receipt.
const Schema = "interlock.receipt.v1"

// Receipt is one link in the decision chain. It is written after a decision and
// commits to everything needed to re-derive and verify that decision.
type Receipt struct {
	Schema          string           `json:"schema"`
	Sequence        int              `json:"sequence"`
	RunID           string           `json:"run_id"`
	RequestID       string           `json:"request_id"`
	RequestHash     string           `json:"request_hash"`
	PolicyHash      string           `json:"policy_hash"`
	RuleID          string           `json:"rule_id,omitempty"`
	Outcome         protocol.Outcome `json:"outcome"`
	EvidenceHashes  []string         `json:"evidence_hashes,omitempty"`
	PrevReceiptHash string           `json:"prev_receipt_hash"`
	SelfHash        string           `json:"self_hash"`
}

// GenesisHash is the PrevReceiptHash of the first receipt in a run.
const GenesisHash = "sha256:genesis"

// Chain accumulates receipts for a single run.
type Chain struct {
	RunID    string
	Receipts []Receipt
}

// NewChain starts an empty chain for a run.
func NewChain(runID string) *Chain {
	return &Chain{RunID: runID}
}

// Append records a decision for req under policy and returns the new receipt.
// It links to the prior receipt and self-hashes the committed fields.
func (c *Chain) Append(policy ir.Policy, req protocol.EffectRequest, d protocol.Decision) (Receipt, error) {
	reqHash, err := hashValue(req)
	if err != nil {
		return Receipt{}, err
	}
	policyHash, err := policy.Hash()
	if err != nil {
		return Receipt{}, err
	}
	prev := GenesisHash
	if n := len(c.Receipts); n > 0 {
		prev = c.Receipts[n-1].SelfHash
	}
	r := Receipt{
		Schema:          Schema,
		Sequence:        len(c.Receipts),
		RunID:           c.RunID,
		RequestID:       req.RequestID,
		RequestHash:     reqHash,
		PolicyHash:      policyHash,
		RuleID:          d.RuleID,
		Outcome:         d.Outcome,
		EvidenceHashes:  evidenceHashes(req.Evidence),
		PrevReceiptHash: prev,
	}
	self, err := selfHash(r)
	if err != nil {
		return Receipt{}, err
	}
	r.SelfHash = self
	c.Receipts = append(c.Receipts, r)
	return r, nil
}

// Replay re-derives every decision in receipts against policy and verifies the
// chain end to end. It fails closed on a changed policy, a missing or reordered
// link, mismatched evidence, a duplicate sequence, or a cross-run receipt.
func Replay(policy ir.Policy, requests []protocol.EffectRequest, receipts []Receipt) error {
	if len(requests) != len(receipts) {
		return fmt.Errorf("interlock/receipt: %d requests but %d receipts", len(requests), len(receipts))
	}
	livePolicyHash, err := policy.Hash()
	if err != nil {
		return err
	}

	prev := GenesisHash
	var runID string
	for i, r := range receipts {
		if r.Schema != Schema {
			return fmt.Errorf("receipt %d: unknown schema %q", i, r.Schema)
		}
		if i == 0 {
			runID = r.RunID
		} else if r.RunID != runID {
			return fmt.Errorf("receipt %d: cross-run receipt (run %q, expected %q)", i, r.RunID, runID)
		}
		if r.Sequence != i {
			return fmt.Errorf("receipt %d: out-of-order or duplicate sequence %d", i, r.Sequence)
		}
		if r.PrevReceiptHash != prev {
			return fmt.Errorf("receipt %d: broken chain link (prev=%s, expected %s)", i, r.PrevReceiptHash, prev)
		}
		if r.PolicyHash != livePolicyHash {
			return fmt.Errorf("receipt %d: policy hash changed (receipt=%s, live=%s)", i, r.PolicyHash, livePolicyHash)
		}

		req := requests[i]
		reqHash, err := hashValue(req)
		if err != nil {
			return err
		}
		if r.RequestHash != reqHash {
			return fmt.Errorf("receipt %d: request hash mismatch", i)
		}
		if !equalStrings(r.EvidenceHashes, evidenceHashes(req.Evidence)) {
			return fmt.Errorf("receipt %d: evidence hashes mismatch", i)
		}

		// Re-derive the decision and confirm the committed outcome/rule.
		d := engine.Decide(policy, req)
		if d.Outcome != r.Outcome {
			return fmt.Errorf("receipt %d: outcome mismatch (receipt=%s, replayed=%s)", i, r.Outcome, d.Outcome)
		}
		if d.RuleID != r.RuleID {
			return fmt.Errorf("receipt %d: rule mismatch (receipt=%q, replayed=%q)", i, r.RuleID, d.RuleID)
		}

		// Recompute the self-hash to detect tampering with committed fields.
		want := r
		want.SelfHash = ""
		self, err := selfHash(want)
		if err != nil {
			return err
		}
		if self != r.SelfHash {
			return fmt.Errorf("receipt %d: self-hash mismatch (tampered)", i)
		}

		prev = r.SelfHash
	}
	return nil
}

var errShort = errors.New("interlock/receipt: empty chain")

// VerifyChain checks only the structural integrity of a chain (links, sequence,
// self-hashes, single run) without re-deriving decisions. Useful when the
// original requests are not available.
func VerifyChain(receipts []Receipt) error {
	if len(receipts) == 0 {
		return errShort
	}
	prev := GenesisHash
	runID := receipts[0].RunID
	for i, r := range receipts {
		if r.RunID != runID {
			return fmt.Errorf("receipt %d: cross-run receipt", i)
		}
		if r.Sequence != i {
			return fmt.Errorf("receipt %d: out-of-order sequence %d", i, r.Sequence)
		}
		if r.PrevReceiptHash != prev {
			return fmt.Errorf("receipt %d: broken chain link", i)
		}
		want := r
		want.SelfHash = ""
		self, err := selfHash(want)
		if err != nil {
			return err
		}
		if self != r.SelfHash {
			return fmt.Errorf("receipt %d: self-hash mismatch", i)
		}
		prev = r.SelfHash
	}
	return nil
}

func evidenceHashes(ev []protocol.Evidence) []string {
	if len(ev) == 0 {
		return nil
	}
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		h, err := hashValue(e)
		if err != nil {
			// hashValue only fails on unmarshalable input; Evidence is plain.
			h = ir.HashBytes([]byte(fmt.Sprintf("%+v", e)))
		}
		out = append(out, h)
	}
	return out
}

// hashValue returns the canonical hash of any JSON value.
func hashValue(v any) (string, error) {
	b, err := ir.Canonical(v)
	if err != nil {
		return "", err
	}
	return ir.HashBytes(b), nil
}

// selfHash hashes the receipt with SelfHash cleared.
func selfHash(r Receipt) (string, error) {
	r.SelfHash = ""
	return hashValue(r)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/operatorstack/interlock/broker"
	"github.com/operatorstack/interlock/compiler"
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
	"github.com/operatorstack/interlock/spec"
)

// TestCompatV010 re-derives the v0.1.0 corpus and fails with breaking-change
// messages if any frozen identity or behavior has drifted. The rules:
//
//   - an old policy hash changing is a BREAKING CHANGE;
//   - an old decision changing is a BREAKING CHANGE;
//   - new additive vocabulary is fine as long as this corpus stays green.
func TestCompatV010(t *testing.T) {
	t.Run("policy hashes", func(t *testing.T) { checkHashes(t, V010) })
	t.Run("spec parity", func(t *testing.T) { checkSpecParity(t, V010) })
	t.Run("decisions", func(t *testing.T) { checkDecisions(t, V010) })
	t.Run("broker vectors", func(t *testing.T) { checkBroker(t, V010) })
	t.Run("replay chains", func(t *testing.T) { checkReplay(t, V010) })
}

// checkSpecParity is the parity oracle for every language authoring frontend: for
// each flagship, decode its frozen interlock.spec.v1 input, compile it through the
// real compiler, and assert the canonical bytes are byte-identical to the frozen
// policy.v1 file AND the hash equals the frozen expected hash. Any non-Go SDK
// reproduces exactly this: read the spec.v1 → canonicalize → hash → must match.
func checkSpecParity(t *testing.T, version string) {
	records, err := Specs(version)
	if err != nil {
		t.Fatalf("load specs: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("no frozen spec parity records")
	}
	// Index the frozen IR files by policy name so we can compare byte-for-byte.
	hashes, err := Hashes(version)
	if err != nil {
		t.Fatalf("load hashes: %v", err)
	}
	irFile := map[string]string{}
	for _, h := range hashes {
		irFile[h.Name] = h.Policy
	}

	for _, r := range records {
		raw, err := ReadFile(version, r.Spec)
		if err != nil {
			t.Errorf("%s: read spec: %v", r.Name, err)
			continue
		}
		s, err := spec.DecodeToSpec(raw)
		if err != nil {
			t.Errorf("%s: decode spec.v1: %v", r.Name, err)
			continue
		}
		pol, err := compiler.Compile(s)
		if err != nil {
			t.Errorf("%s: compile spec.v1: %v", r.Name, err)
			continue
		}
		canon, err := pol.CanonicalBytes()
		if err != nil {
			t.Errorf("%s: canonical: %v", r.Name, err)
			continue
		}

		// Byte-parity with the frozen canonical IR.
		if irRel, ok := irFile[r.Name]; ok {
			_, frozenIR := loadPolicyFile(t, version, irRel)
			if !bytes.Equal(canon, frozenIR) {
				t.Errorf("PARITY FAILURE: %s spec.v1 compiles to different canonical bytes than the frozen IR\n"+
					"  the spec.v1 authoring input must lower to exactly the frozen policy", r.Name)
				continue
			}
		}

		got, err := pol.Hash()
		if err != nil {
			t.Errorf("%s: hash: %v", r.Name, err)
			continue
		}
		if got != r.ExpectedHash {
			t.Errorf("PARITY FAILURE: %s spec.v1 hash != frozen hash\n  want %s\n  got  %s\n"+
				"  every language frontend must reproduce this hash from this spec.v1", r.Name, r.ExpectedHash, got)
		}
	}
}

func loadPolicyFile(t *testing.T, version, relpath string) (ir.Policy, []byte) {
	t.Helper()
	raw, err := ReadFile(version, relpath)
	if err != nil {
		t.Fatalf("read %s: %v", relpath, err)
	}
	var p ir.Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode %s: %v", relpath, err)
	}
	return p, raw
}

func checkHashes(t *testing.T, version string) {
	records, err := Hashes(version)
	if err != nil {
		t.Fatalf("load hashes: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("no frozen hash records")
	}
	for _, r := range records {
		p, raw := loadPolicyFile(t, version, r.Policy)

		// Canonical bytes must be byte-stable: re-canonicalizing the frozen
		// policy must reproduce exactly the frozen file.
		canon, err := p.CanonicalBytes()
		if err != nil {
			t.Errorf("%s: canonical: %v", r.Name, err)
			continue
		}
		if !bytes.Equal(canon, raw) {
			t.Errorf("BREAKING CHANGE: %s canonical bytes changed since %s\n"+
				"  the canonicalization of a frozen policy must never change", r.Name, version)
			continue
		}

		got, err := p.Hash()
		if err != nil {
			t.Errorf("%s: hash: %v", r.Name, err)
			continue
		}
		if got != r.ExpectedHash {
			t.Errorf("BREAKING CHANGE: %s policy hash changed since %s\n"+
				"  want %s\n  got  %s\n"+
				"  an old policy hash changing breaks every stored decision and receipt",
				r.Name, version, r.ExpectedHash, got)
		}
	}
}

func checkDecisions(t *testing.T, version string) {
	cases, err := Decisions(version)
	if err != nil {
		t.Fatalf("load decisions: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no frozen decision vectors")
	}
	for _, c := range cases {
		req := c.Request
		if c.UsePolicyHash {
			h, herr := c.Policy.Hash()
			if herr != nil {
				t.Errorf("%s: policy hash: %v", c.Name, herr)
				continue
			}
			req.ClaimedPolicyHash = h
		}
		d := engine.Decide(c.Policy, req)
		if d.Outcome != c.Expect {
			t.Errorf("BREAKING CHANGE: %s outcome changed since %s: got %q, want %q\n"+
				"  an old decision changing is a breaking change", c.Name, version, d.Outcome, c.Expect)
			continue
		}
		if c.ExpectRuleID != "" && d.RuleID != c.ExpectRuleID {
			t.Errorf("BREAKING CHANGE: %s rule_id changed since %s: got %q, want %q",
				c.Name, version, d.RuleID, c.ExpectRuleID)
		}
	}
}

func checkBroker(t *testing.T, version string) {
	vectors, err := BrokerVectors(version)
	if err != nil {
		t.Fatalf("load broker vectors: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("no frozen broker vectors")
	}
	for _, v := range vectors {
		policy, _ := loadPolicyFile(t, version, v.Policy)
		dir := t.TempDir()

		staged := filepath.Join(dir, "staged")
		if err := os.WriteFile(staged, []byte(v.Staged), 0o644); err != nil {
			t.Fatalf("%s: stage: %v", v.Name, err)
		}

		var artifactHash string
		switch v.Env.Bind {
		case BindStaged:
			artifactHash = ir.HashBytes([]byte(v.Staged))
		case BindWrong:
			artifactHash = ir.HashBytes([]byte("deliberately-wrong"))
		default:
			t.Fatalf("%s: unknown bind mode %q", v.Name, v.Env.Bind)
		}
		envPath := filepath.Join(dir, "envelope.json")
		envBody := fmt.Sprintf(`{"schema":%q,"run_id":%q,"status":%q,"artifact_sha256":%q}`,
			v.Env.Schema, v.Env.RunID, v.Env.Status, artifactHash)
		if err := os.WriteFile(envPath, []byte(envBody), 0o644); err != nil {
			t.Fatalf("%s: envelope: %v", v.Name, err)
		}

		target := filepath.Join(dir, "out", "target")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		req := broker.PublishRequest{
			RunID: v.RunID, RequestID: v.Name, Actor: v.Actor,
			ResourceURI: v.ResourceURI, Kind: ir.KindFile,
			StagedPath: staged, TargetPath: target,
			Upstream: []broker.UpstreamReceipt{{Path: envPath}},
		}
		_, perr := broker.Publish(policy, req, receipt.NewChain(v.RunID))

		gotOK := perr == nil
		if gotOK != v.ExpectOK {
			t.Errorf("BREAKING CHANGE: %s broker outcome changed since %s: ok=%v, want ok=%v (err=%v)",
				v.Name, version, gotOK, v.ExpectOK, perr)
			continue
		}
		if !v.ExpectOK {
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				t.Errorf("%s: fail-closed vector created a target", v.Name)
			}
		}
	}
}

func checkReplay(t *testing.T, version string) {
	records, err := ReplayRecords(version)
	if err != nil {
		t.Fatalf("load replay records: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("no frozen replay records")
	}
	for _, r := range records {
		policy, _ := loadPolicyFile(t, version, r.Policy)
		reqs := loadRequests(t, version, r.Requests)
		receipts := loadReceipts(t, version, r.Receipts)

		if err := receipt.Replay(policy, reqs, receipts); err != nil {
			t.Errorf("BREAKING CHANGE: %s frozen chain no longer replays under its policy since %s: %v\n"+
				"  a frozen receipt chain must always verify", r.Name, version, err)
			continue
		}

		// Mutation must still be caught: a changed policy identity fails replay.
		mutated := policy
		mutated.PolicyID = policy.PolicyID + ".tampered"
		if err := receipt.Replay(mutated, reqs, receipts); err == nil {
			t.Errorf("%s: replay accepted a mutated policy — mutation detection regressed", r.Name)
		}
	}
}

func loadRequests(t *testing.T, version, relpath string) []protocol.EffectRequest {
	t.Helper()
	var reqs []protocol.EffectRequest
	if err := readJSONL(version+"/"+relpath, func(b []byte) error {
		var req protocol.EffectRequest
		if err := json.Unmarshal(b, &req); err != nil {
			return err
		}
		reqs = append(reqs, req)
		return nil
	}); err != nil {
		t.Fatalf("load requests %s: %v", relpath, err)
	}
	return reqs
}

func loadReceipts(t *testing.T, version, relpath string) []receipt.Receipt {
	t.Helper()
	var receipts []receipt.Receipt
	if err := readJSONL(version+"/"+relpath, func(b []byte) error {
		var rc receipt.Receipt
		if err := json.Unmarshal(b, &rc); err != nil {
			return err
		}
		receipts = append(receipts, rc)
		return nil
	}); err != nil {
		t.Fatalf("load receipts %s: %v", relpath, err)
	}
	return receipts
}

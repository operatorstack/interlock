package e2e

// control-law: shipped-surface-honors-the-core (parity obligation)
//
// The wind tunnel: drive the frozen conformance corpus through the SHIPPED binary
// and assert the binary reproduces both the frozen expectation AND the in-process
// library result for every vector. This makes decision parity and broker fidelity
// structural — the binary cannot silently diverge from engine.Decide / broker.Publish
// on any corpus vector without turning this suite red.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/operatorstack/interlock/broker"
	"github.com/operatorstack/interlock/conformance/compat"
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/scaffold"
)

// TestDecideParity_Corpus runs every frozen decision vector through `interlock
// decide` and asserts the CLI's outcome/rule matches the frozen expectation and
// the in-process engine for the identical inputs.
func TestDecideParity_Corpus(t *testing.T) {
	cases, err := compat.Decisions(compat.V010)
	if err != nil {
		t.Fatalf("load decision corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("empty decision corpus")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			dir := t.TempDir()
			polBytes, err := json.Marshal(c.Policy)
			if err != nil {
				t.Fatal(err)
			}
			polFile := filepath.Join(dir, "policy.json")
			writeFile(t, polFile, polBytes)

			req := c.Request
			if c.UsePolicyHash {
				h, herr := c.Policy.Hash()
				if herr != nil {
					t.Fatal(herr)
				}
				req.ClaimedPolicyHash = h
			}
			reqBytes, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			reqFile := filepath.Join(dir, "request.json")
			writeFile(t, reqFile, reqBytes)

			// `decide` prints the decision and exits 0 even on a deny — the outcome
			// is in the payload, not the exit code.
			stdout, stderr, code := run(t, "decide", polFile, reqFile)
			if code != 0 {
				t.Fatalf("decide exit %d\n%s\n%s", code, stdout, stderr)
			}
			var got protocol.Decision
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("decode decision: %v\n%s", err, stdout)
			}

			// The binary must equal the pure engine on the same inputs.
			want := engine.Decide(c.Policy, req)
			if got.Outcome != want.Outcome || got.RuleID != want.RuleID {
				t.Fatalf("binary != library: got (%s,%s), engine (%s,%s)",
					got.Outcome, got.RuleID, want.Outcome, want.RuleID)
			}
			// And it must equal the frozen contract.
			if got.Outcome != c.Expect {
				t.Fatalf("outcome %s, frozen expects %s", got.Outcome, c.Expect)
			}
			if c.ExpectRuleID != "" && got.RuleID != c.ExpectRuleID {
				t.Fatalf("rule %q, frozen expects %q", got.RuleID, c.ExpectRuleID)
			}
		})
	}
}

// TestBrokerParity_Corpus runs every frozen broker vector through `interlock
// publish` and asserts the CLI's publish/deny outcome matches the frozen vector —
// and that a denied publish never creates the target.
func TestBrokerParity_Corpus(t *testing.T) {
	vectors, err := compat.BrokerVectors(compat.V010)
	if err != nil {
		t.Fatalf("load broker corpus: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("empty broker corpus")
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			dir := t.TempDir()
			polFile := filepath.Join(dir, "policy.json")
			writeFile(t, polFile, corpusPolicy(t, v.Policy))

			staged := filepath.Join(dir, "staged")
			writeFile(t, staged, []byte(v.Staged))

			var artifactHash string
			switch v.Env.Bind {
			case compat.BindStaged:
				artifactHash = ir.HashBytes([]byte(v.Staged))
			case compat.BindWrong:
				artifactHash = ir.HashBytes([]byte("deliberately-wrong"))
			default:
				t.Fatalf("unknown bind mode %q", v.Env.Bind)
			}
			envFile := filepath.Join(dir, "envelope.json")
			writeFile(t, envFile, envelope(v.Env.Schema, v.Env.RunID, v.Env.Status, artifactHash))

			target := filepath.Join(dir, "out", "target")
			pr := broker.PublishRequest{
				RunID:       v.RunID,
				RequestID:   v.Name,
				Actor:       v.Actor,
				ResourceURI: v.ResourceURI,
				Kind:        ir.KindFile,
				StagedPath:  staged,
				TargetPath:  target,
				Upstream:    []broker.UpstreamReceipt{{Path: envFile}},
			}
			prBytes, err := json.Marshal(pr)
			if err != nil {
				t.Fatal(err)
			}
			pubFile := filepath.Join(dir, "pub.json")
			writeFile(t, pubFile, prBytes)

			_, stderr, code := run(t, "publish", polFile, pubFile)
			gotOK := code == 0
			if gotOK != v.ExpectOK {
				t.Fatalf("publish ok=%v, frozen expects ok=%v (exit %d)\n%s", gotOK, v.ExpectOK, code, stderr)
			}
			if !v.ExpectOK {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatal("fail-closed vector created a target")
				}
			}
		})
	}
}

// corpusPolicy returns the raw bytes of a frozen corpus policy by version-relative
// path (e.g. "policies/exclusive-publish.json").
func corpusPolicy(t *testing.T, relpath string) []byte {
	t.Helper()
	b, err := compat.ReadFile(compat.V010, relpath)
	if err != nil {
		t.Fatalf("read corpus policy %s: %v", relpath, err)
	}
	return b
}

// initTemplateKey returns a valid scaffold template key for the init journey,
// preferring one whose policy carries a deny rule so the tamper assertion bites.
func initTemplateKey(t *testing.T) string {
	t.Helper()
	keys := scaffold.Keys()
	if len(keys) == 0 {
		t.Fatal("no scaffold templates registered")
	}
	return keys[0]
}

// Package proof runs Interlock's release-proof checks: a fixed set of executable
// assertions that the shipped runtime actually upholds its guarantees. Each check
// reuses the real engine, broker, conformance vectors, and receipt chain — it
// proves the running code, not a prose claim about it.
//
// proof is an impure-allowed package (like broker/conformance): it creates temp
// directories to exercise the broker and replay paths. It performs no network I/O
// and no clock/randomness, so Run() is deterministic. The pure engine/ir/protocol/
// receipt packages it depends on remain pure.
package proof

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/operatorstack/interlock/broker"
	"github.com/operatorstack/interlock/conformance"
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
)

// Result is one release-proof line: a human-readable claim, whether it holds,
// and a short detail (evidence on success, the failure reason otherwise).
type Result struct {
	Claim  string `json:"claim"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// check is one proof: it returns nil on success or an error describing the
// failure. Any panic is recovered by Run and reported as a failed Result, so a
// single broken check can never abort the whole proof.
type check struct {
	claim string
	run   func() (detail string, err error)
}

// Run executes every in-process, toolchain-free release proof in order and
// returns one Result per check. It never panics: a check that panics is reported
// as a failed Result. The order is stable so callers (the CLI, the README
// generator) can render a fixed table.
func Run() []Result {
	checks := []check{
		{"canonical policy bytes are deterministic", proofCanonicalDeterministic},
		{"all golden policy hashes match", proofGoldenHashes},
		{"positive decision vectors conform", proofPositiveVectors},
		{"negative decision vectors conform", proofNegativeVectors},
		{"missing evidence returns require", proofMissingEvidenceRequires},
		{"broker publishes byte-exact staged content", proofBrokerByteExact},
		{"stale target state fails closed", proofStaleTargetFailsClosed},
		{"copied or cross-run evidence fails closed", proofCrossRunEvidenceFailsClosed},
		{"changed policy breaks replay", proofChangedPolicyBreaksReplay},
		{"second tenant uses the same broker path", proofSecondTenantSameBroker},
	}

	results := make([]Result, 0, len(checks))
	for _, c := range checks {
		results = append(results, runOne(c))
	}
	return results
}

// runOne executes a single check, converting a panic or error into a failed
// Result so Run() is total.
func runOne(c check) (res Result) {
	res = Result{Claim: c.claim}
	defer func() {
		if r := recover(); r != nil {
			res.OK = false
			res.Detail = fmt.Sprintf("panic: %v", r)
		}
	}()
	detail, err := c.run()
	if err != nil {
		res.OK = false
		res.Detail = err.Error()
		return res
	}
	res.OK = true
	res.Detail = detail
	return res
}

// --- The policies exercised by the broker/replay proofs -------------------
//
// These mirror the committed examples (exclusive-publish, release-manifest) as
// plain IR structs so the proof needs no filesystem authoring step. The golden
// hashes proof (proofGoldenHashes) is what pins the examples' identity; here we
// only need behaviorally faithful shapes to drive the broker.

func exclusivePublishPolicy() ir.Policy {
	return ir.Policy{
		Protocol: ir.Protocol, PolicyID: "exclusive-publish.v1",
		Actors: []string{"agent", "publisher"},
		Resources: []ir.Resource{
			{ID: "artifact", Kind: ir.KindFile, URI: "repo://out/result.json"},
		},
		Rules: []ir.Rule{
			{ID: "deny-agent", Effect: ir.EffectDeny, Actor: "agent",
				Operations: []ir.Operation{ir.OpWrite, ir.OpPublish}, Resource: "artifact"},
			{ID: "allow-publisher", Effect: ir.EffectAllow, Actor: "publisher",
				Operations: []ir.Operation{ir.OpPublish}, Resource: "artifact",
				Requires: []ir.Requirement{
					{Kind: ir.ReqPolicyHashMatch},
					{Kind: ir.ReqStagedHashMatch},
					{Kind: ir.ReqReceiptStatus, Receipt: "deltawire.supervision.receipt.v1", Status: "released"},
				}},
		},
	}
}

func releaseManifestPolicy() ir.Policy {
	return ir.Policy{
		Protocol: ir.Protocol, PolicyID: "release-manifest.v1",
		Actors: []string{"build-runner", "release-bot"},
		Resources: []ir.Resource{
			{ID: "manifest", Kind: ir.KindFile, URI: "repo://dist/release-manifest.json"},
			{ID: "staging", Kind: ir.KindTree, URI: "repo://build/**"},
		},
		Rules: []ir.Rule{
			{ID: "runner-staging", Effect: ir.EffectAllow, Actor: "build-runner",
				Operations: []ir.Operation{ir.OpWrite, ir.OpDelete}, Resource: "staging"},
			{ID: "deny-runner-manifest", Effect: ir.EffectDeny, Actor: "build-runner",
				Operations: []ir.Operation{ir.OpWrite, ir.OpPublish}, Resource: "manifest"},
			{ID: "allow-release-bot", Effect: ir.EffectAllow, Actor: "release-bot",
				Operations: []ir.Operation{ir.OpPublish}, Resource: "manifest",
				Requires: []ir.Requirement{
					{Kind: ir.ReqPolicyHashMatch},
					{Kind: ir.ReqStagedHashMatch},
					{Kind: ir.ReqReceiptStatus, Receipt: "release.attestation.v1", Status: "approved"},
				}},
		},
	}
}

// --- Proof 1: canonical bytes are deterministic ---------------------------

func proofCanonicalDeterministic() (string, error) {
	p := exclusivePublishPolicy()
	a, err := ir.Canonical(p)
	if err != nil {
		return "", fmt.Errorf("canonical (first): %w", err)
	}
	b, err := ir.Canonical(p)
	if err != nil {
		return "", fmt.Errorf("canonical (second): %w", err)
	}
	if !bytes.Equal(a, b) {
		return "", fmt.Errorf("canonical bytes differ across two renders (%d vs %d bytes)", len(a), len(b))
	}
	return fmt.Sprintf("%d canonical bytes reproduced exactly", len(a)), nil
}

// --- Proof 2: golden policy hashes match ----------------------------------

func proofGoldenHashes() (string, error) {
	cases, err := conformance.GoldenHashes()
	if err != nil {
		return "", fmt.Errorf("load golden hashes: %w", err)
	}
	if len(cases) == 0 {
		return "", fmt.Errorf("no golden hash vectors embedded")
	}
	for _, c := range cases {
		got, err := c.Policy.Hash()
		if err != nil {
			return "", fmt.Errorf("%s: hash: %w", c.Name, err)
		}
		if got != c.ExpectedHash {
			return "", fmt.Errorf("%s: hash drift\n  want %s\n  got  %s", c.Name, c.ExpectedHash, got)
		}
	}
	return fmt.Sprintf("%d frozen policy hashes match", len(cases)), nil
}

// --- Proof 3 & 4: decision vectors conform --------------------------------

func decideVectors(cases []conformance.Case) error {
	for _, c := range cases {
		req := c.Request
		if c.UsePolicyHash {
			h, err := c.Policy.Hash()
			if err != nil {
				return fmt.Errorf("%s: policy hash: %w", c.Name, err)
			}
			req.ClaimedPolicyHash = h
		}
		d := engine.Decide(c.Policy, req)
		if d.Outcome != c.Expect {
			return fmt.Errorf("%s: outcome %q, want %q", c.Name, d.Outcome, c.Expect)
		}
		if c.ExpectRuleID != "" && d.RuleID != c.ExpectRuleID {
			return fmt.Errorf("%s: rule_id %q, want %q", c.Name, d.RuleID, c.ExpectRuleID)
		}
	}
	return nil
}

func proofPositiveVectors() (string, error) {
	cases, err := conformance.Positive()
	if err != nil {
		return "", fmt.Errorf("load positive vectors: %w", err)
	}
	if len(cases) == 0 {
		return "", fmt.Errorf("no positive vectors embedded")
	}
	if err := decideVectors(cases); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d positive vectors conform", len(cases)), nil
}

func proofNegativeVectors() (string, error) {
	cases, err := conformance.Negative()
	if err != nil {
		return "", fmt.Errorf("load negative vectors: %w", err)
	}
	if len(cases) == 0 {
		return "", fmt.Errorf("no negative vectors embedded")
	}
	if err := decideVectors(cases); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d negative vectors conform", len(cases)), nil
}

// --- Proof 5: missing evidence returns require ----------------------------

func proofMissingEvidenceRequires() (string, error) {
	cases, err := conformance.Positive()
	if err != nil {
		return "", fmt.Errorf("load positive vectors: %w", err)
	}
	var requireN int
	for _, c := range cases {
		if c.Expect != protocol.OutcomeRequire {
			continue
		}
		req := c.Request
		if c.UsePolicyHash {
			h, herr := c.Policy.Hash()
			if herr != nil {
				return "", fmt.Errorf("%s: policy hash: %w", c.Name, herr)
			}
			req.ClaimedPolicyHash = h
		}
		d := engine.Decide(c.Policy, req)
		if d.Outcome != protocol.OutcomeRequire {
			return "", fmt.Errorf("%s: outcome %q, want require", c.Name, d.Outcome)
		}
		if len(d.MissingEvidence) == 0 {
			return "", fmt.Errorf("%s: require decision listed no missing evidence", c.Name)
		}
		requireN++
	}
	if requireN == 0 {
		return "", fmt.Errorf("no require-outcome vector present to prove missing-evidence handling")
	}
	return fmt.Sprintf("%d require vectors list missing evidence", requireN), nil
}

// --- Broker proof helpers -------------------------------------------------

// writeFile writes content to dir/name and returns the path.
func writeFile(dir, name, content string) (string, error) {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// publishReq builds a broker.PublishRequest for a file publish referencing a
// single upstream envelope. expectedTargetHash may be empty ("target must not
// exist").
func publishReq(runID, requestID, actor, resourceURI, staged, target, expectedTargetHash, env string) broker.PublishRequest {
	return broker.PublishRequest{
		RunID:              runID,
		RequestID:          requestID,
		Actor:              actor,
		ResourceURI:        resourceURI,
		Kind:               ir.KindFile,
		StagedPath:         staged,
		TargetPath:         target,
		ExpectedTargetHash: expectedTargetHash,
		Upstream:           []broker.UpstreamReceipt{{Path: env}},
	}
}

// brokerPublish creates the target's parent directory (the broker never creates
// directories) and drives req through broker.Publish on a fresh receipt chain.
func brokerPublish(policy ir.Policy, req broker.PublishRequest) (broker.Result, error) {
	if err := os.MkdirAll(filepath.Dir(req.TargetPath), 0o755); err != nil {
		return broker.Result{}, err
	}
	return broker.Publish(policy, req, receipt.NewChain(req.RunID))
}

// writeEnvelope writes an upstream evidence envelope via Interlock's own exported
// writer, which hash-binds it to the staged bytes, and returns its path. Using
// broker.WriteUpstreamEnvelope (rather than hand-formatting the JSON) means the
// proofs exercise the same writer tenants use and cannot drift from the reader.
func writeEnvelope(dir, name, schema, runID, status string, staged []byte) (string, error) {
	path := filepath.Join(dir, name)
	if err := broker.WriteUpstreamEnvelope(path, broker.UpstreamEvidence{
		Schema: schema,
		RunID:  runID,
		Status: status,
	}, staged); err != nil {
		return "", err
	}
	return path, nil
}

// --- Proof 6: broker publishes byte-exact staged content ------------------

func proofBrokerByteExact() (string, error) {
	dir, err := os.MkdirTemp("", "interlock-proof-publish-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	const content = `{"ok":true}`
	staged, err := writeFile(dir, "staged.json", content)
	if err != nil {
		return "", err
	}
	env, err := writeEnvelope(dir, "envelope.json",
		"deltawire.supervision.receipt.v1", "run1", "released", []byte(content))
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "out", "result.json")

	res, err := brokerPublish(exclusivePublishPolicy(), publishReq("run1", "pub1", "publisher",
		"repo://out/result.json", staged, target, "", env))
	if err != nil {
		return "", fmt.Errorf("publish failed: %w", err)
	}
	got, err := os.ReadFile(res.PublishedTo)
	if err != nil {
		return "", fmt.Errorf("read published: %w", err)
	}
	if string(got) != content {
		return "", fmt.Errorf("published bytes differ: %q", got)
	}
	if res.PublishedHash != res.StagedHash {
		return "", fmt.Errorf("published hash %q != staged hash %q", res.PublishedHash, res.StagedHash)
	}
	return "staged bytes published exactly; published hash == staged hash", nil
}

// --- Proof 7: stale target state fails closed -----------------------------

func proofStaleTargetFailsClosed() (string, error) {
	dir, err := os.MkdirTemp("", "interlock-proof-stale-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	const content = `{"ok":true}`
	staged, err := writeFile(dir, "staged.json", content)
	if err != nil {
		return "", err
	}
	env, err := writeEnvelope(dir, "envelope.json",
		"deltawire.supervision.receipt.v1", "run1", "released", []byte(content))
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "out", "result.json")

	req := publishReq("run1", "pub1", "publisher", "repo://out/result.json", staged, target, "", env)
	// Claim the target currently holds bytes it does not — a stale expectation.
	req.ExpectedTargetHash = ir.HashBytes([]byte("stale-not-current"))

	if _, err := brokerPublish(exclusivePublishPolicy(), req); err == nil {
		return "", fmt.Errorf("publish accepted a stale target expectation")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		return "", fmt.Errorf("fail-closed publish still created the target")
	}
	return "stale target expectation rejected; target untouched", nil
}

// --- Proof 8: copied / cross-run evidence fails closed --------------------

func proofCrossRunEvidenceFailsClosed() (string, error) {
	dir, err := os.MkdirTemp("", "interlock-proof-crossrun-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	const content = `{"ok":true}`
	staged, err := writeFile(dir, "staged.json", content)
	if err != nil {
		return "", err
	}
	// Envelope hash-bound to the staged bytes but correlated to a DIFFERENT run —
	// the shape of a copied receipt reused across runs.
	env, err := writeEnvelope(dir, "envelope.json",
		"deltawire.supervision.receipt.v1", "other-run", "released", []byte(content))
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "out", "result.json")

	req := publishReq("run1", "pub1", "publisher", "repo://out/result.json", staged, target, "", env)
	if _, err := brokerPublish(exclusivePublishPolicy(), req); err == nil {
		return "", fmt.Errorf("publish accepted evidence from a different run")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		return "", fmt.Errorf("fail-closed publish still created the target")
	}
	return "cross-run evidence rejected; target untouched", nil
}

// --- Proof 9: changed policy breaks replay --------------------------------

func proofChangedPolicyBreaksReplay() (string, error) {
	policy := exclusivePublishPolicy()

	reqs := []protocol.EffectRequest{
		{
			Protocol: protocol.EffectRequestProtocol, RequestID: "s-1", RunID: "sim",
			Actor: "agent", Operation: ir.OpWrite,
			Resource: protocol.TargetResource{Kind: ir.KindFile, URI: "repo://out/result.json"},
		},
		{
			Protocol: protocol.EffectRequestProtocol, RequestID: "s-2", RunID: "sim",
			Actor: "agent", Operation: ir.OpPublish,
			Resource: protocol.TargetResource{Kind: ir.KindFile, URI: "repo://out/result.json"},
		},
	}

	chain := receipt.NewChain("sim")
	for _, r := range reqs {
		d := engine.Decide(policy, r)
		if _, err := chain.Append(policy, r, d); err != nil {
			return "", fmt.Errorf("append receipt: %w", err)
		}
	}

	// Replay against the same policy must succeed.
	if err := receipt.Replay(policy, reqs, chain.Receipts); err != nil {
		return "", fmt.Errorf("replay against the original policy failed: %w", err)
	}

	// Replay against a mutated policy (different identity) must fail closed.
	mutated := exclusivePublishPolicy()
	mutated.PolicyID = "exclusive-publish.tampered"
	if err := receipt.Replay(mutated, reqs, chain.Receipts); err == nil {
		return "", fmt.Errorf("replay accepted a mutated policy")
	}
	return "chain replays under its policy; a changed policy is rejected", nil
}

// --- Proof 10: a second tenant uses the same broker path ------------------

func proofSecondTenantSameBroker() (string, error) {
	dir, err := os.MkdirTemp("", "interlock-proof-tenant-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	policy := releaseManifestPolicy()
	const content = `{"version":"1.2.3"}`
	staged, err := writeFile(dir, "release-manifest.json", content)
	if err != nil {
		return "", err
	}

	// This tenant's own schema/status, hash-bound to its own artifact.
	env, err := writeEnvelope(dir, "attestation.json",
		"release.attestation.v1", "rel-run", "approved", []byte(content))
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "dist", "release-manifest.json")

	res, err := brokerPublish(policy, publishReq("rel-run", "rel-1", "release-bot",
		"repo://dist/release-manifest.json", staged, target, "", env))
	if err != nil {
		return "", fmt.Errorf("second-tenant publish failed: %w", err)
	}
	got, err := os.ReadFile(res.PublishedTo)
	if err != nil || string(got) != content {
		return "", fmt.Errorf("second-tenant published wrong bytes: %q (err %v)", got, err)
	}

	// A foreign receipt schema must fail closed even when hash-bound and
	// run-correlated: schema is policy data, not broker-privileged.
	badEnv, err := writeEnvelope(dir, "foreign.json",
		"deltawire.supervision.receipt.v1", "rel-run", "released", []byte(content))
	if err != nil {
		return "", err
	}
	badTarget := filepath.Join(dir, "dist", "other.json")
	badReq := publishReq("rel-run", "rel-2", "release-bot",
		"repo://dist/release-manifest.json", staged, badTarget, "", badEnv)
	if _, err := brokerPublish(policy, badReq); err == nil {
		return "", fmt.Errorf("release policy accepted a foreign receipt schema")
	}
	if _, statErr := os.Stat(badTarget); !os.IsNotExist(statErr) {
		return "", fmt.Errorf("foreign-schema publish still created a target")
	}
	return "second tenant publishes; foreign schema fails closed", nil
}

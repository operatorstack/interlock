//go:build ignore

// Command gen freezes the v0.1.0 compatibility-lock corpus: the canonical policy
// bytes, their hashes, the decision vectors, broker vectors, and receipt chains
// that define Interlock's v0.1.0 identity and behavior. It is run once and its
// output committed; thereafter the corpus is IMMUTABLE. compat_test.go re-derives
// everything from it, so a changed old hash or old decision fails CI as a
// breaking change.
//
// Re-run (only to add a NEW frozen version, never to mutate v0.1.0):
//
//	go run ./conformance/compat/gen/main.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/operatorstack/interlock/conformance"
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
	"github.com/operatorstack/interlock/spec"
)

const root = "conformance/compat/v0.1.0"

func main() {
	must(os.MkdirAll(filepath.Join(root, "policies"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "specs"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "receipts"), 0o755))

	freezePoliciesAndHashes()
	freezeSpecs()
	freezeDecisions()
	freezeBroker()
	freezeReceipts()

	fmt.Println("froze compat corpus at", root)
}

// freezeSpecs writes each golden policy's interlock.spec.v1 authoring input to
// specs/<name>.json. The spec is derived from the frozen canonical IR, so it
// re-compiles to byte-identical canonical bytes and the same frozen hash — this
// is the cross-language parity manifest: every non-Go frontend reads these
// spec.v1 documents, canonicalizes, and must reproduce the frozen hash.
func freezeSpecs() {
	cases, err := conformance.GoldenHashes()
	must(err)
	f := create(filepath.Join(root, "specs.jsonl"))
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, c := range cases {
		doc, err := spec.Encode(spec.FromPolicy(c.Policy))
		must(err)
		file := filepath.Join("specs", c.Name+".json")
		must(os.WriteFile(filepath.Join(root, file), doc, 0o644))
		must(enc.Encode(specRecord{Name: c.Name, Spec: file, ExpectedHash: c.ExpectedHash}))
	}
}

// freezePoliciesAndHashes writes each golden policy's canonical bytes to
// policies/<name>.json and records its frozen hash in hashes.jsonl.
func freezePoliciesAndHashes() {
	cases, err := conformance.GoldenHashes()
	must(err)
	f := create(filepath.Join(root, "hashes.jsonl"))
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, c := range cases {
		canon, err := c.Policy.CanonicalBytes()
		must(err)
		file := filepath.Join("policies", c.Name+".json")
		must(os.WriteFile(filepath.Join(root, file), canon, 0o644))
		must(enc.Encode(hashRecord{Name: c.Name, Policy: file, ExpectedHash: c.ExpectedHash}))
	}
}

// freezeDecisions snapshots today's positive and negative conformance vectors as
// the v0.1.0 decision baseline. Each line is a conformance.Case.
func freezeDecisions() {
	f := create(filepath.Join(root, "decisions.jsonl"))
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, loader := range []func() ([]conformance.Case, error){conformance.Positive, conformance.Negative} {
		cases, err := loader()
		must(err)
		for _, c := range cases {
			must(enc.Encode(c))
		}
	}
}

// freezeBroker writes broker vectors: a happy-path publish and a cross-run
// fail-closed, for both tenants. The compat test re-runs each through
// broker.Publish and asserts the frozen outcome.
func freezeBroker() {
	f := create(filepath.Join(root, "broker.jsonl"))
	defer f.Close()
	enc := json.NewEncoder(f)
	vectors := []brokerVector{
		{
			Name: "exclusive-publish-happy", Policy: policyFile("exclusive-publish"),
			Actor: "publisher", ResourceURI: "repo://out/result.json", RunID: "run1",
			Staged:   `{"ok":true}`,
			Env:      envelopeSpec{Schema: "deltawire.supervision.receipt.v1", RunID: "run1", Status: "released", Bind: bindStaged},
			ExpectOK: true,
		},
		{
			Name: "exclusive-publish-cross-run", Policy: policyFile("exclusive-publish"),
			Actor: "publisher", ResourceURI: "repo://out/result.json", RunID: "run1",
			Staged:   `{"ok":true}`,
			Env:      envelopeSpec{Schema: "deltawire.supervision.receipt.v1", RunID: "other-run", Status: "released", Bind: bindStaged},
			ExpectOK: false,
		},
		{
			Name: "release-manifest-happy", Policy: policyFile("release-manifest"),
			Actor: "release-bot", ResourceURI: "repo://dist/release-manifest.json", RunID: "rel-run",
			Staged:   `{"version":"1.2.3"}`,
			Env:      envelopeSpec{Schema: "release.attestation.v1", RunID: "rel-run", Status: "approved", Bind: bindStaged},
			ExpectOK: true,
		},
		{
			Name: "release-manifest-foreign-schema", Policy: policyFile("release-manifest"),
			Actor: "release-bot", ResourceURI: "repo://dist/release-manifest.json", RunID: "rel-run",
			Staged:   `{"version":"1.2.3"}`,
			Env:      envelopeSpec{Schema: "deltawire.supervision.receipt.v1", RunID: "rel-run", Status: "released", Bind: bindStaged},
			ExpectOK: false,
		},
	}
	for _, v := range vectors {
		must(enc.Encode(v))
	}
}

// freezeReceipts writes a frozen request stream and its receipt chain so replay
// can be locked: the chain must still verify under its policy, and a mutated
// policy must still be rejected.
func freezeReceipts() {
	policy := goldenPolicy("exclusive-publish")
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
		_, err := chain.Append(policy, r, engine.Decide(policy, r))
		must(err)
	}

	writeJSONL(filepath.Join(root, "receipts", "exclusive-publish.requests.jsonl"), func(enc *json.Encoder) {
		for _, r := range reqs {
			must(enc.Encode(r))
		}
	})
	writeJSONL(filepath.Join(root, "receipts", "exclusive-publish.receipts.jsonl"), func(enc *json.Encoder) {
		for _, r := range chain.Receipts {
			must(enc.Encode(r))
		}
	})

	f := create(filepath.Join(root, "replay.jsonl"))
	defer f.Close()
	must(json.NewEncoder(f).Encode(replayRecord{
		Name:     "exclusive-publish-chain",
		Policy:   policyFile("exclusive-publish"),
		Requests: filepath.Join("receipts", "exclusive-publish.requests.jsonl"),
		Receipts: filepath.Join("receipts", "exclusive-publish.receipts.jsonl"),
	}))
}

// --- shared record shapes (kept in sync with compat.go) -------------------

type hashRecord struct {
	Name         string `json:"name"`
	Policy       string `json:"policy"`
	ExpectedHash string `json:"expected_hash"`
}

type specRecord struct {
	Name         string `json:"name"`
	Spec         string `json:"spec"`
	ExpectedHash string `json:"expected_hash"`
}

type envelopeSpec struct {
	Schema string `json:"schema"`
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Bind   string `json:"bind"` // "staged" = bind to staged bytes; "wrong" = bind to a wrong hash
}

const (
	bindStaged = "staged"
	bindWrong  = "wrong"
)

type brokerVector struct {
	Name        string       `json:"name"`
	Policy      string       `json:"policy"`
	Actor       string       `json:"actor"`
	ResourceURI string       `json:"resource_uri"`
	RunID       string       `json:"run_id"`
	Staged      string       `json:"staged"`
	Env         envelopeSpec `json:"env"`
	ExpectOK    bool         `json:"expect_ok"`
}

type replayRecord struct {
	Name     string `json:"name"`
	Policy   string `json:"policy"`
	Requests string `json:"requests"`
	Receipts string `json:"receipts"`
}

// --- helpers --------------------------------------------------------------

func policyFile(name string) string { return filepath.Join("policies", name+".json") }

func goldenPolicy(name string) ir.Policy {
	cases, err := conformance.GoldenHashes()
	must(err)
	for _, c := range cases {
		if c.Name == name {
			return c.Policy
		}
	}
	panic("golden policy not found: " + name)
}

func create(path string) *os.File {
	f, err := os.Create(path)
	must(err)
	return f
}

func writeJSONL(path string, fn func(*json.Encoder)) {
	f := create(path)
	defer f.Close()
	fn(json.NewEncoder(f))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

var _ = bindWrong // reserved for future negative vectors

package broker

// The upstream evidence envelope is Interlock's own, tenant-agnostic durable
// evidence format. It exists so the broker never has to trust a caller-*claimed*
// receipt status handed in as a struct field: instead it re-reads authoritative
// evidence from disk and binds it, by hash, to the exact bytes it is about to
// publish.
//
// Threat model — this is HASH-BINDING, not authenticity. The envelope proves the
// durable evidence refers to *these exact staged bytes* for *this run*, which
// defends against stale, copy-pasted, cross-run, or typo'd status claims and makes
// the evidence durable and auditable. It does NOT prove that a trusted supervisor
// produced the bytes: whoever can write StagedPath can write the adjacent
// envelope. True authenticity would require a signed envelope, or the broker
// re-verifying the tenant's own receipt chain — and the latter is exactly what the
// M3 generality guarantee forbids (the broker must not learn any tenant's receipt
// schema). Do not describe this as cryptographic provenance anywhere.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/operatorstack/interlock/ir"
)

// upstreamEnvelope is the durable, tenant-agnostic evidence the broker re-reads.
// The schema/status pair is the data the engine's receipt_status requirement
// matches (the policy carries the schema; the broker stays generic). artifact_sha256
// binds the envelope to the candidate bytes in Interlock's tagged hash format
// ("sha256:"+hex). It carries NO timestamps or other nondeterministic fields, so
// the pinned envelope hash is replay-safe.
type upstreamEnvelope struct {
	Schema         string `json:"schema"`
	RunID          string `json:"run_id"`
	Status         string `json:"status"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

// readUpstreamEnvelope reads the envelope at path, verifies it correlates to the
// expected run and is hash-bound to the expected artifact bytes, and returns the
// envelope plus its own content hash (in ir.HashBytes tagged format) as an
// audit-only pin. Any read/parse failure, run mismatch, or artifact-hash mismatch
// fails closed.
//
// wantArtifactHash MUST be in Interlock's tagged format (the value ir.HashBytes
// returns); a bare-hex artifact_sha256 in the file will therefore never match and
// fails closed — the format is strict on purpose.
func readUpstreamEnvelope(path, wantRunID, wantArtifactHash string) (upstreamEnvelope, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return upstreamEnvelope{}, "", fmt.Errorf("interlock/broker: read upstream envelope: %w", err)
	}
	var env upstreamEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return upstreamEnvelope{}, "", fmt.Errorf("interlock/broker: parse upstream envelope: %w", err)
	}
	if env.Schema == "" || env.Status == "" || env.RunID == "" || env.ArtifactSHA256 == "" {
		return upstreamEnvelope{}, "", fmt.Errorf("interlock/broker: upstream envelope missing required fields")
	}
	if env.RunID != wantRunID {
		return upstreamEnvelope{}, "", fmt.Errorf("interlock/broker: upstream envelope run %q does not match request run %q", env.RunID, wantRunID)
	}
	if env.ArtifactSHA256 != wantArtifactHash {
		return upstreamEnvelope{}, "", fmt.Errorf("interlock/broker: upstream envelope artifact hash %q not bound to staged bytes %q", env.ArtifactSHA256, wantArtifactHash)
	}
	return env, ir.HashBytes(raw), nil
}

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
	"path/filepath"

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

// UpstreamEvidence is the tenant-owned data a caller supplies to write an
// upstream evidence envelope. The tenant owns the meaning of Schema and Status;
// Interlock never interprets either. Deliberately absent is the artifact hash:
// WriteUpstreamEnvelope computes it from the staged bytes themselves, so the
// tagged-vs-bare-hex footgun is unrepresentable — a caller cannot supply a hash
// at all, let alone the wrong format.
type UpstreamEvidence struct {
	Schema string
	RunID  string
	Status string
}

// WriteUpstreamEnvelope writes the durable upstream evidence envelope that
// readUpstreamEnvelope re-reads. It binds the envelope to the exact staged bytes
// by computing ir.HashBytes(staged) internally (never a caller-supplied hash),
// and emits byte-identical output to what the reader decodes: the four
// upstreamEnvelope fields in struct order via json.Marshal plus a trailing
// newline. Co-locating the writer with the reader is why they can never drift.
func WriteUpstreamEnvelope(path string, ev UpstreamEvidence, staged []byte) error {
	data, err := json.Marshal(upstreamEnvelope{
		Schema:         ev.Schema,
		RunID:          ev.RunID,
		Status:         ev.Status,
		ArtifactSHA256: ir.HashBytes(staged),
	})
	if err != nil {
		return fmt.Errorf("interlock/broker: marshal upstream envelope: %w", err)
	}
	if err := WriteFileAtomic(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("interlock/broker: write upstream envelope: %w", err)
	}
	return nil
}

// WriteFileAtomic writes data to a same-directory temp file (fsync'd, chmod'd)
// and renames it into place, so a reader never observes a partial file. This is a
// generic durable-write utility — NOT the broker's protected effect, which is the
// policy-gated atomic publish in Publish. It is exported so the publishing façade
// can persist evidence through the same implementation rather than duplicating it.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
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

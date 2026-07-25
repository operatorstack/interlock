package e2e

// control-law: shipped-surface-honors-the-core (authority-separation obligation)
//
// The filesystem-authority contract. Interlock is "authority, not interception":
// it does not intercept an agent's syscalls, it withholds authority for the one
// protected write and lends it only to the broker on truthful evidence. This suite
// proves that separation at the shipped boundary:
//
//   (a) the agent's own attempt to write the protected artifact is DENIED by the
//       engine (authority is not the agent's to exercise);
//   (b) the publisher CAN land it through the broker on truthful, hash-bound
//       evidence;
//   (c) the broker refuses to overwrite an existing target (fail closed, bytes
//       untouched);
//   (d) a real filesystem fault on the staged file fails closed — the target is
//       never created.
//
// Scope: this is a single-process hermetic proxy for the authority split. TRUE
// two-principal OS isolation (agent and publisher as separate users/namespaces so
// the agent cannot even reach the broker's inputs) is a deployment property
// (docs/04-enforcement-boundary.md, workspace/workspace.go), out of scope for a
// fast test and deferred to an install-fidelity tier.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

func TestIsolation_AuthoritySeparation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics; skipped on Windows")
	}
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	writeFile(t, policy, exclusivePublishPolicy(t))

	// (a) The agent's direct write on the protected artifact is denied — authority
	// for that effect is not the agent's.
	req := protocol.EffectRequest{
		Protocol:  protocol.EffectRequestProtocol,
		RequestID: "iso-agent",
		RunID:     "iso",
		Actor:     "agent",
		Operation: ir.OpWrite,
		Resource:  protocol.TargetResource{Kind: ir.KindFile, URI: "repo://out/result.json"},
	}
	reqFile := filepath.Join(dir, "agent-write.json")
	writeJSON(t, reqFile, req)
	stdout, _, code := run(t, "decide", policy, reqFile)
	if code != 0 {
		t.Fatalf("decide exit %d", code)
	}
	if d := decodeDecision(t, stdout); d.Outcome != protocol.OutcomeDeny {
		t.Fatalf("agent write on the protected artifact: outcome %s, want deny", d.Outcome)
	}

	// (b) The publisher lands it through the broker on truthful, hash-bound evidence.
	staged := filepath.Join(dir, "stage", "result.json")
	stagedBytes := []byte(`{"ok":true}`)
	writeFile(t, staged, stagedBytes)
	env := filepath.Join(dir, "envelope.json")
	writeFile(t, env, envelope("deltawire.supervision.receipt.v1", "iso", "released", ir.HashBytes(stagedBytes)))
	target := filepath.Join(dir, "out", "result.json")
	pub := filepath.Join(dir, "pub.json")
	writeFile(t, pub, publishRequest("iso", "iso-pub", "publisher", "repo://out/result.json", staged, target, env))
	if so, se, c := run(t, "publish", policy, pub); c != 0 {
		t.Fatalf("publisher could not land the artifact (exit %d):\n%s\n%s", c, so, se)
	}
	if got, _ := os.ReadFile(target); string(got) != string(stagedBytes) {
		t.Fatalf("published bytes wrong: %q", got)
	}

	// (c) The broker refuses to overwrite an existing target (no prior hash
	// expected) — fail closed, existing bytes untouched.
	occupied := filepath.Join(dir, "occupied", "result.json")
	writeFile(t, occupied, []byte("PRE-EXISTING"))
	pubOver := filepath.Join(dir, "pub-overwrite.json")
	writeFile(t, pubOver, publishRequest("iso", "iso-over", "publisher", "repo://out/result.json", staged, occupied, env))
	if _, _, c := run(t, "publish", policy, pubOver); c == 0 {
		t.Fatal("broker overwrote an existing target")
	}
	if got, _ := os.ReadFile(occupied); string(got) != "PRE-EXISTING" {
		t.Fatalf("fail-closed overwrite mutated the target: %q", got)
	}

	// (d) A real filesystem fault (unreadable staged file) fails closed. Skipped
	// when running as root, where mode 0000 is still readable.
	if os.Geteuid() == 0 {
		t.Log("running as root; skipping the unreadable-staged-file case")
		return
	}
	locked := filepath.Join(dir, "locked", "result.json")
	writeFile(t, locked, stagedBytes)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o644)
	lockedEnv := filepath.Join(dir, "locked-env.json")
	writeFile(t, lockedEnv, envelope("deltawire.supervision.receipt.v1", "iso", "released", ir.HashBytes(stagedBytes)))
	lockedTarget := filepath.Join(dir, "locked-out", "result.json")
	pubLocked := filepath.Join(dir, "pub-locked.json")
	writeFile(t, pubLocked, publishRequest("iso", "iso-locked", "publisher", "repo://out/result.json", locked, lockedTarget, lockedEnv))
	if _, _, c := run(t, "publish", policy, pubLocked); c == 0 {
		t.Fatal("publish succeeded despite an unreadable staged file")
	}
	if _, err := os.Stat(lockedTarget); !os.IsNotExist(err) {
		t.Fatal("fail-closed publish created a target from an unreadable staged file")
	}
}

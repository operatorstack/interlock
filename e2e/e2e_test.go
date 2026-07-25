package e2e

// control-law: shipped-surface-honors-the-core
//
// Boundary: the customer's real contact with Interlock. A customer never calls
// the library — they install and run the `interlock` binary (argv, stdin JSON,
// files under --output/.interlock/, stdout, exit code) and delegate one protected
// write to the broker. That argv/exit-code/file surface, plus the broker's promote
// seam resting on OS workspace isolation, is the enforcement boundary ("authority,
// not interception" — docs/04-enforcement-boundary.md). There is no git hook or
// interceptor to test; the artifact itself is the boundary.
//
// Control law: every capability a customer invokes through the installed binary
// produces exactly the decision, artifact, and failure behavior the pure engine /
// compiler / broker produce for the same inputs. Wrong inputs fail closed with a
// non-zero exit; nothing a customer can do through the CLI activates authority the
// library would not.
//
// This package builds the binary once and drives it via os/exec on tiny hermetic
// fixtures — no agents, no network, no VM. The decomposed obligations are realized
// across four files: customer journeys (here), decision/broker parity against the
// frozen conformance corpus (parity_test.go), a coverage law that forces every
// subcommand to have an e2e (coverage_test.go), and the POSIX authority-separation
// & fail-closed contract (isolation_test.go). It runs under the ordinary
// `go test ./...` and the projection build, so the law is enforced by gates that
// already run — no new CI job.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/operatorstack/interlock/broker"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

// interlockBin is the path to the binary built once by TestMain.
var interlockBin string

// TestMain builds the shipped binary a single time from the module root, so every
// test drives the exact artifact a customer would install rather than an in-process
// stand-in.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "interlock-e2e-bin")
	if err != nil {
		panic("e2e: mkdir temp: " + err.Error())
	}
	bin := filepath.Join(tmp, "interlock")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/interlock")
	build.Dir = ".." // the interlock module root (parent of e2e/)
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("e2e: build interlock: " + err.Error())
	}
	interlockBin = bin
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// run executes the built binary and returns stdout, stderr, and the exit code.
// A non-zero exit is a normal outcome (fail-closed paths), not a test error.
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(interlockBin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), errb.String(), ee.ExitCode()
		}
		t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), errb.String(), 0
}

// sampleAgentsMD is the fixture repository's intent: a force-push prohibition and
// a generated-file prohibition (both should ground into proposed deny rules), an
// advisory line (never emittable), and an ambiguous approval (an unresolved
// question). It is written inline rather than kept under testdata/ so the journey
// runs identically in the projected public tree, which excludes testdata/.
const sampleAgentsMD = `# Agent rules

- Never force-push the main branch.
- Do not edit generated files.
- Prefer functional components over class components.
- Ask before publishing a release.
`

// sampleRepo materializes the fixture repository in a temp dir and returns its
// path.
func sampleRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), []byte(sampleAgentsMD))
	return root
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeJSON marshals v and writes it to path.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, b)
}

// decodeDecision parses a protocol.Decision from CLI stdout.
func decodeDecision(t *testing.T, stdout string) protocol.Decision {
	t.Helper()
	var d protocol.Decision
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("decode decision: %v\n%s", err, stdout)
	}
	return d
}

// --- FLAGSHIP: the derive journey, end to end through the binary ---------------

// TestJourney_Derive proves derive safety at the shipped boundary: the command
// writes only reviewable candidates (never an active policy), and the candidate it
// produces is real — it compiles through the binary's own compiler and its vectors
// pass the binary's own test runner. Then it proves the vectors are live by
// tampering the promoted policy and watching `test` turn red.
func TestJourney_Derive(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "derived")

	stdout, stderr, code := run(t, "derive", sampleRepo(t), "--output", out)
	if code != 0 {
		t.Fatalf("derive exit %d\nstdout:%s\nstderr:%s", code, stdout, stderr)
	}
	// Derive safety: no active policy anywhere under --output.
	err := filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "policy.json" {
			t.Fatalf("derive wrote an active policy file: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"candidate.policy.json", "QUESTIONS.md"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("derive did not emit %s: %v", name, err)
		}
	}
	if !strings.Contains(stdout, "Nothing is enforced yet") {
		t.Fatalf("derive output missing the not-enforced framing:\n%s", stdout)
	}

	// Promote through the binary itself: the candidate must compile (proving it is
	// real spec.v1, not a decorative draft).
	candidate := filepath.Join(out, "candidate.policy.json")
	policy := filepath.Join(out, "policy.json")
	if _, se, c := run(t, "compile", candidate, "-o", policy); c != 0 {
		t.Fatalf("compiling the derived candidate failed (exit %d): %s", c, se)
	}
	// Wire the candidate's vectors next to the promoted policy and run the binary's
	// own test runner — these are live engine decisions.
	tests, err := os.ReadFile(filepath.Join(out, "candidate.tests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(out, "tests.jsonl"), tests)
	if so, se, c := run(t, "test", out); c != 0 {
		t.Fatalf("derived candidate's own vectors did not pass (exit %d):\n%s\n%s", c, so, se)
	}

	// Test honesty: tamper the promoted policy and the vectors must turn red.
	raw, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.ReplaceAll(raw, []byte(`"effect":"deny"`), []byte(`"effect":"allow"`))
	if bytes.Equal(tampered, raw) {
		t.Fatal("expected the derived policy to contain a deny rule to tamper")
	}
	writeFile(t, policy, tampered)
	if so, _, c := run(t, "test", out); c == 0 {
		t.Fatalf("tampered policy still passed its tests — vectors are not live:\n%s", so)
	}
}

// TestJourney_InitTestTamper proves the accessible onboarding path: init a
// no-toolchain JSON policy, its scaffolded tests pass through the binary, and a
// tamper makes them fail closed with a non-zero exit.
func TestJourney_InitTestTamper(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	if so, se, c := run(t, "init", "--authoring", "json", "--template", initTemplateKey(t), dir); c != 0 {
		t.Fatalf("init exit %d\n%s\n%s", c, so, se)
	}
	if so, se, c := run(t, "test", dir); c != 0 {
		t.Fatalf("scaffolded tests did not pass (exit %d):\n%s\n%s", c, so, se)
	}
	policy := filepath.Join(dir, "policy.json")
	raw, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.ReplaceAll(raw, []byte(`"effect":"deny"`), []byte(`"effect":"allow"`))
	if bytes.Equal(tampered, raw) {
		t.Skip("scaffold template has no deny rule to tamper")
	}
	writeFile(t, policy, tampered)
	if so, _, c := run(t, "test", dir); c == 0 {
		t.Fatalf("tampered scaffold still passed:\n%s", so)
	}
}

// TestJourney_PublishRoundTrip proves broker fidelity at the CLI: a hash-bound
// envelope lands the byte-exact target with exit 0, and an envelope not bound to
// the staged bytes fails closed (non-zero exit, target never created).
func TestJourney_PublishRoundTrip(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	writeFile(t, policy, exclusivePublishPolicy(t))

	staged := filepath.Join(dir, "stage", "result.json")
	stagedBytes := []byte(`{"ok":true}`)
	writeFile(t, staged, stagedBytes)

	// Happy path: envelope bound to the staged bytes, correlated to the run.
	good := filepath.Join(dir, "envelope.json")
	writeFile(t, good, envelope("deltawire.supervision.receipt.v1", "run1", "released", ir.HashBytes(stagedBytes)))
	target := filepath.Join(dir, "out", "result.json")
	pub := filepath.Join(dir, "pub.json")
	writeFile(t, pub, publishRequest("run1", "pub-1", "publisher", "repo://out/result.json", staged, target, good))
	if so, se, c := run(t, "publish", policy, pub); c != 0 {
		t.Fatalf("verified publish failed (exit %d):\n%s\n%s", c, so, se)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("publish did not create the target: %v", err)
	}
	if !bytes.Equal(got, stagedBytes) {
		t.Fatalf("published bytes differ from staged: %q", got)
	}

	// Fail-closed: an envelope carrying a wrong artifact hash must not publish.
	bad := filepath.Join(dir, "envelope-unbound.json")
	writeFile(t, bad, envelope("deltawire.supervision.receipt.v1", "run1", "released", ir.HashBytes([]byte("wrong"))))
	badTarget := filepath.Join(dir, "out", "unbound.json")
	badPub := filepath.Join(dir, "pub-unbound.json")
	writeFile(t, badPub, publishRequest("run1", "pub-2", "publisher", "repo://out/result.json", staged, badTarget, bad))
	if _, _, c := run(t, "publish", policy, badPub); c == 0 {
		t.Fatal("publish accepted an envelope not bound to the staged bytes")
	}
	if _, err := os.Stat(badTarget); !os.IsNotExist(err) {
		t.Fatal("fail-closed publish created a target")
	}
}

// TestJourney_SimulateReplay proves chain integrity: a request stream simulates
// into a receipt chain that replays clean under its policy, and replay against a
// mutated policy identity fails closed.
func TestJourney_SimulateReplay(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	writeFile(t, policy, exclusivePublishPolicy(t))

	reqs := filepath.Join(dir, "reqs.jsonl")
	writeFile(t, reqs, []byte(strings.Join([]string{
		`{"protocol":"interlock.effect.v1","request_id":"s-1","run_id":"sim","actor":"agent","operation":"filesystem.write","resource":{"kind":"tree","uri":"repo://work/a.txt"}}`,
		`{"protocol":"interlock.effect.v1","request_id":"s-2","run_id":"sim","actor":"agent","operation":"artifact.publish","resource":{"kind":"file","uri":"repo://out/result.json"}}`,
		"",
	}, "\n")))
	receipts := filepath.Join(dir, "receipts.jsonl")
	if so, se, c := run(t, "simulate", policy, reqs, "sim", "-o", receipts); c != 0 {
		t.Fatalf("simulate failed (exit %d):\n%s\n%s", c, so, se)
	}
	if so, se, c := run(t, "replay", policy, reqs, receipts); c != 0 {
		t.Fatalf("replay of a fresh chain failed (exit %d):\n%s\n%s", c, so, se)
	}

	// Mutate the policy identity: the same chain must no longer verify.
	mutated := filepath.Join(dir, "policy-mutated.json")
	raw, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, mutated, bytes.ReplaceAll(raw, []byte(`"exclusive-publish.v1"`), []byte(`"exclusive-publish.v1.tampered"`)))
	if _, _, c := run(t, "replay", mutated, reqs, receipts); c == 0 {
		t.Fatal("replay accepted a chain under a mutated policy identity")
	}
}

// TestSmoke_InfoCommands proves the read-only surface runs and exits 0 with the
// expected framing. These are the commands an installer runs to prove the artifact
// works right after install.
func TestSmoke_InfoCommands(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	writeFile(t, policy, exclusivePublishPolicy(t))

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"version", []string{"version"}, "interlock"},
		{"doctor", []string{"doctor"}, "interlock doctor"},
		{"demo", []string{"demo"}, "interlock demo"},
		{"explain", []string{"explain", policy}, "exclusive-publish.v1"},
		{"check", []string{"check", policy}, "hash="},
		{"verify", []string{"verify", "--format", "json"}, "claim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			so, se, c := run(t, tc.args...)
			if c != 0 {
				t.Fatalf("%s exit %d\n%s\n%s", tc.name, c, so, se)
			}
			if !strings.Contains(so, tc.want) {
				t.Fatalf("%s output missing %q:\n%s", tc.name, tc.want, so)
			}
		})
	}
}

// --- shared fixture builders --------------------------------------------------

// exclusivePublishPolicy returns the frozen exclusive-publish canonical policy
// from the conformance corpus — the same artifact the broker vectors use.
func exclusivePublishPolicy(t *testing.T) []byte {
	t.Helper()
	b := corpusPolicy(t, "policies/exclusive-publish.json")
	return b
}

// envelope renders an upstream evidence envelope in the broker's on-disk shape.
func envelope(schema, runID, status, artifactHash string) []byte {
	e := map[string]string{
		"schema":          schema,
		"run_id":          runID,
		"status":          status,
		"artifact_sha256": artifactHash,
	}
	b, _ := json.Marshal(e)
	return b
}

// publishRequest renders a broker.PublishRequest JSON document.
func publishRequest(runID, reqID, actor, resourceURI, staged, target, envPath string) []byte {
	pr := broker.PublishRequest{
		RunID:       runID,
		RequestID:   reqID,
		Actor:       actor,
		ResourceURI: resourceURI,
		Kind:        ir.KindFile,
		StagedPath:  staged,
		TargetPath:  target,
		Upstream:    []broker.UpstreamReceipt{{Path: envPath}},
	}
	b, _ := json.Marshal(pr)
	return b
}

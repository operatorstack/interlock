package proof

import "testing"

// TestProofAllPass asserts every release-proof check returns OK. This keeps the
// proof honest: if the engine, broker, receipt chain, or conformance vectors
// regress, `go test` fails here — not just the `interlock verify` command.
func TestProofAllPass(t *testing.T) {
	results := Run()
	if len(results) == 0 {
		t.Fatal("proof.Run returned no results")
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("FAIL  %s: %s", r.Claim, r.Detail)
			continue
		}
		t.Logf("PASS  %s: %s", r.Claim, r.Detail)
	}
}

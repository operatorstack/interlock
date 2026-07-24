package scaffold

import (
	"testing"

	"github.com/operatorstack/interlock/engine"
)

// TestTemplatesSelfProve compiles every starter template and re-decides every one
// of its vectors through the real engine, asserting the promised outcome. This is
// the guarantee behind `interlock init` → `interlock test`: a freshly scaffolded
// project is green on day one, and the PASS lines a user sees are real decisions —
// not a hand-maintained table that could drift from the policy.
func TestTemplatesSelfProve(t *testing.T) {
	// The custom template is parameterized; prove it with a non-default path too.
	paths := map[string]string{"custom": "repo://docs/**"}

	for _, tmpl := range Templates() {
		tmpl := tmpl
		t.Run(tmpl.Key, func(t *testing.T) {
			path := paths[tmpl.Key]

			pol, err := tmpl.build(path).Compile()
			if err != nil {
				t.Fatalf("template %q does not compile: %v", tmpl.Key, err)
			}

			vectors := tmpl.Vectors(path)
			if len(vectors) == 0 {
				t.Fatalf("template %q has no vectors", tmpl.Key)
			}

			for _, v := range vectors {
				req := v.Request
				if v.UsePolicyHash {
					h, err := pol.Hash()
					if err != nil {
						t.Fatalf("hashing policy for %q: %v", v.Name, err)
					}
					req.ClaimedPolicyHash = h
				}
				d := engine.Decide(pol, req)
				if d.Outcome != v.Expect {
					t.Errorf("%s / %q: outcome = %q, want %q", tmpl.Key, v.Name, d.Outcome, v.Expect)
				}
				if v.ExpectRuleID != "" && d.RuleID != v.ExpectRuleID {
					t.Errorf("%s / %q: rule = %q, want %q", tmpl.Key, v.Name, d.RuleID, v.ExpectRuleID)
				}
			}
		})
	}
}

// TestDemosSelfProve compiles every built-in demo and re-decides its narrated
// scenarios through the real engine. This is the guarantee behind `interlock
// demo`: the outcomes a just-installed binary prints are live decisions, so the
// showcase can never drift from the engine it is meant to demonstrate.
func TestDemosSelfProve(t *testing.T) {
	demos := Demos()
	if len(demos) == 0 {
		t.Fatal("no demos registered")
	}
	for _, d := range demos {
		d := d
		t.Run(d.Key, func(t *testing.T) {
			pol, err := d.build("").Compile()
			if err != nil {
				t.Fatalf("demo %q does not compile: %v", d.Key, err)
			}
			vectors := d.Vectors("")
			if len(vectors) == 0 {
				t.Fatalf("demo %q has no scenarios", d.Key)
			}
			for _, v := range vectors {
				req := v.Request
				if v.UsePolicyHash {
					h, err := pol.Hash()
					if err != nil {
						t.Fatalf("hashing policy for %q: %v", v.Name, err)
					}
					req.ClaimedPolicyHash = h
				}
				dec := engine.Decide(pol, req)
				if dec.Outcome != v.Expect {
					t.Errorf("%s / %q: outcome = %q, want %q", d.Key, v.Name, dec.Outcome, v.Expect)
				}
				if v.ExpectRuleID != "" && dec.RuleID != v.ExpectRuleID {
					t.Errorf("%s / %q: rule = %q, want %q", d.Key, v.Name, dec.RuleID, v.ExpectRuleID)
				}
			}
		})
	}
}

package conformance

import (
	"testing"

	"github.com/operatorstack/interlock/engine"
)

func run(t *testing.T, cases []Case) {
	t.Helper()
	if len(cases) == 0 {
		t.Fatal("no conformance cases loaded")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			req := c.Request
			if c.UsePolicyHash {
				h, err := c.Policy.Hash()
				if err != nil {
					t.Fatal(err)
				}
				req.ClaimedPolicyHash = h
			}
			d := engine.Decide(c.Policy, req)
			if d.Outcome != c.Expect {
				t.Fatalf("%s: outcome = %s, want %s (reason: %s)", c.Name, d.Outcome, c.Expect, d.Reason)
			}
			if c.ExpectRuleID != "" && d.RuleID != c.ExpectRuleID {
				t.Fatalf("%s: rule = %q, want %q", c.Name, d.RuleID, c.ExpectRuleID)
			}
		})
	}
}

func TestPositive(t *testing.T) {
	cases, err := Positive()
	if err != nil {
		t.Fatal(err)
	}
	run(t, cases)
}

func TestNegative(t *testing.T) {
	cases, err := Negative()
	if err != nil {
		t.Fatal(err)
	}
	run(t, cases)
}

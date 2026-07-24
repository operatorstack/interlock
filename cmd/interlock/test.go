package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/scaffold"
)

// cmdTest re-decides every vector in <dir>/tests.jsonl against <dir>/policy.json
// through the real engine and prints a PASS/FAIL ladder. This is the "does my
// policy do what I meant?" command: the PASS lines are live decisions, so editing
// policy.json and re-running immediately shows the effect (and turns a broken edit
// red). It needs no Go toolchain.
func cmdTest(args []string) error {
	dir := defaultInterlockDir
	format := "text"
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--format":
			if i+1 >= len(args) {
				return fmt.Errorf("test: --format wants text|json")
			}
			format = args[i+1]
			i += 2
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return fmt.Errorf("test: unexpected flag %q", args[i])
			}
			dir = args[i]
			i++
		}
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("test: unknown --format %q (want text|json)", format)
	}

	pol, err := loadPolicy(filepath.Join(dir, "policy.json"))
	if err != nil {
		return fmt.Errorf("test: loading policy: %w", err)
	}

	var vectors []scaffold.Vector
	if err := loadJSONL(filepath.Join(dir, "tests.jsonl"), func(b []byte) error {
		// tests.jsonl may carry '#' comment lines (loadJSONL only skips blanks).
		if len(b) > 0 && b[0] == '#' {
			return nil
		}
		var v scaffold.Vector
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		vectors = append(vectors, v)
		return nil
	}); err != nil {
		return fmt.Errorf("test: loading vectors: %w", err)
	}
	if len(vectors) == 0 {
		return fmt.Errorf("test: %s has no vectors", filepath.Join(dir, "tests.jsonl"))
	}

	type result struct {
		Name   string           `json:"name"`
		Expect protocol.Outcome `json:"expect"`
		Got    protocol.Outcome `json:"got"`
		Rule   string           `json:"rule,omitempty"`
		OK     bool             `json:"ok"`
	}
	results := make([]result, 0, len(vectors))
	passed := 0
	for _, v := range vectors {
		reqst := v.Request
		if v.UsePolicyHash {
			h, herr := pol.Hash()
			if herr != nil {
				return herr
			}
			reqst.ClaimedPolicyHash = h
		}
		d := engine.Decide(pol, reqst)
		ok := d.Outcome == v.Expect && (v.ExpectRuleID == "" || d.RuleID == v.ExpectRuleID)
		if ok {
			passed++
		}
		results = append(results, result{Name: v.Name, Expect: v.Expect, Got: d.Outcome, Rule: d.RuleID, OK: ok})
	}

	if format == "json" {
		out := struct {
			Results []result `json:"results"`
			Result  string   `json:"result"`
			Passed  int      `json:"passed"`
			Total   int      `json:"total"`
		}{results, passLabel(passed == len(vectors)), passed, len(vectors)}
		if err := printJSON(out); err != nil {
			return err
		}
		if passed != len(vectors) {
			os.Exit(1)
		}
		return nil
	}

	for _, r := range results {
		if r.OK {
			fmt.Printf("PASS %s\n", r.Name)
			continue
		}
		if r.Expect != r.Got {
			fmt.Printf("FAIL %s (expected %s, got %s)\n", r.Name, r.Expect, r.Got)
		} else {
			fmt.Printf("FAIL %s (fired rule %q)\n", r.Name, r.Rule)
		}
	}
	fmt.Printf("\nRESULT: %s  (%d/%d)\n", passLabel(passed == len(vectors)), passed, len(vectors))
	if passed != len(vectors) {
		os.Exit(1)
	}
	return nil
}

func passLabel(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

package main

import (
	"fmt"
	"strings"

	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/scaffold"
)

// cmdDemo narrates a built-in policy showcase: it compiles the named demo policy
// in-process and runs a scripted sequence of effect requests through the real
// engine, printing each outcome. It needs no Go toolchain and touches no files —
// it is the "prove it works" moment the installers run right after install.
func cmdDemo(args []string) error {
	name := "repository-policy"
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--list", "-l":
			for _, d := range scaffold.Demos() {
				fmt.Printf("%-20s %s\n", d.Key, d.Title)
			}
			return nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("demo: unexpected flag %q", args[i])
			}
			name = args[i]
			i++
		}
	}

	d, ok := scaffold.DemoByKey(name)
	if !ok {
		return fmt.Errorf("demo: unknown demo %q (want %s)", name, strings.Join(scaffold.DemoKeys(), "|"))
	}

	pol, err := loadDemoPolicy(d)
	if err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	h, err := pol.Hash()
	if err != nil {
		return err
	}

	fmt.Printf("interlock demo — %s\n\n", d.Title)
	fmt.Printf("%s\n\n", d.Summary)
	fmt.Printf("policy %s  (%s)\n\n", pol.PolicyID, h)
	fmt.Println("Rules (first match wins):")
	for _, r := range d.Rules("") {
		fmt.Printf("  - %s\n", stripTicks(r))
	}
	fmt.Println()
	fmt.Println("Decisions (each line is a live engine.Decide result):")

	for _, v := range d.Vectors("") {
		reqst := v.Request
		if v.UsePolicyHash {
			reqst.ClaimedPolicyHash = h
		}
		dec := engine.Decide(pol, reqst)
		rule := dec.RuleID
		if rule == "" {
			rule = "default-deny"
		}
		fmt.Printf("  %-40s %-8s (%s) — %s\n", v.Name, strings.ToUpper(string(dec.Outcome)), rule, dec.Reason)
	}
	fmt.Println()
	fmt.Println("Author your own with `interlock init`, then `interlock test`.")
	return nil
}

// loadDemoPolicy compiles a demo's in-process builder to canonical IR and
// decodes it back through the CLI's own decode path. Round-tripping the bytes
// keeps the demo honest: the same compiler that backs `interlock compile`
// produces the policy the engine then decides against.
func loadDemoPolicy(d scaffold.Demo) (ir.Policy, error) {
	b, err := d.Policy("")
	if err != nil {
		return ir.Policy{}, err
	}
	return decodePolicy(b)
}

// stripTicks removes the markdown backticks the README rules carry so they read
// cleanly in a terminal.
func stripTicks(s string) string { return strings.ReplaceAll(s, "`", "") }

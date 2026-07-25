package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/operatorstack/interlock/derive"
)

const defaultDerivedDir = ".interlock/derived"

// cmdDerive is the thin CLI for `interlock derive`: it owns flag parsing and file
// I/O only. All classification and grounding live in the derive package, which
// writes nothing. The command never activates policy — it writes a reviewable
// candidate under --output and prints the "not enforced" framing.
func cmdDerive(args []string) error {
	repo := "."
	outDir := defaultDerivedDir
	format := "text"
	force := false
	nonInteractive := false
	review := false
	var from []string
	var positional []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--from":
			if i+1 >= len(args) {
				return fmt.Errorf("derive: --from wants a path")
			}
			from = append(from, args[i+1])
			i += 2
		case "--output", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("derive: --output wants a directory")
			}
			outDir = args[i+1]
			i += 2
		case "--format":
			if i+1 >= len(args) {
				return fmt.Errorf("derive: --format wants text|json")
			}
			format = args[i+1]
			i += 2
		case "--force", "-f":
			force = true
			i++
		case "--non-interactive":
			nonInteractive = true
			i++
		case "--review":
			review = true
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("derive: unexpected flag %q", args[i])
			}
			positional = append(positional, args[i])
			i++
		}
	}
	if len(positional) > 1 {
		return fmt.Errorf("derive: want at most one repository path")
	}
	if len(positional) == 1 {
		repo = positional[0]
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("derive: --format wants text|json, got %q", format)
	}
	// Refuse to point --output at an active policy file (defense in depth; derive
	// never writes a file named policy.json regardless).
	if filepath.Base(outDir) == "policy.json" {
		return fmt.Errorf("derive: --output must be a directory, not policy.json")
	}

	if review {
		return runReview(outDir, format, nonInteractive)
	}

	res, err := derive.Derive(repo, from)
	if err != nil {
		return err
	}
	if err := writeResult(res, outDir, force); err != nil {
		return err
	}
	return report(res, outDir, format)
}

// runReview re-opens an existing candidate, walks its unresolved questions, and
// rewrites the candidate with the answers applied. It never activates policy.
func runReview(outDir, format string, nonInteractive bool) error {
	if nonInteractive {
		return fmt.Errorf("derive --review needs interactive stdin (omit --non-interactive)")
	}
	raw, err := os.ReadFile(filepath.Join(outDir, derive.FileDerivation))
	if err != nil {
		return fmt.Errorf("derive --review: reading %s: %w (run `interlock derive` first)", derive.FileDerivation, err)
	}
	d, err := derive.DecodeDerivation(raw)
	if err != nil {
		return err
	}
	// Rebuild once to learn the freeze state (whether to ask the baseline question).
	current, err := derive.Rebuild(d)
	if err != nil {
		return err
	}

	answers := promptAnswers(d, current.Candidate.FreezeWarning)
	updated := derive.ApplyAnswers(d, answers)
	res, err := derive.Rebuild(updated)
	if err != nil {
		return err
	}
	if err := writeResult(res, outDir, true); err != nil {
		return err
	}
	return report(res, outDir, format)
}

// promptAnswers reads one answer per unresolved question from stdin, plus the
// baseline question when the candidate is deny-only. Blank input skips a question.
func promptAnswers(d derive.Derivation, freeze bool) map[string]string {
	r := bufio.NewReader(os.Stdin)
	answers := map[string]string{}
	fmt.Println("Answer each question to ground it into the candidate. Press Enter to skip.")
	fmt.Println()
	for _, rec := range d.Records {
		if rec.Status != derive.StatusUnresolved {
			continue
		}
		fmt.Printf("[%s] %s:%d\n  %q\n  %s\n", rec.ID, rec.Source.Path, rec.Source.LineStart, rec.Excerpt, rec.Question)
		fmt.Print("  answer> ")
		line, _ := r.ReadString('\n')
		answers[rec.ID] = strings.TrimSpace(line)
		fmt.Println()
	}
	if freeze {
		fmt.Println("[baseline] The candidate only denies; under default-deny that blocks everything.")
		fmt.Println("  What baseline should the agent be allowed to read/write? (e.g. repo://src/**)")
		fmt.Print("  answer> ")
		line, _ := r.ReadString('\n')
		answers["baseline"] = strings.TrimSpace(line)
		fmt.Println()
	}
	return answers
}

// writeResult writes all candidate artifacts atomically: it renders every file
// first, guards against overwrite, then writes. A render error leaves the output
// dir untouched (failure-state invariant). It never writes policy.json.
func writeResult(res derive.Result, outDir string, force bool) error {
	files, err := res.Files()
	if err != nil {
		return err
	}
	policyPath := filepath.Join(outDir, derive.FileCandidatePolicy)
	if _, err := os.Stat(policyPath); err == nil && !force {
		return fmt.Errorf("derive: %s already exists (use --force to overwrite, or --review to refine)", policyPath)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// Stable write order for deterministic output.
	for _, name := range []string{
		derive.FileCandidatePolicy, derive.FileCandidateTests,
		derive.FileDerivation, derive.FileQuestions, derive.FileReadme,
	} {
		if err := os.WriteFile(filepath.Join(outDir, name), files[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// report prints the closing summary. The text form leads with the "not enforced"
// framing so no one mistakes a candidate for an active policy.
func report(res derive.Result, outDir, format string) error {
	proposed, unresolved, rejected := 0, 0, 0
	for _, r := range res.Derivation.Records {
		switch r.Status {
		case derive.StatusProposed:
			proposed++
		case derive.StatusUnresolved:
			unresolved++
		case derive.StatusRejected:
			rejected++
		}
	}
	if format == "json" {
		return printJSON(map[string]any{
			"output":     outDir,
			"policy_id":  res.Derivation.PolicyID,
			"rules":      res.Candidate.RuleCount,
			"proposed":   proposed,
			"unresolved": unresolved,
			"rejected":   rejected,
			"freeze":     res.Candidate.FreezeWarning,
			"enforced":   false,
		})
	}
	fmt.Printf("Reviewed your repository and drafted a candidate Interlock policy.\n\n")
	fmt.Printf("  %s/\n", outDir)
	fmt.Printf("    %-22s %d proposed rule(s) — interlock.spec.v1\n", derive.FileCandidatePolicy, res.Candidate.RuleCount)
	fmt.Printf("    %-22s test vectors for each rule\n", derive.FileCandidateTests)
	fmt.Printf("    %-22s provenance for every record\n", derive.FileDerivation)
	fmt.Printf("    %-22s %d unresolved question(s)\n", derive.FileQuestions, unresolved)
	if rejected > 0 {
		fmt.Printf("\n  %d record(s) were rejected (conflict or would weaken an existing policy) — see %s.\n", rejected, derive.FileDerivation)
	}
	fmt.Printf("\nThese enforceable rules appear to be implied by your repository. Review and approve them.\n")
	fmt.Printf("Nothing is enforced yet. To activate after review:\n")
	fmt.Printf("  interlock derive --review\n")
	fmt.Printf("  interlock compile %s -o .interlock/policy.json && interlock test\n", filepath.Join(outDir, derive.FileCandidatePolicy))
	return nil
}

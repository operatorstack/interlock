package derive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// report.go renders the human-facing artifacts and decodes derivation.json for
// --review. The framing is load-bearing: every artifact says "candidate, not
// enforced" so the control law is visible to the reader, not just the code.

// encodeDerivation renders the typed derivation record as indented JSON with a
// trailing newline (diff-friendly authoring form, like spec.Encode).
func encodeDerivation(d Derivation) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		return nil, fmt.Errorf("interlock/derive: encode derivation: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeDerivation parses derivation.json, rejecting a wrong schema tag and any
// unknown field (fail closed, mirroring spec.Decode). This is the --review entry.
func decodeDerivation(data []byte) (Derivation, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d Derivation
	if err := dec.Decode(&d); err != nil {
		return Derivation{}, fmt.Errorf("interlock/derive: decode derivation: %w", err)
	}
	if d.Schema != DerivationSchema {
		return Derivation{}, fmt.Errorf("interlock/derive: unexpected schema %q, want %q", d.Schema, DerivationSchema)
	}
	return d, nil
}

// renderQuestions renders QUESTIONS.md: one entry per unresolved record, plus the
// baseline-freeze question when the candidate is deny-only. Each entry names the
// record id so --review (and a human) can map an answer back to it.
func renderQuestions(d Derivation, freeze bool) []byte {
	var b strings.Builder
	b.WriteString("# Unresolved questions\n\n")
	b.WriteString("These are decisions `interlock derive` could not make for you without guessing. ")
	b.WriteString("Nothing here is enforced. Answer them with `interlock derive --review` (or edit ")
	b.WriteString("`candidate.policy.json` directly), then compile the candidate to activate it.\n\n")

	any := false
	for _, r := range d.Records {
		if r.Status != StatusUnresolved {
			continue
		}
		any = true
		fmt.Fprintf(&b, "## %s\n\n", r.ID)
		fmt.Fprintf(&b, "- Source: `%s:%d`\n", r.Source.Path, r.Source.LineStart)
		fmt.Fprintf(&b, "- Excerpt: %q\n", r.Excerpt)
		fmt.Fprintf(&b, "- Classification: `%s`\n", r.Class)
		if len(r.Missing) > 0 {
			fmt.Fprintf(&b, "- Missing: %s\n", strings.Join(r.Missing, ", "))
		}
		fmt.Fprintf(&b, "- Question: %s\n\n", r.Question)
	}

	if freeze {
		any = true
		b.WriteString("## baseline\n\n")
		b.WriteString("- Question: The candidate only *denies*. Under Interlock's default-deny, every\n")
		b.WriteString("  other request is also blocked, which would freeze the repository. What baseline\n")
		b.WriteString("  should the agent be allowed to do (e.g. read/write `repo://src/**`)? Answering\n")
		b.WriteString("  adds a grounded allow rule; leaving it unanswered keeps the candidate deny-only.\n\n")
	}

	if !any {
		b.WriteString("_None — every classified statement was either grounded into the candidate or is advisory._\n")
	}
	return []byte(b.String())
}

// renderReadme renders README.md: what this directory is, the "not enforced"
// framing, and the exact promotion command through the real compiler.
func renderReadme(d Derivation, c Candidate) []byte {
	var b strings.Builder
	b.WriteString("# Derived Interlock policy (candidate — NOT enforced)\n\n")
	b.WriteString("`interlock derive` read your repository's existing instructions and produced the\n")
	b.WriteString("enforceable rules they appear to imply. **This is a proposal for you to review, not\n")
	b.WriteString("an active policy.** Nothing here changes what your agent can do until you compile it.\n\n")

	proposed, unresolved, rejected := countByStatus(d)
	b.WriteString("## What's here\n\n")
	fmt.Fprintf(&b, "- `candidate.policy.json` — %d proposed rule(s) as `interlock.spec.v1`\n", c.RuleCount)
	b.WriteString("- `candidate.tests.jsonl` — a blocking + allowed test vector for each rule\n")
	fmt.Fprintf(&b, "- `derivation.json` — every record with source provenance (%d proposed, %d unresolved, %d rejected)\n", proposed, unresolved, rejected)
	b.WriteString("- `QUESTIONS.md` — decisions we would not guess\n\n")

	if c.FreezeWarning {
		b.WriteString("> ⚠️ The candidate only denies. Under default-deny that blocks everything else —\n")
		b.WriteString("> see the `baseline` question in `QUESTIONS.md` before compiling.\n\n")
	}

	b.WriteString("## Review, then enforce\n\n")
	b.WriteString("1. Read `derivation.json` — every rule cites the source line it came from.\n")
	b.WriteString("2. Answer `QUESTIONS.md`: `interlock derive --review`.\n")
	b.WriteString("3. Compile the candidate through the real compiler and run its tests:\n\n")
	b.WriteString("   ```\n")
	b.WriteString("   interlock compile .interlock/derived/candidate.policy.json -o .interlock/policy.json\n")
	b.WriteString("   interlock test\n")
	b.WriteString("   ```\n\n")
	b.WriteString("Only step 3 activates anything, and only because *you* ran the compiler.\n")
	return []byte(b.String())
}

func countByStatus(d Derivation) (proposed, unresolved, rejected int) {
	for _, r := range d.Records {
		switch r.Status {
		case StatusProposed:
			proposed++
		case StatusUnresolved:
			unresolved++
		case StatusRejected:
			rejected++
		}
	}
	return
}

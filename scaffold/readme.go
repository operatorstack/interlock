package scaffold

import (
	"strings"
)

// renderReadme produces the .interlock/README.md a user gets from `init`. It
// explains what the policy protects, lists the rules in plain English, shows the
// checked-in test expectations, and points at both the declarative and Go
// authoring paths.
func renderReadme(t Template, path string) string {
	var b strings.Builder
	b.WriteString("# Interlock: " + t.Title + "\n\n")
	b.WriteString(t.Summary + "\n\n")

	b.WriteString("This directory is a no-toolchain Interlock setup:\n\n")
	b.WriteString("- `policy.json` — the policy authored as `interlock.spec.v1` (validated and lowered to a hashable, first-match decision table by the compiler).\n")
	b.WriteString("- `tests.jsonl` — one effect request per line with the outcome it must produce.\n")
	b.WriteString("- `README.md` — this file.\n\n")

	b.WriteString("## What it protects\n\n")
	for _, r := range t.rules(path) {
		b.WriteString("- " + r + "\n")
	}
	b.WriteString("\n")

	b.WriteString("## Run\n\n")
	b.WriteString("```\ninterlock test\n```\n\n")
	b.WriteString("Each line is decided by the real engine against `policy.json`:\n\n")
	for _, v := range t.Vectors(path) {
		b.WriteString("- **" + string(v.Expect) + "** — " + v.Name + "\n")
	}
	b.WriteString("\n")

	b.WriteString("## Edit it\n\n")
	b.WriteString("Edit `policy.json` (an `interlock.spec.v1` document) directly, then add or adjust vectors in `tests.jsonl` and re-run `interlock test`. Structural mistakes (unknown actor, unreachable rule) are reported by the compiler when you run it.\n")
	b.WriteString("A vector is `{\"name\": \"...\", \"request\": {...}, \"expect\": \"allow|deny|require\"}`; add `\"expect_rule_id\"` to also assert which rule fired.\n\n")

	b.WriteString("## Prefer to author in Go?\n\n")
	b.WriteString("`interlock init --authoring go <dir>` scaffolds a Go policy module instead. Go lets you use loops, helpers, tables, and reusable packages at construction time; it emits the **same** `interlock.spec.v1`, compiles to the **same** canonical IR, the **same** policy hash, and is decided by the **same** engine. Run `interlock compile <dir> -o policy.json` (needs a Go toolchain) to produce the JSON, or author the spec directly here with no toolchain at all.\n")

	return b.String()
}

package derive

import (
	"encoding/json"
	"strings"
)

// adapters.go holds the deterministic V1 source parsers. Each is a pure function
// from (path, content) to raw statements. They deliberately do NOT classify or
// ground — a prose adapter just yields lines and lets classify.go read the
// language; a machine-config adapter yields a Suggest hint + a Note describing the
// decision a human must make, so structured config becomes a question rather than
// a silently widened rule.

// parseProse emits one statement per non-empty content line. It handles all
// free-text authority sources (AGENTS.md, CLAUDE.md, .cursor/rules/*,
// SKILL.md). Prose is weak evidence: only lines with an explicit imperative
// marker survive classification into an emittable rule.
func parseProse(path string, content []byte) []RawStatement {
	var out []RawStatement
	inFence := false
	for i, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" {
			continue
		}
		excerpt := cleanExcerpt(line)
		if excerpt == "" {
			continue
		}
		out = append(out, RawStatement{
			Path:      path,
			LineStart: i + 1,
			LineEnd:   i + 1,
			Text:      excerpt,
			Strength:  StrengthWeak,
		})
	}
	return out
}

// parseCodeowners reads a CODEOWNERS file. Ownership is NOT an Interlock approval
// gate — treating it as one would be semantic widening — so each entry becomes an
// unresolved suggestion (Suggest=ClassUnresolved) carrying the owners in Note.
func parseCodeowners(path string, content []byte) []RawStatement {
	var out []RawStatement
	for i, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		pattern, owners := fields[0], strings.Join(fields[1:], " ")
		out = append(out, RawStatement{
			Path:      path,
			LineStart: i + 1,
			LineEnd:   i + 1,
			Text:      "changes under `" + pattern + "` are owned by " + owners,
			Strength:  StrengthStrong,
			Suggest:   ClassUnresolved,
			Note: "CODEOWNERS assigns " + owners + " to `" + pattern + "`. Ownership is not the same as an enforced approval gate. " +
				"Should writes under `" + pattern + "` require human approval?",
		})
	}
	return out
}

// parseWorkflow scans a GitHub Actions workflow for job/step names that read like
// required checks. A CI check *existing* does not tell derive which receipt
// schema/status counts as evidence, so each becomes an unresolved verification
// question rather than a receipt requirement invented from nothing (invariant 5).
func parseWorkflow(path string, content []byte) []RawStatement {
	var out []RawStatement
	for i, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		// Match "name: <something with test/lint/build>" job or step names.
		if !strings.HasPrefix(lower, "name:") && !strings.HasPrefix(lower, "- name:") {
			continue
		}
		if !containsWord(lower, "test", "lint", "build", "check", "verify", "ci") {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(trimmed, ":", 2)[1])
		name = strings.Trim(name, `"'`)
		if name == "" {
			continue
		}
		out = append(out, RawStatement{
			Path:      path,
			LineStart: i + 1,
			LineEnd:   i + 1,
			Text:      "CI defines a check named " + name,
			Strength:  StrengthStrong,
			Suggest:   ClassUnresolved,
			Note: "The workflow defines a check named " + name + ". If a passing " + name +
				" run should be required evidence before an effect, name the receipt schema and status it produces",
		})
	}
	return out
}

// parsePackageScripts reads package.json scripts. A "test"/"publish" script is
// evidence a project *has* those operations, but not what must gate them, so each
// is an unresolved suggestion.
func parsePackageScripts(path string, content []byte) []RawStatement {
	var doc struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil
	}
	// Deterministic order over map keys.
	names := make([]string, 0, len(doc.Scripts))
	for k := range doc.Scripts {
		names = append(names, k)
	}
	sortStrings(names)

	var out []RawStatement
	for _, name := range names {
		lower := strings.ToLower(name)
		if !containsWord(lower, "test", "publish", "release", "deploy", "lint") {
			continue
		}
		out = append(out, RawStatement{
			Path:      path,
			LineStart: 1,
			LineEnd:   1,
			Text:      "package.json defines a `" + name + "` script",
			Strength:  StrengthStrong,
			Suggest:   ClassUnresolved,
			Note: "package.json defines a `" + name + "` script. Should any effect be gated on it? " +
				"If so, name the operation, resource, and evidence",
		})
	}
	return out
}

// parseMakefile reads Makefile targets, applying the same "operation exists,
// gate unknown" treatment as package scripts.
func parseMakefile(path string, content []byte) []RawStatement {
	var out []RawStatement
	for i, line := range splitLines(content) {
		// A target line is "name:" at column 0 (not indented, not a variable).
		if line == "" || line[0] == '\t' || line[0] == ' ' || line[0] == '#' {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		lower := strings.ToLower(name)
		if strings.ContainsAny(name, " =") || !containsWord(lower, "test", "publish", "release", "deploy", "lint", "build") {
			continue
		}
		out = append(out, RawStatement{
			Path:      path,
			LineStart: i + 1,
			LineEnd:   i + 1,
			Text:      "Makefile defines a `" + name + "` target",
			Strength:  StrengthStrong,
			Suggest:   ClassUnresolved,
			Note: "The Makefile defines a `" + name + "` target. Should any effect be gated on it? " +
				"If so, name the operation, resource, and evidence",
		})
	}
	return out
}

// splitLines splits content into lines without a trailing empty element from a
// final newline, so line numbers are stable.
func splitLines(content []byte) []string {
	s := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// sortStrings is a tiny local sort to avoid importing sort here (discover.go
// already imports it; keep this file's imports minimal and its behavior obvious).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

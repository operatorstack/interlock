package derive

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// discover.go resolves which sources to read and dispatches each to its adapter.
// Discovery is deterministic: the candidate path set is fixed and each glob is
// sorted, so a given repository always yields the same statements in the same
// order regardless of filesystem enumeration order.

// adapter parses one source file's bytes into raw statements. Adapters never
// touch the filesystem themselves (discover reads the bytes) so they are pure and
// unit-testable.
type adapter func(path string, content []byte) []RawStatement

// sourceRef is a resolved source: an absolute path and the adapter that parses it.
type sourceRef struct {
	path    string
	relPath string
	parse   adapter
}

// discover returns the ordered source set. When from is non-empty it is the
// explicit, user-chosen set (each dispatched by filename); otherwise the default
// V1 set is auto-discovered under root. Order is stable and sorted by relPath.
func discover(root string, from []string) ([]sourceRef, error) {
	var refs []sourceRef
	seen := map[string]bool{}

	add := func(abs string) {
		abs = filepath.Clean(abs)
		if seen[abs] {
			return
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return
		}
		rel, rerr := filepath.Rel(root, abs)
		if rerr != nil {
			rel = abs
		}
		refs = append(refs, sourceRef{path: abs, relPath: filepath.ToSlash(rel), parse: adapterFor(abs)})
		seen[abs] = true
	}

	if len(from) > 0 {
		for _, p := range from {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			add(p)
		}
	} else {
		for _, c := range defaultCandidates(root) {
			add(c)
		}
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].relPath < refs[j].relPath })
	return refs, nil
}

// defaultCandidates is the fixed V1 auto-discovery set, in a stable order. Globs
// are expanded and sorted so enumeration is deterministic.
func defaultCandidates(root string) []string {
	var out []string
	fixed := []string{
		"AGENTS.md", "CLAUDE.md",
		"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS",
		"package.json", "Makefile",
	}
	for _, f := range fixed {
		out = append(out, filepath.Join(root, filepath.FromSlash(f)))
	}
	globs := []string{
		".cursor/rules/*",
		".claude/skills/*/SKILL.md",
		".github/workflows/*.yml",
		".github/workflows/*.yaml",
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(g)))
		sort.Strings(matches)
		out = append(out, matches...)
	}
	return out
}

// adapterFor selects a parser by filename. Unknown files fall back to the prose
// adapter, which is safe: prose statements without an explicit imperative marker
// classify as advisory/domain and are dropped, never emitted.
func adapterFor(path string) adapter {
	base := filepath.Base(path)
	lower := strings.ToLower(base)
	switch {
	case base == "CODEOWNERS":
		return parseCodeowners
	case lower == "package.json":
		return parsePackageScripts
	case lower == "makefile":
		return parseMakefile
	case strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml"):
		if strings.Contains(filepath.ToSlash(path), "/.github/workflows/") {
			return parseWorkflow
		}
		return parseProse
	default:
		return parseProse
	}
}

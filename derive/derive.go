package derive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/operatorstack/interlock/ir"
)

// derive.go is the package entrypoint: it runs the full deterministic pipeline
// (discover → classify → ground → conflicts/weakening → candidate) and renders
// the output artifacts. The command layer (cmd/interlock/derive.go) only handles
// flags and file I/O; everything decision-shaped lives here and is pure given the
// repository bytes, so the whole pipeline is unit- and conformance-testable.

// Output file names. None is "policy.json": derive writes only candidates, never
// an active policy (invariant 8).
const (
	FileCandidatePolicy = "candidate.policy.json"
	FileCandidateTests  = "candidate.tests.jsonl"
	FileDerivation      = "derivation.json"
	FileQuestions       = "QUESTIONS.md"
	FileReadme          = "README.md"

	// activePolicyRel is the existing active policy derive diffs against for the
	// weakening check — and never writes to.
	activePolicyRel = ".interlock/policy.json"
)

// Result is a completed derivation: the typed records and the emittable candidate.
type Result struct {
	Derivation Derivation
	Candidate  Candidate
}

// Derive runs the pipeline over a repository root. from, when non-empty, is the
// explicit source set; otherwise the default V1 sources are auto-discovered. It
// reads files but writes nothing — rendering and writing are the caller's job.
func Derive(root string, from []string) (Result, error) {
	refs, err := discover(root, from)
	if err != nil {
		return Result{}, err
	}

	var raws []RawStatement
	explicit := len(from) > 0
	for _, ref := range refs {
		content, rerr := os.ReadFile(ref.path)
		if rerr != nil {
			if explicit {
				return Result{}, fmt.Errorf("interlock/derive: reading %s: %w", ref.relPath, rerr)
			}
			continue // auto-discovery tolerates a missing/unreadable candidate
		}
		for _, s := range ref.parse(ref.relPath, content) {
			raws = append(raws, s)
		}
	}

	records := recordsFrom(raws)
	detectConflicts(records)
	if existing, ok := loadActivePolicy(root); ok {
		checkWeakening(records, existing)
	}

	d := Derivation{Schema: DerivationSchema, PolicyID: DefaultPolicyID, Records: records}
	cand, err := buildCandidate(d)
	if err != nil {
		return Result{}, err
	}
	return Result{Derivation: d, Candidate: cand}, nil
}

// Rebuild re-assembles the candidate from a (possibly review-updated) Derivation
// without re-reading the repository. It is the second half of the --review flow:
// load derivation.json → ApplyAnswers → Rebuild → write. It runs the same
// buildCandidate as a fresh derive, so a reviewed candidate is byte-identical to
// one that had been fully grounded from the start.
func Rebuild(d Derivation) (Result, error) {
	cand, err := buildCandidate(d)
	if err != nil {
		return Result{}, err
	}
	return Result{Derivation: d, Candidate: cand}, nil
}

// DecodeDerivation parses a derivation.json document (fail-closed) for the
// --review flow.
func DecodeDerivation(b []byte) (Derivation, error) { return decodeDerivation(b) }

// recordsFrom sorts raw statements into a stable order, then classifies and
// grounds each into a Record with a deterministic id. Sorting before id
// assignment is what makes `--from A B` == `--from B A` (invariant 10).
func recordsFrom(raws []RawStatement) []Record {
	sort.SliceStable(raws, func(i, j int) bool {
		a, b := raws[i], raws[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.LineStart != b.LineStart {
			return a.LineStart < b.LineStart
		}
		if a.LineEnd != b.LineEnd {
			return a.LineEnd < b.LineEnd
		}
		return a.Text < b.Text
	})

	c := deterministicClassifier{}
	records := make([]Record, 0, len(raws))
	for i, raw := range raws {
		excerpt := cleanExcerpt(raw.Text)
		if excerpt == "" {
			continue
		}
		rec := Record{
			ID:       "r" + itoa(i+1),
			Source:   makeSource(raw, excerpt),
			Excerpt:  excerpt,
			Class:    classify(raw, c),
			Strength: raw.Strength,
		}
		ground(&rec, raw.Note)
		records = append(records, rec)
	}
	return records
}

// loadActivePolicy loads the existing canonical active policy for the weakening
// check. A missing file (the common case) or a non-canonical document yields
// ok=false — the check simply does not run, and derive never touches the file.
func loadActivePolicy(root string) (ir.Policy, bool) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(activePolicyRel)))
	if err != nil {
		return ir.Policy{}, false
	}
	p, err := ir.LoadPolicy(b)
	if err != nil {
		return ir.Policy{}, false
	}
	return p, true
}

// Files renders the result's output artifacts as a name→bytes map. The caller
// writes them atomically (all-or-nothing) so a failure never leaves a partial
// candidate (invariant 8, failure-state).
func (r Result) Files() (map[string][]byte, error) {
	derivationJSON, err := encodeDerivation(r.Derivation)
	if err != nil {
		return nil, err
	}
	tests, err := renderTests(r.Candidate.Vectors)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		FileCandidatePolicy: r.Candidate.Spec,
		FileCandidateTests:  tests,
		FileDerivation:      derivationJSON,
		FileQuestions:       renderQuestions(r.Derivation, r.Candidate.FreezeWarning),
		FileReadme:          renderReadme(r.Derivation, r.Candidate),
	}, nil
}

// renderTests writes the candidate test vectors as JSONL with a comment header,
// matching the shape `interlock test` reads (it skips lines starting with '#').
func renderTests(vectors []Vector) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# derived candidate tests — review, then: interlock test\n")
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, v := range vectors {
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

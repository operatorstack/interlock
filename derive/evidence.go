package derive

import (
	"strings"

	"github.com/operatorstack/interlock/ir"
)

// evidence.go turns an adapter's raw line span into a fail-closed Source. The
// provenance hash is always computed here from the excerpt bytes — never accepted
// from a caller — mirroring broker/envelope.go: hash-binding, not authenticity.

// cleanExcerpt normalizes a line of source into the stored excerpt: trimmed, with
// leading Markdown bullet/heading/quote punctuation removed so the same sentence
// hashes identically whether it appears as prose, a list item, or a heading.
func cleanExcerpt(text string) string {
	s := strings.TrimSpace(text)
	// Strip a leading run of markdown list/heading/quote markers.
	for {
		trimmed := strings.TrimLeft(s, "#>-*+ \t")
		if trimmed == s {
			break
		}
		s = trimmed
	}
	// Collapse internal runs of whitespace to single spaces for stable hashing.
	return strings.Join(strings.Fields(s), " ")
}

// makeSource binds a raw statement to its exact source bytes. SHA256 is
// ir.HashBytes(excerpt): tagged "sha256:"+hex, computed internally. There is no
// timestamp, so the same repository always produces the same provenance (the
// determinism invariant). This proves the record refers to these exact excerpt
// bytes at this path — it is NOT a claim that a trusted author wrote them.
func makeSource(raw RawStatement, excerpt string) Source {
	return Source{
		Path:      raw.Path,
		LineStart: raw.LineStart,
		LineEnd:   raw.LineEnd,
		SHA256:    ir.HashBytes([]byte(excerpt)),
	}
}

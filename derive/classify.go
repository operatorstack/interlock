package derive

import "strings"

// classify.go is the deterministic V1 classifier: it reads the language of a
// statement and assigns it a Class. It makes no network call and consults no
// model. The Classifier interface (schema.go) exists so a later semantic
// implementation can slot in, but even then ground.go re-validates every proposal
// against the closed vocabulary and compiler.Compile is the final gate — no
// classifier output ever becomes authority on its own.

// Marker sets, checked in precedence order. Precedence is deliberately biased
// toward the *most restrictive safe* reading: an explicit prohibition wins over
// everything (fail-closed), and advisory language can never be promoted past it.
var (
	// A hard prohibition. "should not"/"shouldn't" are included so plain
	// "should" (advisory) does not swallow them.
	prohibitionMarkers = []string{
		"must not", "must never", "never ", "do not ", "don't ", "does not ",
		"cannot ", "can not ", "may not ", "not allowed", "not permitted",
		"forbidden", "prohibited", "off-limits", "off limits",
		"should not", "shouldn't", "no one may", "under no circumstances",
	}
	// An explicit human sign-off gate.
	approvalMarkers = []string{
		"ask before", "require approval", "requires approval", "require human",
		"human approval", "approval before", "approval to", "approval from",
		"get approval", "sign-off", "sign off", "must be approved",
		"only with approval", "without approval", "needs approval",
	}
	// A demand for evidence before an effect.
	verificationMarkers = []string{
		"run tests before", "tests must pass", "must pass tests", "before pushing",
		"ensure tests pass", "must pass ci", "ci must pass", "tests before you",
		"before you push", "require passing", "only after tests",
	}
	// A preference, never authority.
	advisoryMarkers = []string{
		"prefer", "preferably", "usually", "consider", "try to", "should ",
		"recommend", "when possible", "ideally", "avoid ", "it's best",
		"we like", "tend to",
	}
)

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// deterministicClassifier is the only V1 Classifier. It is pure.
type deterministicClassifier struct{}

func (deterministicClassifier) Classify(text string) Class {
	// Pad with spaces so word-boundary-ish markers ("never ", "avoid ") match at
	// the start/end of the line too.
	h := " " + strings.ToLower(text) + " "
	switch {
	case containsAny(h, prohibitionMarkers):
		return ClassEnforceableEffect
	case containsAny(h, approvalMarkers):
		return ClassHumanDecision
	case containsAny(h, verificationMarkers):
		return ClassVerificationRequirement
	case containsAny(h, advisoryMarkers):
		return ClassAdvisoryGuidance
	default:
		return ClassDomainKnowledge
	}
}

// classify resolves a raw statement's class. An adapter's Suggest hint wins (a
// machine-config adapter knows its own shape); otherwise the deterministic
// classifier reads the prose.
func classify(raw RawStatement, c Classifier) Class {
	if raw.Suggest != "" {
		return raw.Suggest
	}
	return c.Classify(raw.Text)
}

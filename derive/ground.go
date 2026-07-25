package derive

import (
	"regexp"
	"strings"

	"github.com/operatorstack/interlock/ir"
)

// ground.go maps a classified statement onto the closed Interlock vocabulary
// (ir.Operations / ir.ResourceKinds / ir.RequirementKinds). It is where the "no
// invented authority" and "no semantic widening" invariants live: a field is
// filled ONLY when the source text literally cites it. Anything not cited becomes
// a Missing entry and a Question — never a guessed default. A statement that
// cannot be fully grounded stays StatusUnresolved and is not emitted.

// defaultActor is the coding agent — the principal these repository instructions
// address. V1 does not infer other actors; a rule about a different principal is
// surfaced as a question, not guessed.
const defaultActor = "agent"

var (
	repoURIRe  = regexp.MustCompile(`repo://[^\s"'` + "`" + `]+`)
	backtickRe = regexp.MustCompile("`([^`]+)`")
)

// ground turns a classified record into either a fully-grounded proposal
// (StatusProposed) or an unresolved question (StatusUnresolved). It mutates rec.
func ground(rec *Record, note string) {
	rec.Actor = defaultActor
	text := rec.Excerpt

	switch rec.Class {
	case ClassEnforceableEffect:
		groundEffect(rec, text)
	case ClassHumanDecision:
		groundHumanDecision(rec, text)
	case ClassVerificationRequirement:
		groundVerification(rec, text, note)
	default:
		// Advisory / domain / caller-suggested unresolved: never a rule.
		if rec.Class == ClassUnresolved {
			rec.Status = StatusUnresolved
			if note != "" {
				rec.Question = note
			} else {
				rec.Question = "This statement implies an effect restriction, but its operation and resource are not stated explicitly. Which operation and resource (repo:// URI) should it govern?"
			}
			// Record whatever we could detect, to help the reviewer.
			rec.Operations = detectOperations(text)
			if kind, uri, ok := detectResource(text, rec.Operations); ok {
				rec.ResourceKind, rec.ResourceURI = kind, uri
			}
		}
	}
}

// groundEffect handles an explicit prohibition → a deny rule.
func groundEffect(rec *Record, text string) {
	rec.Effect = ir.EffectDeny
	rec.Operations = detectOperations(text)
	kind, uri, hasRes := detectResource(text, rec.Operations)
	rec.ResourceKind, rec.ResourceURI = kind, uri
	rec.Reason = deriveReason(rec)

	var missing []string
	if len(rec.Operations) == 0 {
		missing = append(missing, "operation")
	}
	if !hasRes {
		missing = append(missing, "resource")
	}
	finalize(rec, missing,
		"This prohibition is clear but its "+strings.Join(missing, " and ")+
			" is not stated in the closed vocabulary. Which "+strings.Join(missing, " and ")+" does it govern?")
}

// groundHumanDecision handles an explicit approval gate → an allow rule requiring
// human_approval.
func groundHumanDecision(rec *Record, text string) {
	rec.Effect = ir.EffectAllow
	rec.Operations = detectOperations(text)
	kind, uri, hasRes := detectResource(text, rec.Operations)
	rec.ResourceKind, rec.ResourceURI = kind, uri

	var missing []string
	if len(rec.Operations) == 0 {
		missing = append(missing, "operation")
	}
	if !hasRes {
		missing = append(missing, "resource")
	}
	if len(missing) == 0 {
		req := ir.Requirement{Kind: ir.ReqHumanApproval, Approval: approvalID(rec.Operations)}
		rec.Requirement = &req
	}
	rec.Reason = deriveReason(rec)
	finalize(rec, missing,
		"This requires human approval, but its "+strings.Join(missing, " and ")+
			" is not stated. Which "+strings.Join(missing, " and ")+" needs approval?")
}

// groundVerification handles a "requires passing evidence" statement. In V1 the
// *verifier* (which receipt schema and status count as passing) is never inferred
// — a receipt requirement without a real verifier would be authority with no
// meaning (invariant 5) — so these always resolve to a question until --review
// supplies the schema.
func groundVerification(rec *Record, text, note string) {
	rec.Effect = ir.EffectAllow
	rec.Operations = detectOperations(text)
	kind, uri, hasRes := detectResource(text, rec.Operations)
	rec.ResourceKind, rec.ResourceURI = kind, uri
	rec.Reason = deriveReason(rec)

	missing := []string{"verifier"}
	if len(rec.Operations) == 0 {
		missing = append(missing, "operation")
	}
	if !hasRes {
		missing = append(missing, "resource")
	}
	q := "This demands evidence before an effect, but no receipt schema/status is named"
	if note != "" {
		q = note
	}
	rec.Status = StatusUnresolved
	rec.Missing = missing
	rec.Question = q + ". Which receipt schema and passing status count as evidence?"
}

// finalize sets a record's status from its Missing list: empty → proposed,
// otherwise unresolved with the given question.
func finalize(rec *Record, missing []string, question string) {
	if len(missing) == 0 {
		rec.Status = StatusProposed
		return
	}
	rec.Status = StatusUnresolved
	rec.Missing = missing
	rec.Question = question
}

// detectOperations returns the closed-vocabulary operations literally cited in
// the text, in canonical (ir.Operations) order, deduplicated.
func detectOperations(text string) []ir.Operation {
	h := strings.ToLower(text)
	set := map[ir.Operation]bool{}

	// VCS: force-push must be checked before push (it contains "push").
	if strings.Contains(h, "force-push") || strings.Contains(h, "force push") || strings.Contains(h, "force-pushing") {
		set[ir.OpForcePush] = true
	} else if strings.Contains(h, "push") {
		set[ir.OpPush] = true
	}
	if containsWord(h, "publish", "release", "deploy", "ship", "publishing", "releasing", "deploying") {
		set[ir.OpPublish] = true
	}
	// Filesystem.
	if containsWord(h, "edit", "edited", "editing", "modify", "modified", "modifying",
		"change", "changed", "changing", "write", "writing", "touch", "touching", "alter", "update", "updating") {
		set[ir.OpWrite] = true
	}
	if containsWord(h, "delete", "deleting", "remove", "removing") {
		set[ir.OpDelete] = true
	}
	if containsWord(h, "rename", "renaming", "move", "moving") {
		set[ir.OpRenameFrom] = true
		set[ir.OpRenameTo] = true
	}
	if containsWord(h, "execute", "executing", "run ", "running") {
		set[ir.OpExecute] = true
	}
	if containsWord(h, "read", "reading") {
		set[ir.OpRead] = true
	}

	var out []ir.Operation
	for _, op := range ir.Operations {
		if set[op] {
			out = append(out, op)
		}
	}
	return out
}

// containsWord reports whether any of the substrings appear in h. (Names it
// "word" for intent; matching is substring, which is sufficient for the fixed
// keyword set above.)
func containsWord(h string, subs ...string) bool {
	for _, s := range subs {
		if strings.Contains(h, s) {
			return true
		}
	}
	return false
}

// detectResource extracts the resource a statement governs, only from what the
// text literally cites. Order: an explicit repo:// URI, then a protected branch
// (main/master) when the ops are branch ops, then a generated-files reference,
// then a backtick-quoted path. Returns ok=false when nothing is cited — the
// caller must then raise a question, never assume a scope.
func detectResource(text string, ops []ir.Operation) (ir.ResourceKind, string, bool) {
	// 1. Explicit repo:// URI wins — no interpretation needed.
	if m := repoURIRe.FindString(text); m != "" {
		return classifyURI(m)
	}

	h := strings.ToLower(text)
	branchOps := hasAny(ops, ir.OpPush, ir.OpForcePush)

	// 2. A named protected branch, when the operation is a branch operation.
	if branchOps {
		for _, b := range []string{"main", "master", "release", "production"} {
			if strings.Contains(h, b+" branch") || strings.Contains(h, "branch "+b) ||
				strings.Contains(h, "the "+b) || strings.Contains(h, " "+b+" ") {
				return ir.KindBranch, "repo://branch/" + b, true
			}
		}
	}

	// 3. Generated files — a well-known machine-owned tree.
	if strings.Contains(h, "generated") {
		return ir.KindTree, "repo://generated/**", true
	}

	// 4. A backtick-quoted path (e.g. `src/**`, `config/prod.yaml`).
	for _, m := range backtickRe.FindAllStringSubmatch(text, -1) {
		p := strings.TrimSpace(m[1])
		if looksLikePath(p) {
			return pathToResource(p)
		}
	}

	return "", "", false
}

func hasAny(ops []ir.Operation, want ...ir.Operation) bool {
	for _, o := range ops {
		for _, w := range want {
			if o == w {
				return true
			}
		}
	}
	return false
}

// looksLikePath is a conservative filter so an inline code span for a symbol
// (`someFunc`) is not mistaken for a file path.
func looksLikePath(p string) bool {
	if p == "" || strings.Contains(p, " ") {
		return false
	}
	return strings.HasPrefix(p, "repo://") || strings.Contains(p, "/") ||
		strings.Contains(p, "*") || strings.Contains(p, ".")
}

// classifyURI infers a resource kind from an explicit repo:// URI without
// widening it: a branch path is a branch, a glob/dir is a tree, a file with an
// extension is a file, everything else defaults to a tree (the least surprising
// containing scope for a bare directory name).
func classifyURI(uri string) (ir.ResourceKind, string, bool) {
	uri = strings.TrimRight(uri, ".,;:)")
	switch {
	case strings.Contains(uri, "/branch/"):
		return ir.KindBranch, uri, true
	case strings.HasSuffix(uri, "/"):
		return ir.KindTree, uri + "**", true
	case strings.HasSuffix(uri, "**"):
		return ir.KindTree, uri, true
	case hasFileExt(uri):
		return ir.KindFile, uri, true
	default:
		return ir.KindTree, strings.TrimRight(uri, "/") + "/**", true
	}
}

// pathToResource converts a repo-relative or repo:// path into a resource.
func pathToResource(p string) (ir.ResourceKind, string, bool) {
	if !strings.HasPrefix(p, "repo://") {
		p = "repo://" + strings.TrimPrefix(strings.TrimPrefix(p, "./"), "/")
	}
	return classifyURI(p)
}

func hasFileExt(uri string) bool {
	base := uri
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		base = uri[i+1:]
	}
	dot := strings.LastIndex(base, ".")
	return dot > 0 && dot < len(base)-1 && !strings.Contains(base, "*")
}

// approvalID derives a stable approval id from the governed operations, e.g.
// publish → "approve-publish". It is a label on the requirement, not authority.
func approvalID(ops []ir.Operation) string {
	if len(ops) == 0 {
		return "approve"
	}
	seg := string(ops[0])
	if i := strings.LastIndex(seg, "."); i >= 0 {
		seg = seg[i+1:]
	}
	return "approve-" + seg
}

// deriveReason renders the human-facing reason carried into the candidate rule.
// It cites the provenance so a reviewer can trace every rule to its source line.
func deriveReason(rec *Record) string {
	verb := "restricted"
	if rec.Effect == ir.EffectAllow {
		verb = "gated"
	}
	return "derived (" + verb + ") from " + rec.Source.Path + " — review before enforcing"
}

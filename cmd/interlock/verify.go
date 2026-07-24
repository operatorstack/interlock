package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/proof"
)

// verifyLine is one release-proof line in the CLI output: a claim plus a status
// (PASS/FAIL/SKIP) and a short detail. proof.Run() supplies the ten in-process
// lines; the Pitot line is added by shelling out (or SKIP when unreachable).
type verifyLine struct {
	Claim  string `json:"claim"`
	Status string `json:"status"` // "pass" | "fail" | "skip"
	Detail string `json:"detail,omitempty"`
}

const (
	statusPass = "pass"
	statusFail = "fail"
	statusSkip = "skip"
)

// cmdVerify runs the release proof and prints it in the requested format. It
// returns a non-nil error (→ exit 1) if any line FAILed. A SKIP is not a FAIL.
func cmdVerify(args []string) error {
	format := "text"
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--format", "-f":
			if i+1 >= len(args) {
				return fmt.Errorf("verify: --format wants text|json|markdown")
			}
			format = args[i+1]
			i += 2
		default:
			return fmt.Errorf("verify: unexpected argument %q", args[i])
		}
	}

	lines := make([]verifyLine, 0, 11)
	for _, r := range proof.Run() {
		st := statusPass
		if !r.OK {
			st = statusFail
		}
		lines = append(lines, verifyLine{Claim: r.Claim, Status: st, Detail: r.Detail})
	}
	lines = append(lines, verifyPitot())

	failed := false
	for _, l := range lines {
		if l.Status == statusFail {
			failed = true
			break
		}
	}
	commit := buildCommit()

	switch format {
	case "text":
		printVerifyText(lines, failed, commit)
	case "json":
		if err := printVerifyJSON(lines, failed, commit); err != nil {
			return err
		}
	case "markdown":
		printVerifyMarkdown(lines, failed, commit)
	default:
		return fmt.Errorf("verify: unknown format %q (want text|json|markdown)", format)
	}

	if failed {
		return errors.New("release proof FAILED")
	}
	return nil
}

// --- Line 11: Pitot decision transport (cross-module shell-out) ------------

// verifyPitot proves the Pitot decision adapter round-trips. Pitot is a separate
// Go module (github.com/operatorstack/interlock-pitot); the core binary cannot
// import it. When a go toolchain and the integrations/pitot source are both
// reachable, we run its adapter round-trip via `go test ./adapter/...`. Otherwise
// we SKIP — never a false PASS.
func verifyPitot() verifyLine {
	claim := "Pitot decision transport round-trips"
	dir := findPitotDir()
	if dir == "" {
		return verifyLine{Claim: claim, Status: statusSkip,
			Detail: "integrations/pitot source not found; run its own verify"}
	}
	if _, err := exec.LookPath("go"); err != nil {
		return verifyLine{Claim: claim, Status: statusSkip,
			Detail: "go toolchain not found; cannot exercise the Pitot module"}
	}
	cmd := exec.Command("go", "test", "./adapter/...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		return verifyLine{Claim: claim, Status: statusFail,
			Detail: strings.TrimSpace(lastLine(string(out)))}
	}
	return verifyLine{Claim: claim, Status: statusPass, Detail: "adapter round-trip (" + dir + ")"}
}

// findPitotDir locates the integrations/pitot module directory: an explicit
// INTERLOCK_PITOT_DIR override wins, else a small set of paths relative to the
// current working directory (covering runs from the interlock module dir or the
// lab root). It returns "" when no adapter source is present.
func findPitotDir() string {
	if env := os.Getenv("INTERLOCK_PITOT_DIR"); env != "" {
		if isPitotDir(env) {
			return env
		}
		return ""
	}
	candidates := []string{
		filepath.Join("integrations", "pitot"),
		filepath.Join("..", "integrations", "pitot"),
		filepath.Join("labs", "21-interlock", "integrations", "pitot"),
	}
	for _, c := range candidates {
		if isPitotDir(c) {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}

// isPitotDir reports whether dir looks like the Pitot module: a go.mod plus an
// adapter package to test.
func isPitotDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "adapter")); err != nil {
		return false
	}
	return true
}

// --- Build info -----------------------------------------------------------

// buildCommit returns the short VCS revision the binary was built from, with a
// "-dirty" suffix when the tree was modified. It reads runtime build info, which
// is populated by `go build` (not `go run`); it returns "unknown" otherwise.
func buildCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "unknown"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

// --- Renderers ------------------------------------------------------------

func statusLabel(s string) string {
	switch s {
	case statusPass:
		return "PASS"
	case statusFail:
		return "FAIL"
	default:
		return "SKIP"
	}
}

func printVerifyText(lines []verifyLine, failed bool, commit string) {
	fmt.Println("Interlock Release Proof")
	fmt.Println()
	for _, l := range lines {
		fmt.Printf("%s  %s\n", statusLabel(l.Status), l.Claim)
	}
	fmt.Println()
	if failed {
		fmt.Println("RESULT: FAIL")
	} else {
		fmt.Println("RESULT: PASS")
	}
	fmt.Printf("Commit: %s\n", commit)
	fmt.Printf("Protocol: %s\n", ir.Protocol)
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  - \"second tenant uses the same broker path\" runs a second policy shape")
	fmt.Println("    through the same broker code, not concurrent multi-tenant execution.")
	fmt.Println("  - the Pitot line proves the pure decision adapter; the wire controller is")
	fmt.Println("    covered by integrations/pitot's own validate.sh. A SKIP means the Pitot")
	fmt.Println("    module was not reachable from here — run its verify separately.")
}

func printVerifyJSON(lines []verifyLine, failed bool, commit string) error {
	result := "PASS"
	if failed {
		result = "FAIL"
	}
	return printJSON(struct {
		Proof    string       `json:"proof"`
		Results  []verifyLine `json:"results"`
		Result   string       `json:"result"`
		Commit   string       `json:"commit"`
		Protocol string       `json:"protocol"`
	}{
		Proof:    "Interlock Release Proof",
		Results:  lines,
		Result:   result,
		Commit:   commit,
		Protocol: ir.Protocol,
	})
}

// mdRow is one curated README table row: a plain-language claim, the backing
// evidence label, and the underlying proof-line claims that determine its status.
type mdRow struct {
	claim    string
	evidence string
	backing  []string
}

// curatedRows maps the 11 proof lines onto the 6 rows published in the README.
var curatedRows = []mdRow{
	{"Canonical IR is deterministic", "4 frozen policy hashes",
		[]string{"canonical policy bytes are deterministic", "all golden policy hashes match"}},
	{"Engine decisions conform", "positive + negative vectors",
		[]string{"positive decision vectors conform", "negative decision vectors conform", "missing evidence returns require"}},
	{"Broker publishes exact bytes", "broker lifecycle suite",
		[]string{"broker publishes byte-exact staged content", "second tenant uses the same broker path"}},
	{"Evidence mismatch fails closed", "negative broker vectors",
		[]string{"stale target state fails closed", "copied or cross-run evidence fails closed"}},
	{"Receipt chains detect mutation", "replay suite",
		[]string{"changed policy breaks replay"}},
	{"Pitot transports decisions", "Controller round-trip",
		[]string{"Pitot decision transport round-trips"}},
}

// rowStatus folds the statuses of a row's backing proof lines into one label: any
// FAIL → FAIL; else any SKIP → SKIP; else PASS.
func rowStatus(lines []verifyLine, backing []string) string {
	byClaim := make(map[string]string, len(lines))
	for _, l := range lines {
		byClaim[l.Claim] = l.Status
	}
	worst := statusPass
	for _, b := range backing {
		st, ok := byClaim[b]
		if !ok {
			return statusFail // a curated row references a proof line that vanished
		}
		if st == statusFail {
			return statusFail
		}
		if st == statusSkip {
			worst = statusSkip
		}
	}
	return worst
}

func printVerifyMarkdown(lines []verifyLine, failed bool, commit string) {
	fmt.Println("| Claim | Result | Evidence |")
	fmt.Println("| --- | --- | --- |")
	for _, r := range curatedRows {
		fmt.Printf("| %s | %s | %s |\n", r.claim, statusLabel(rowStatus(lines, r.backing)), r.evidence)
	}
	fmt.Println()
	fmt.Printf("Verified at commit `%s`.\n", commit)
}

// lastLine returns the last non-empty line of s (used to surface a failing
// Pitot test's most relevant output without dumping the whole log).
func lastLine(s string) string {
	fields := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(fields) - 1; i >= 0; i-- {
		if strings.TrimSpace(fields[i]) != "" {
			return fields[i]
		}
	}
	return s
}

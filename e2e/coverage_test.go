package e2e

// control-law: shipped-surface-honors-the-core (coverage obligation)
//
// The coverage law that keeps the control law obeyed as the surface grows: read
// the CLI dispatch switch in cmd/interlock/main.go, extract every subcommand it
// serves, and require each one to be named in the `covered` map below (each value
// naming the e2e that exercises it). A new subcommand cannot merge without an e2e
// entry — the shipped surface can never outgrow its proof. Mirrors Pitot's
// TestAllAdaptersHaveE2EScripts.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// covered maps each real CLI subcommand to the e2e that drives it through the
// shipped binary. Alias flags (-v, --version, -h, --help) and `help` are not
// commands and are excluded from the dispatch set below.
var covered = map[string]string{
	"init":     "TestJourney_InitTestTamper",
	"derive":   "TestJourney_Derive",
	"compile":  "TestJourney_Derive (promotion) + parity fixtures",
	"check":    "TestSmoke_InfoCommands",
	"explain":  "TestSmoke_InfoCommands",
	"decide":   "TestDecideParity_Corpus + TestIsolation_AuthoritySeparation",
	"publish":  "TestJourney_PublishRoundTrip + TestBrokerParity_Corpus",
	"simulate": "TestJourney_SimulateReplay",
	"replay":   "TestJourney_SimulateReplay",
	"test":     "TestJourney_InitTestTamper + TestJourney_Derive",
	"demo":     "TestSmoke_InfoCommands",
	"doctor":   "TestSmoke_InfoCommands",
	"verify":   "TestSmoke_InfoCommands",
	"version":  "TestSmoke_InfoCommands",
}

var caseLabel = regexp.MustCompile(`case\s+((?:"[^"]+"\s*,\s*)*"[^"]+")\s*:`)
var quoted = regexp.MustCompile(`"([^"]+)"`)

func TestEveryCommandHasE2E(t *testing.T) {
	src, err := os.ReadFile("../cmd/interlock/main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	// Only scan the dispatch switch (main's `switch os.Args[1]`), not the helper
	// switches (decodePolicy's protocol switch, flag parsing) further down.
	body := string(src)
	start := strings.Index(body, "switch os.Args[1]")
	if start < 0 {
		t.Fatal("could not locate the CLI dispatch switch")
	}
	end := strings.Index(body[start:], "\nfunc ")
	if end < 0 {
		end = len(body) - start
	}
	dispatch := body[start : start+end]

	commands := map[string]bool{}
	for _, m := range caseLabel.FindAllStringSubmatch(dispatch, -1) {
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			label := q[1]
			if strings.HasPrefix(label, "-") || label == "help" {
				continue // alias flags and help are not commands
			}
			commands[label] = true
		}
	}
	if len(commands) == 0 {
		t.Fatal("extracted no commands from the dispatch switch — regex drift?")
	}

	var missing []string
	for cmd := range commands {
		if _, ok := covered[cmd]; !ok {
			missing = append(missing, cmd)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("these CLI commands have no e2e entry in covered{}: %v\n"+
			"add an e2e that drives the shipped binary and register it in coverage_test.go", missing)
	}

	// Also flag stale entries so the map does not rot as commands are removed.
	var stale []string
	for cmd := range covered {
		if !commands[cmd] {
			stale = append(stale, cmd)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("covered{} names commands no longer in the dispatch switch: %v", stale)
	}
}

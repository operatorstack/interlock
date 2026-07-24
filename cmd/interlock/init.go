package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/operatorstack/interlock/scaffold"
)

const defaultInterlockDir = ".interlock"

// cmdInit routes between the two authoring surfaces. The accessible default is a
// no-toolchain JSON policy under .interlock/; `--authoring go <dir>` preserves the
// programmable Go scaffold. A bare positional `init <dir>` (no --authoring) is a
// deprecated alias for the Go scaffold, kept for compatibility through ≤1.0.
func cmdInit(args []string) error {
	authoring := ""
	template := ""
	path := ""
	force := false
	var positional []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--authoring":
			if i+1 >= len(args) {
				return fmt.Errorf("init: --authoring wants go|json")
			}
			authoring = args[i+1]
			i += 2
		case "--template", "-t":
			if i+1 >= len(args) {
				return fmt.Errorf("init: --template wants a key (%s)", strings.Join(scaffold.Keys(), "|"))
			}
			template = args[i+1]
			i += 2
		case "--path":
			if i+1 >= len(args) {
				return fmt.Errorf("init: --path wants a resource glob")
			}
			path = args[i+1]
			i += 2
		case "--force", "-f":
			force = true
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("init: unexpected flag %q", args[i])
			}
			positional = append(positional, args[i])
			i++
		}
	}
	if len(positional) > 1 {
		return fmt.Errorf("init: want at most one directory")
	}

	switch authoring {
	case "go":
		if len(positional) != 1 {
			return fmt.Errorf("init --authoring go: want <dir>")
		}
		return initGo(positional[0])
	case "json":
		dir := defaultInterlockDir
		if len(positional) == 1 {
			dir = positional[0]
		}
		return initJSON(dir, template, path, force)
	case "":
		// No --authoring: a positional dir is the deprecated Go alias; otherwise
		// the bare, interactive JSON flow.
		if len(positional) == 1 {
			fmt.Fprint(os.Stderr, "Note: `interlock init <dir>` currently creates a Go policy module.\n"+
				"Use `interlock init --authoring go <dir>` explicitly.\n"+
				"Bare `interlock init` creates the no-toolchain JSON setup.\n")
			return initGo(positional[0])
		}
		return initJSON(defaultInterlockDir, template, path, force)
	default:
		return fmt.Errorf("init: unknown --authoring %q (want go|json)", authoring)
	}
}

// initJSON writes the declarative, no-toolchain setup (policy.json + tests.jsonl +
// README.md) into dir. When template is empty it prompts interactively; on EOF
// (non-interactive, no --template) it errors rather than guessing.
func initJSON(dir, template, path string, force bool) error {
	if template == "" {
		chosen, chosenPath, err := promptTemplate()
		if err != nil {
			return err
		}
		template = chosen
		if chosenPath != "" {
			path = chosenPath
		}
	}
	tmpl, ok := scaffold.ByKey(template)
	if !ok {
		return fmt.Errorf("init: unknown template %q (want %s)", template, strings.Join(scaffold.Keys(), "|"))
	}

	policyPath := filepath.Join(dir, "policy.json")
	if _, err := os.Stat(policyPath); err == nil && !force {
		return fmt.Errorf("init: %s already exists (use --force to overwrite)", policyPath)
	}

	policy, err := tmpl.Policy(path)
	if err != nil {
		return fmt.Errorf("init: building policy: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(policyPath, policy, 0o644); err != nil {
		return err
	}
	if err := writeTests(filepath.Join(dir, "tests.jsonl"), tmpl, path); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(tmpl.Readme(path)), 0o644); err != nil {
		return err
	}

	fmt.Printf("Created:\n\n  %s\n  %s\n  %s\n\nRun:\n  interlock test",
		policyPath, filepath.Join(dir, "tests.jsonl"), filepath.Join(dir, "README.md"))
	if dir != defaultInterlockDir {
		fmt.Printf(" %s", dir)
	}
	fmt.Println()
	return nil
}

// writeTests emits one canonical JSON object per vector, preceded by a comment
// header (interlock test skips lines starting with '#').
func writeTests(path string, tmpl scaffold.Template, custom string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "# tests for %s — run: interlock test\n", tmpl.Key); err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, v := range tmpl.Vectors(custom) {
		if err := enc.Encode(v); err != nil {
			return err
		}
	}
	return nil
}

// promptTemplate renders the "What are you protecting?" menu and reads a choice
// from stdin. It returns the chosen template key (and, for the custom template, a
// protected path). On EOF with no input it returns a clear non-interactive error.
func promptTemplate() (key, path string, err error) {
	templates := scaffold.Templates()
	fmt.Println("What are you protecting?")
	fmt.Println()
	for i, t := range templates {
		fmt.Printf("  %d. %s\n", i+1, t.Title)
	}
	fmt.Println()
	fmt.Print("Choice [1-" + strconv.Itoa(len(templates)) + "]: ")

	r := bufio.NewReader(os.Stdin)
	line, rerr := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", fmt.Errorf("init: --authoring json needs --template <key> when non-interactive (%s)", strings.Join(scaffold.Keys(), "|"))
	}
	n, cerr := strconv.Atoi(line)
	if cerr != nil || n < 1 || n > len(templates) {
		return "", "", fmt.Errorf("init: invalid choice %q (want 1-%d)", line, len(templates))
	}
	chosen := templates[n-1]
	if chosen.Key == "custom" {
		fmt.Print("Protected path glob [" + defaultCustomPathHint + "]: ")
		p, _ := r.ReadString('\n')
		p = strings.TrimSpace(p)
		return chosen.Key, p, nil
	}
	_ = rerr
	return chosen.Key, "", nil
}

// defaultCustomPathHint mirrors scaffold's default for the interactive prompt.
const defaultCustomPathHint = "repo://protected/**"

// initGo scaffolds a minimal, deterministic Go policy module (the programmable
// authoring surface): arbitrary Go may run in Build(); only the emitted IR decides.
func initGo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	main := `package main

import (
	"fmt"
	"os"

	il "github.com/operatorstack/interlock"
)

// Build constructs the policy. Arbitrary Go may run here; only the emitted IR
// decides requests.
func Build() *il.Builder {
	return il.Policy("example.v1").
		Actor("agent").
		Actor("publisher").
		File("artifact", "repo://out/result.json").
		Deny("agent-no-write").By("agent").To(il.Write, il.Publish).On("artifact").
		Because("the producing agent may not write the protected artifact").Add().
		Allow("publisher-may-publish").By("publisher").To(il.Publish).On("artifact").
		Requiring(il.PolicyHashMatch(), il.StagedHashMatch()).
		Because("the verified publisher may publish a staged candidate").Add()
}

func main() {
	b, err := Build().Emit()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(b)
}
`
	if err := os.WriteFile(filepath.Join(dir, "policy.go"), []byte(main), 0o644); err != nil {
		return err
	}
	fmt.Printf("scaffolded policy module at %s\n", dir)
	return nil
}

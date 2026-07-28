package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The install shortcut fetches the typed client for a language from the project's
// OWN registry, fronted by the public install host — the ONE coordinate this command
// needs. The host is public (it already appears in the shell installer), so embedding
// it here leaks nothing; the private Artifact Registry coordinates live only behind
// that front door (get-service proxies them). Package names are the public client
// package identities. Override the host with INTERLOCK_GET_HOST or --host for staging.
const (
	defaultGetHost = "get.operatorstack.systems"
	npmScope       = "@operatorstack"
	npmClientPkg   = "@operatorstack/interlock"
	pyClientPkg    = "interlock-protocol"
)

// compatibleSDKVersion is the typed-client version this CLI is protocol-compatible
// with. Binary and SDK ship from one tag, so "compatible" is simply "same release".
// A dev/un-stamped build pins nothing (installs latest) so local checkouts work.
func compatibleSDKVersion() string {
	v := releaseVersion()
	if v == "dev" || v == "" {
		return ""
	}
	return v
}

// npmClientSpec / pyClientSpec pin the install to the compatible version, preventing
// a fresh SDK from silently outrunning the CLI's protocol.
func npmClientSpec() string {
	if v := compatibleSDKVersion(); v != "" {
		return npmClientPkg + "@" + v
	}
	return npmClientPkg
}

func pyClientSpec() string {
	if v := compatibleSDKVersion(); v != "" {
		return pyClientPkg + "==" + v
	}
	return pyClientPkg
}

func resolveGetHost(flag string) string {
	if flag != "" {
		return flag
	}
	if env := strings.TrimSpace(os.Getenv("INTERLOCK_GET_HOST")); env != "" {
		return env
	}
	return defaultGetHost
}

// cmdInstall fetches the typed client for a language from the front-door registry,
// configuring the package manager's index so the consumer never hand-edits .npmrc /
// --index-url and never needs GCP credentials. --configure-only writes the registry
// config and stops (no toolchain required); the default also runs the install.
func cmdInstall(args []string) error {
	host := ""
	dir := "."
	force := false
	configureOnly := false
	revert := false
	example := false
	var positional []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--host":
			if i+1 >= len(args) {
				return fmt.Errorf("install: --host wants a hostname")
			}
			host = args[i+1]
			i += 2
		case "--dir":
			if i+1 >= len(args) {
				return fmt.Errorf("install: --dir wants a path")
			}
			dir = args[i+1]
			i += 2
		case "--configure-only":
			configureOnly = true
			i++
		case "--revert":
			revert = true
			i++
		case "--example":
			example = true
			i++
		case "--upgrade":
			// --upgrade is ergonomic: a normal install already pins to the compatible
			// version, so re-running it after `interlock upgrade` bumps the SDK to match.
			i++
		case "--force", "-f":
			force = true
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("install: unexpected flag %q", args[i])
			}
			positional = append(positional, args[i])
			i++
		}
	}
	if len(positional) > 1 {
		return fmt.Errorf("install: expected at most one language, got %v", positional)
	}

	lang := ""
	if len(positional) == 1 {
		lang = positional[0]
	} else {
		chosen, err := promptLanguage()
		if err != nil {
			return err
		}
		lang = chosen
	}

	host = resolveGetHost(host)
	nl := normalizeLang(lang)
	if nl == "" {
		return fmt.Errorf("install: unknown language %q (want: ts | python)", lang)
	}
	if revert {
		return revertLang(nl, dir)
	}
	if example {
		return writeExample(nl, dir, force)
	}
	switch nl {
	case "ts":
		return installNPM(dir, host, force, configureOnly)
	default:
		return installPython(dir, host, force, configureOnly)
	}
}

// revertLang removes the registry config the install wrote — the undo side of the
// install lifecycle. It clears the scoped .npmrc line / the .interlock/registry
// file; it does not uninstall the package (leave that to the package manager).
func revertLang(nl, dir string) error {
	switch nl {
	case "ts":
		npmrc := filepath.Join(dir, ".npmrc")
		b, err := os.ReadFile(npmrc)
		if os.IsNotExist(err) {
			fmt.Printf("nothing to revert: %s not present\n", npmrc)
			return nil
		}
		if err != nil {
			return err
		}
		remaining := removeLine(string(b), npmScope+":registry=")
		if strings.TrimSpace(remaining) == "" {
			if err := os.Remove(npmrc); err != nil {
				return err
			}
			fmt.Printf("reverted: removed %s\n", npmrc)
			return nil
		}
		if err := os.WriteFile(npmrc, []byte(remaining), 0o644); err != nil {
			return err
		}
		fmt.Printf("reverted: removed the @operatorstack registry line from %s\n", npmrc)
		return nil
	default:
		reg := filepath.Join(dir, ".interlock", "registry")
		if err := os.Remove(reg); err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("nothing to revert: %s not present\n", reg)
				return nil
			}
			return err
		}
		fmt.Printf("reverted: removed %s\n", reg)
		return nil
	}
}

// removeLine drops every line whose trimmed form starts with prefix.
func removeLine(existing, prefix string) string {
	out := []string{}
	for _, l := range strings.Split(existing, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimLeft(strings.Join(out, "\n"), "\n")
}

// writeExample scaffolds a minimal starter that builds an interlock.effect.v1 request
// with the typed client, mirroring init's scaffolding convention.
func writeExample(nl, dir string, force bool) error {
	var name, body string
	if nl == "ts" {
		name = "interlock-example.ts"
		body = `import { EffectRequest } from "@operatorstack/interlock";

// The client builds requests; the interlock engine decides them.
const req: EffectRequest = {
  protocol: "interlock.effect.v1",
  request_id: "example-1",
  run_id: "example-run",
  actor: "agent",
  operation: "artifact.publish",
  resource: { kind: "file", uri: "repo://out/result.json" },
};
console.log(JSON.stringify(req, null, 2));
`
	} else {
		name = "interlock_example.py"
		body = `from interlock_protocol.protocol import EffectRequest, TargetResource

# The client builds requests; the interlock engine decides them.
req: EffectRequest = {
    "protocol": "interlock.effect.v1",
    "request_id": "example-1",
    "run_id": "example-run",
    "actor": "agent",
    "operation": "artifact.publish",
    "resource": {"kind": "file", "uri": "repo://out/result.json"},
}
print(req)
`
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("install: %s already exists (use --force to overwrite)", path)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote example: %s\n", path)
	return nil
}

func normalizeLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "ts", "typescript", "js", "javascript", "npm", "node":
		return "ts"
	case "python", "py", "pip", "uv":
		return "python"
	default:
		return ""
	}
}

// promptLanguage renders a numbered picker and reads a choice, mirroring init's
// promptTemplate. On EOF with no input it returns a clear non-interactive error.
func promptLanguage() (string, error) {
	fmt.Println("Which typed client do you want to install?")
	fmt.Println()
	fmt.Println("  1. TypeScript (" + npmClientPkg + ", via npm)")
	fmt.Println("  2. Python (" + pyClientPkg + ", via uv or pip)")
	fmt.Println()
	fmt.Print("Choice [1-2]: ")

	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		return "ts", nil
	case "2":
		return "python", nil
	case "":
		return "", fmt.Errorf("install: name a language when non-interactive: interlock install ts|python")
	default:
		return "", fmt.Errorf("install: invalid choice %q (want 1 or 2)", strings.TrimSpace(line))
	}
}

// upsertLine replaces the first line whose key matches prefix, or appends it,
// keeping the file idempotent across re-runs.
func upsertLine(existing, prefix, line string) string {
	out := []string{}
	replaced := false
	for _, l := range strings.Split(existing, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			if !replaced {
				out = append(out, line)
				replaced = true
			}
			continue
		}
		out = append(out, l)
	}
	joined := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if !replaced {
		if joined != "" {
			joined += "\n"
		}
		joined += line
	}
	return joined + "\n"
}

func installNPM(dir, host string, force, configureOnly bool) error {
	registryURL := fmt.Sprintf("https://%s/npm/", host)
	line := fmt.Sprintf("%s:registry=%s", npmScope, registryURL)
	npmrc := filepath.Join(dir, ".npmrc")
	existing := ""
	if b, err := os.ReadFile(npmrc); err == nil {
		existing = string(b)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(npmrc, []byte(upsertLine(existing, npmScope+":registry=", line)), 0o644); err != nil {
		return err
	}
	fmt.Printf("configured %s -> %s\n", npmrc, registryURL)
	if configureOnly {
		fmt.Printf("run: npm install %s\n", npmClientSpec())
		return nil
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("install: npm not found on PATH (config written; run: npm install %s)", npmClientSpec())
	}
	return runIn(dir, "npm", "install", npmClientSpec())
}

func installPython(dir, host string, force, configureOnly bool) error {
	indexURL := fmt.Sprintf("https://%s/pip/simple/", host)
	// Persist the index for reuse and print the exact command. uv and pip take the
	// same --index-url flag, so the install line is uniform; the .interlock/registry
	// file records it so re-runs and CI can source one place.
	regFile := filepath.Join(dir, ".interlock", "registry")
	if err := os.MkdirAll(filepath.Dir(regFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(regFile, []byte("PIP_INDEX_URL="+indexURL+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("configured %s (PIP_INDEX_URL=%s)\n", regFile, indexURL)
	if configureOnly {
		fmt.Printf("run: uv pip install --index-url %s %s   (or: pip install --index-url %s %s)\n", indexURL, pyClientSpec(), indexURL, pyClientSpec())
		return nil
	}
	if _, err := exec.LookPath("uv"); err == nil {
		return runIn(dir, "uv", "pip", "install", "--index-url", indexURL, pyClientSpec())
	}
	if _, err := exec.LookPath("pip"); err == nil {
		return runIn(dir, "pip", "install", "--index-url", indexURL, pyClientSpec())
	}
	return fmt.Errorf("install: neither uv nor pip found on PATH (config written; run with --index-url %s)", indexURL)
}

func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

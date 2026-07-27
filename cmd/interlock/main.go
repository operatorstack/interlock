// Command interlock is the Interlock CLI: it compiles code-defined policies to
// canonical IR, evaluates effect requests, brokers protected publishes, and
// replays decision receipts. The heavy lifting lives in the library packages;
// this binary is a thin, deterministic front end (the only place os/exec, flags,
// and file I/O are allowed to meet the pure engine).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/operatorstack/interlock/broker"
	"github.com/operatorstack/interlock/compiler"
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
	"github.com/operatorstack/interlock/spec"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "install":
		err = cmdInstall(os.Args[2:])
	case "upgrade":
		err = cmdUpgrade(os.Args[2:])
	case "derive":
		err = cmdDerive(os.Args[2:])
	case "compile":
		err = cmdCompile(os.Args[2:])
	case "check":
		err = cmdCheck(os.Args[2:])
	case "explain":
		err = cmdExplain(os.Args[2:])
	case "decide":
		err = cmdDecide(os.Args[2:])
	case "publish":
		err = cmdPublish(os.Args[2:])
	case "simulate":
		err = cmdSimulate(os.Args[2:])
	case "replay":
		err = cmdReplay(os.Args[2:])
	case "test":
		err = cmdTest(os.Args[2:])
	case "demo":
		err = cmdDemo(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "version", "-v", "--version":
		err = cmdVersion(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "interlock: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "interlock: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `interlock — code-defined effect-policy runtime

usage:
  interlock init                             set up a no-toolchain JSON policy (interactive)
  interlock init --authoring json [dir]      set up a JSON policy (dir defaults to .interlock)
  interlock init --authoring go <dir>        scaffold a programmable Go policy module
  interlock install [ts|python]              install the typed client from your registry (--configure-only writes config)
  interlock upgrade [--check]                update interlock to the latest published version
  interlock derive [repo] [--from PATH] [--output DIR] [--review]   draft a candidate policy from a repo's existing instructions (never enforces)
  interlock test [dir]                       run the policy's tests (dir defaults to .interlock)
  interlock demo [name]                       narrate a built-in policy (default repository-policy; --list)
  interlock compile <dir> [-o policy.json]   build+run a Go policy module → canonical IR
  interlock compile <spec.json> [-o out]     compile an interlock.spec.v1 doc → canonical IR (no toolchain)
  interlock check <policy|spec.json>         validate a policy (IR or spec.v1) and print its hash
  interlock explain <policy|spec.json>       print a human-readable policy summary
  interlock decide <policy.json> <req.json>  evaluate one effect request
  interlock publish <policy.json> <pub.json> broker a protected publish
  interlock simulate <policy.json> <reqs.jsonl> <run_id> -o <receipts.jsonl>   decide a stream → receipt chain
  interlock replay <policy.json> <reqs.jsonl> <receipts.jsonl>   verify a decision chain
  interlock doctor                           report environment readiness
  interlock verify [--format text|json|markdown]   run the release proof
  interlock version                          print the release version, commit, and protocols
`)
}

// cmdCompile builds and runs a policy module, capturing its canonical IR on
// stdout. This realizes "Go authors, IR decides": the module is executed once to
// emit bytes, never interpreted at decision time.
func cmdCompile(args []string) error {
	var dir, out string
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("compile: -o wants a path")
			}
			out = args[i+1]
			i += 2
		default:
			dir = args[i]
			i++
		}
	}
	if dir == "" {
		return fmt.Errorf("compile: want <dir> (Go module) or <spec.json> (interlock.spec.v1)")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// A regular file is a spec.v1 (or canonical policy.v1) document: compile it
	// in-process, no Go toolchain. This is the language-neutral compile authority
	// and the parity reference every non-Go frontend is checked against.
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		raw, rerr := os.ReadFile(abs)
		if rerr != nil {
			return rerr
		}
		pol, derr := decodePolicy(raw)
		if derr != nil {
			return fmt.Errorf("compile: %w", derr)
		}
		canon, cerr := pol.CanonicalBytes()
		if cerr != nil {
			return cerr
		}
		if out == "" {
			os.Stdout.Write(canon)
			return nil
		}
		return os.WriteFile(out, canon, 0o644)
	}
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = abs
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("compile: running policy module: %w", err)
	}
	// Canonicalize once more so the CLI output is independent of the module's
	// formatting, and validate it round-trips.
	pol, err := decodePolicy(raw)
	if err != nil {
		return fmt.Errorf("compile: policy module emitted invalid IR: %w", err)
	}
	canon, err := pol.CanonicalBytes()
	if err != nil {
		return err
	}
	if out == "" {
		os.Stdout.Write(canon)
		return nil
	}
	return os.WriteFile(out, canon, 0o644)
}

func cmdCheck(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("check: want <policy.json>")
	}
	pol, err := loadPolicy(args[0])
	if err != nil {
		return err
	}
	h, err := pol.Hash()
	if err != nil {
		return err
	}
	fmt.Printf("ok  policy_id=%s  rules=%d  hash=%s\n", pol.PolicyID, len(pol.Rules), h)
	return nil
}

func cmdExplain(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("explain: want <policy.json>")
	}
	pol, err := loadPolicy(args[0])
	if err != nil {
		return err
	}
	h, _ := pol.Hash()
	fmt.Printf("policy %s (%s)\n", pol.PolicyID, h)
	fmt.Printf("actors: %v\n", pol.Actors)
	fmt.Println("resources:")
	for _, r := range pol.Resources {
		fmt.Printf("  %-16s %-8s %s\n", r.ID, r.Kind, r.URI)
	}
	fmt.Println("rules (first match wins):")
	for _, r := range pol.Rules {
		fmt.Printf("  [%s] %s %s on %s %v", r.ID, r.Effect, r.Actor, r.Resource, r.Operations)
		if len(r.Requires) > 0 {
			fmt.Printf(" requires %d", len(r.Requires))
		}
		fmt.Println()
	}
	return nil
}

func cmdDecide(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("decide: want <policy.json> <request.json>")
	}
	pol, err := loadPolicy(args[0])
	if err != nil {
		return err
	}
	var req protocol.EffectRequest
	if err := loadJSON(args[1], &req); err != nil {
		return err
	}
	d := engine.Decide(pol, req)
	return printJSON(d)
}

func cmdPublish(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("publish: want <policy.json> <publish.json>")
	}
	pol, err := loadPolicy(args[0])
	if err != nil {
		return err
	}
	var pr broker.PublishRequest
	if err := loadJSON(args[1], &pr); err != nil {
		return err
	}
	chain := receipt.NewChain(pr.RunID)
	res, perr := broker.Publish(pol, pr, chain)
	// Print the decision/receipt even on denial so the outcome is auditable.
	_ = printJSON(res)
	return perr
}

// cmdSimulate decides each request in a stream against the policy and records
// the decisions as a hash-linked receipt chain — the input `replay` verifies.
func cmdSimulate(args []string) error {
	var positional []string
	var out string
	i := 0
	for i < len(args) {
		if args[i] == "-o" {
			if i+1 >= len(args) {
				return fmt.Errorf("simulate: -o wants a path")
			}
			out = args[i+1]
			i += 2
			continue
		}
		positional = append(positional, args[i])
		i++
	}
	if len(positional) != 3 || out == "" {
		return fmt.Errorf("simulate: want <policy.json> <requests.jsonl> <run_id> -o <receipts.jsonl>")
	}
	pol, err := loadPolicy(positional[0])
	if err != nil {
		return err
	}
	runID := positional[2]
	chain := receipt.NewChain(runID)
	if err := loadJSONL(positional[1], func(b []byte) error {
		var r protocol.EffectRequest
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		if r.RunID == "" {
			r.RunID = runID
		}
		_, aerr := chain.Append(pol, r, engine.Decide(pol, r))
		return aerr
	}); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, rc := range chain.Receipts {
		if err := enc.Encode(rc); err != nil {
			return err
		}
	}
	fmt.Printf("ok  simulated %d decisions → %s\n", len(chain.Receipts), out)
	return nil
}

func cmdReplay(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("replay: want <policy.json> <requests.jsonl> <receipts.jsonl>")
	}
	pol, err := loadPolicy(args[0])
	if err != nil {
		return err
	}
	var reqs []protocol.EffectRequest
	if err := loadJSONL(args[1], func(b []byte) error {
		var r protocol.EffectRequest
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		reqs = append(reqs, r)
		return nil
	}); err != nil {
		return err
	}
	var receipts []receipt.Receipt
	if err := loadJSONL(args[2], func(b []byte) error {
		var r receipt.Receipt
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		receipts = append(receipts, r)
		return nil
	}); err != nil {
		return err
	}
	if err := receipt.Replay(pol, reqs, receipts); err != nil {
		return err
	}
	fmt.Printf("ok  chain verified: %d receipts\n", len(receipts))
	return nil
}

func cmdDoctor(args []string) error {
	fmt.Printf("interlock doctor\n")
	fmt.Printf("  version         : %s\n", releaseVersion())
	fmt.Printf("  policy protocol : %s\n", ir.Protocol)
	fmt.Printf("  effect protocol : %s\n", protocol.EffectRequestProtocol)
	fmt.Printf("  receipt schema  : %s\n", receipt.Schema)
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Printf("  go toolchain    : NOT FOUND (needed only for compile / init --authoring go)\n")
	} else {
		fmt.Printf("  go toolchain    : available\n")
	}
	if latest, lerr := latestVersion(resolveGetHost("")); lerr != nil {
		fmt.Printf("  updates         : could not check (offline?)\n")
	} else if upgradeAvailable(releaseVersion(), latest) {
		fmt.Printf("  updates         : newer available %s -> %s (run: interlock upgrade)\n", releaseVersion(), latest)
	} else {
		fmt.Printf("  updates         : up to date (latest %s)\n", latest)
	}
	fmt.Printf("  note            : init --authoring json and test need no toolchain\n")
	return nil
}

// helpers

// decodePolicy turns policy bytes into an ir.Policy, routing on the protocol tag:
// interlock.policy.v1 is already canonical IR (passed through unchanged), while
// interlock.spec.v1 is authoring input that is run through the real compiler —
// the same authority Go authoring uses. This is what lets the toolchain-free
// binary compile a spec.v1 document without `go run`, and makes spec.v1 the
// language-neutral input every consumer (check/explain/decide/test) accepts.
func decodePolicy(b []byte) (ir.Policy, error) {
	var probe struct {
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return ir.Policy{}, err
	}
	switch probe.Protocol {
	case ir.Protocol:
		var p ir.Policy
		if err := json.Unmarshal(b, &p); err != nil {
			return ir.Policy{}, err
		}
		return p, nil
	case spec.Protocol:
		s, err := spec.DecodeToSpec(b)
		if err != nil {
			return ir.Policy{}, err
		}
		return compiler.Compile(s)
	default:
		return ir.Policy{}, fmt.Errorf("unexpected protocol %q (want %q or %q)", probe.Protocol, spec.Protocol, ir.Protocol)
	}
}

func loadPolicy(path string) (ir.Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ir.Policy{}, err
	}
	return decodePolicy(b)
}

func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func loadJSONL(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		buf := make([]byte, len(line))
		copy(buf, line)
		if err := fn(buf); err != nil {
			return err
		}
	}
	return sc.Err()
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

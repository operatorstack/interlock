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
	"github.com/operatorstack/interlock/engine"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
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
	case "doctor":
		err = cmdDoctor(os.Args[2:])
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
  interlock init <dir>                       scaffold a policy module
  interlock compile <dir> [-o policy.json]   build+run a policy module → canonical IR
  interlock check <policy.json>              validate canonical IR and print its hash
  interlock explain <policy.json>            print a human-readable policy summary
  interlock decide <policy.json> <req.json>  evaluate one effect request
  interlock publish <policy.json> <pub.json> broker a protected publish
  interlock simulate <policy.json> <reqs.jsonl> <run_id> -o <receipts.jsonl>   decide a stream → receipt chain
  interlock replay <policy.json> <reqs.jsonl> <receipts.jsonl>   verify a decision chain
  interlock doctor                           report environment readiness
`)
}

// cmdInit scaffolds a minimal, deterministic policy module.
func cmdInit(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("init: want <dir>")
	}
	dir := args[0]
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
		return fmt.Errorf("compile: want <dir>")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
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
	fmt.Printf("  policy protocol : %s\n", ir.Protocol)
	fmt.Printf("  effect protocol : %s\n", protocol.EffectRequestProtocol)
	fmt.Printf("  receipt schema  : %s\n", receipt.Schema)
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Printf("  go toolchain    : NOT FOUND (compile unavailable)\n")
	} else {
		fmt.Printf("  go toolchain    : available\n")
	}
	return nil
}

// helpers

func decodePolicy(b []byte) (ir.Policy, error) {
	var p ir.Policy
	if err := json.Unmarshal(b, &p); err != nil {
		return ir.Policy{}, err
	}
	if p.Protocol != ir.Protocol {
		return ir.Policy{}, fmt.Errorf("unexpected protocol %q (want %q)", p.Protocol, ir.Protocol)
	}
	return p, nil
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

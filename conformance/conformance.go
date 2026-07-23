// Package conformance holds the executable decision vectors for the Interlock
// engine: each line of the embedded JSONL fixtures pairs a policy and an effect
// request with the outcome the engine must produce. The positive file asserts
// intended allows/requires; the negative file asserts denies and faults. The
// suite is the machine-checkable contract for "what the engine decides."
package conformance

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
)

//go:embed fixtures/*.jsonl
var fixtures embed.FS

// Case is one conformance vector.
type Case struct {
	Name    string                 `json:"name"`
	Policy  ir.Policy              `json:"policy"`
	Request protocol.EffectRequest `json:"request"`
	// UsePolicyHash, when true, stamps the request's ClaimedPolicyHash with the
	// live policy hash before deciding — so a vector can exercise
	// policy_hash_match without hard-coding a hash that would drift.
	UsePolicyHash bool             `json:"use_policy_hash"`
	Expect        protocol.Outcome `json:"expect"`
	ExpectRuleID  string           `json:"expect_rule_id"`
}

// Positive loads the vectors expected to allow or require.
func Positive() ([]Case, error) { return load("fixtures/positive.jsonl") }

// Negative loads the vectors expected to deny or fault.
func Negative() ([]Case, error) { return load("fixtures/negative.jsonl") }

func load(path string) ([]Case, error) {
	f, err := fixtures.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cases []Case
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 || b[0] == '#' {
			continue
		}
		var c Case
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		cases = append(cases, c)
	}
	return cases, sc.Err()
}

package main

import (
	"fmt"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
)

// version, commit, and date carry the release identity of the binary. They are
// injected at build time via ldflags (`-X main.version=...`), matching
// GoReleaser's defaults so its stock build config stamps them without extra
// wiring. In a plain `go build` / `go run` checkout they stay at their defaults,
// and buildCommit() falls back to the VCS revision from debug.ReadBuildInfo().
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// releaseVersion returns the injected release version, or "dev" for an
// un-stamped build.
func releaseVersion() string {
	if version == "" {
		return "dev"
	}
	return version
}

// cmdVersion prints the binary's release identity and the protocol/schema
// versions it speaks. This is the "what am I running?" command: after a
// prebuilt-binary install, it reports the exact tag and commit the artifact was
// built from, alongside the wire contracts.
func cmdVersion(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("version: unexpected argument %q", args[0])
	}
	fmt.Printf("interlock %s (%s)\n", releaseVersion(), buildCommit())
	if date != "" {
		fmt.Printf("  built           : %s\n", date)
	}
	fmt.Printf("  policy protocol : %s\n", ir.Protocol)
	fmt.Printf("  effect protocol : %s\n", protocol.EffectRequestProtocol)
	fmt.Printf("  receipt schema  : %s\n", receipt.Schema)
	return nil
}

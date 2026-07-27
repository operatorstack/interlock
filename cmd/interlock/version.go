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
	check := false
	for _, a := range args {
		if a == "--check" {
			check = true
			continue
		}
		return fmt.Errorf("version: unexpected argument %q", a)
	}
	fmt.Printf("interlock %s (%s)\n", releaseVersion(), buildCommit())
	if date != "" {
		fmt.Printf("  built           : %s\n", date)
	}
	fmt.Printf("  policy protocol : %s\n", ir.Protocol)
	fmt.Printf("  effect protocol : %s\n", protocol.EffectRequestProtocol)
	fmt.Printf("  receipt schema  : %s\n", receipt.Schema)
	// --check contacts the front door for a newer release; plain `version` stays offline.
	if check {
		if latest, err := latestVersion(resolveGetHost("")); err != nil {
			fmt.Printf("  updates         : could not check (offline?)\n")
		} else if upgradeAvailable(releaseVersion(), latest) {
			fmt.Printf("  updates         : newer available %s -> %s (run: interlock upgrade)\n", releaseVersion(), latest)
		} else {
			fmt.Printf("  updates         : up to date (latest %s)\n", latest)
		}
	}
	return nil
}

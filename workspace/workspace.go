// Package workspace provides the isolation helpers behind Interlock's strict
// enforcement mode. The honest guarantee is not that Interlock intercepts every
// write — it cannot see a child process's writes — but that the agent runs with
// no write authority over the protected artifact, and a separate broker performs
// the protected effect. This package models that separation on the filesystem: a
// staging area the agent may write, and a protected target the agent cannot.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// Layout describes an isolated run: a staging directory the producing agent may
// write freely, and a protected root the agent must not be able to modify — only
// the broker publishes into it.
type Layout struct {
	Root      string // run root containing both areas
	Staging   string // agent-writable candidate area
	Protected string // broker-only publication area
}

// New creates a Layout rooted at root, making the staging and protected dirs.
// Callers running an agent under isolation are responsible for confining the
// agent's write authority to Staging (e.g. via OS sandboxing); this package
// only lays out the directories and never grants the agent the protected path.
func New(root string) (Layout, error) {
	l := Layout{
		Root:      root,
		Staging:   filepath.Join(root, "staging"),
		Protected: filepath.Join(root, "protected"),
	}
	for _, d := range []string{l.Root, l.Staging, l.Protected} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Layout{}, fmt.Errorf("interlock/workspace: mkdir %s: %w", d, err)
		}
	}
	return l, nil
}

// StagedPath returns the absolute path of a named candidate in staging.
func (l Layout) StagedPath(name string) string {
	return filepath.Join(l.Staging, name)
}

// ProtectedPath returns the absolute path of a named artifact under the
// protected root.
func (l Layout) ProtectedPath(name string) string {
	return filepath.Join(l.Protected, name)
}

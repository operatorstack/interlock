// Package spec is the typed, in-memory description a policy author builds before
// compilation. It is data only: the constructors in the root interlock package
// populate these types; the compiler package lowers them to canonical ir.Policy.
//
// Arbitrary Go may run while constructing a Spec (loops, helpers, tests). None
// of that Go survives into the IR — only the resulting declarations do. This is
// the "Go authors, IR decides" boundary in type form.
package spec

import (
	"github.com/operatorstack/interlock/ir"
)

// Spec is a whole policy under construction.
type Spec struct {
	PolicyID  string
	Actors    []Actor
	Resources []Resource
	Rules     []Rule
}

// Actor is a named principal a rule can bind to.
type Actor struct {
	ID string
}

// Resource is a declared capability target under construction.
type Resource struct {
	ID   string
	Kind ir.ResourceKind
	URI  string
}

// Rule is one decision-table entry under construction. Resource holds a declared
// Resource.ID; the compiler verifies the reference.
type Rule struct {
	ID         string
	Effect     ir.Effect
	Actor      string
	Operations []ir.Operation
	Resource   string
	Requires   []ir.Requirement
	Reason     string
}

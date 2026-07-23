// Package interlock is the code-first authoring surface for Interlock, a
// code-defined, capability-based effect-policy runtime for agents.
//
// The design principle is fixed: Go is the authoring language, canonical IR is
// the execution language. Arbitrary Go may CONSTRUCT a policy (loops, helpers,
// typed constructors, source-level tests). Only framework predicates may DECIDE
// a request inside the trusted engine — there are no arbitrary Go callbacks at
// decision time in V1, so the runtime stays deterministic, hashable, replayable,
// and conformance-testable.
//
//	Go policy source → typed constructors → canonical Effect Policy IR
//	→ pure decision engine → allow/deny/require/fault → effect broker → receipt
//
// Package layout:
//
//	spec       typed, in-memory policy description built by the constructors here
//	compiler   spec → canonical ir.Policy, with structural diagnostics
//	ir         canonical Effect Policy IR: stable bytes + stable SHA-256
//	engine     pure Decide(policy, request) → decision (no I/O, deterministic)
//	protocol   the effect-request / decision wire types
//	receipt    hash-linked decision receipts and replay verification
//	broker     exclusive-publish: verify evidence → stage → atomic promote → receipt
//	workspace  isolation helpers for the strict-mode experiment
//
// This root package re-exports the operation and resource-kind vocabulary and
// the constructors so a policy author writes only against
// github.com/operatorstack/interlock.
package interlock

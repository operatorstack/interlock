# Enforcement model — transport is not authority

Interlock runs in more than one place: a laptop, a local coding agent, a cloud
sandbox, and CI. This document explains what changes between those places and
what does not.

Read [the enforcement boundary](../../README.md#the-enforcement-boundary-what-is-and-isnt-guaranteed)
first. It defines the three modes and the V1 guarantee. This document adds the
operational view: **the transport changes with the environment; the authority
does not change.**

## Two rules

1. **Transport changes.** The transport carries the request to the controller.
   The controller answers `allow` or `deny`. The transport is different in each
   environment.
2. **Authority does not change.** The broker makes the evidence. The gate blocks
   the merge. The authority is the same in every environment.

The controller is transport. The broker and the gate are authority. Do not
confuse the two. The way a decision is *asked* is not the thing that makes the
decision *trustworthy*.

## Where Interlock runs

| Environment | Controller runs | Hooks fire | Extra setup | Result |
|---|---|---|---|---|
| Local (laptop) | On the laptop | Yes | None | Works |
| Local agent (Claude Code, Cursor) | On the laptop | Yes | None | Works |
| Cloud sandbox (Claude Code remote, Devin, Codespaces) | In the sandbox | Yes, after setup | Hydrate the controller. Wire the hooks. | Works after setup |

In every environment the messages stay local (JSON-Lines over stdio). The sandbox
does **not** need network access to decide.

## The authority does not move

### Broker

The broker makes hash-bound evidence. The broker does the protected action. The
controller cannot fake this. The engine compares claims; the broker makes the
claims truthful.

The V1 guarantee is Strict mode: the agent cannot modify the protected artifact,
and only the broker can — and only for a request the compiled policy allows on
truthful evidence. The broker tests enforce this, they do not just assert it. See
the [`broker`](../../broker) package.

### CI gate

The gate runs Interlock in CI. The gate blocks the merge if the check fails. The
gate runs in one environment you control. The gate runs for every change, from
every environment.

## Fail-closed

If the controller does not answer, the result is `deny`. Default deny also applies
when no rule matches. A missing or unreachable controller cannot open the gate.

## Set up the controller

- **Do** put the controller in the sandbox. Keep the messages local.
- **Do not** put the controller on another network. A locked sandbox cannot reach
  it. Then Interlock denies **every** action.

Use a network controller only when a local controller is not possible. Then add
authentication and a latency budget.

## Cloud agents and sandboxes (deferred)

V1 needs no special code for cloud agents. The gate makes every environment safe.
Whatever the sandbox does, the change must pass the same gate to reach `main`.

The transport ergonomics for sandboxes — how a request reaches a controller
inside a remote agent — is future work. It is tracked in
[operatorstack/pitot#18](https://github.com/operatorstack/pitot/issues/18).

## Remember

- Local hooks give fast feedback. They are not the guarantee.
- The gate gives the guarantee. It runs in one place you control.
- Cloud agents need no special code. Every change must pass the same gate.

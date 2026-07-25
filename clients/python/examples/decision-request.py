#!/usr/bin/env python3
# decision-request.py shows the winning cross-language ergonomic in Python: a
# caller names the resource by its POLICY ID and gets a typed EffectRequest with
# the URI+kind resolved from the policy — never restating a resource's URI/kind and
# never hand-writing the request JSON. The request is emitted with the same
# canonical encoder the hash is computed from.
#
# Authority boundary — read this before extending the example. This client does
# NOT decide and does NOT enforce. It builds a typed, canonically-encoded request.
# The decision is made by the Go engine (behind the interlock-pitot controller),
# and the protected effect is performed by the Go broker. Porting the decision
# engine or the broker into Python is an explicit non-goal: the canonical IR is the
# execution language, Go is the trusted executable. So this file resolves a
# resource and shapes a request; it makes no allow/deny claim.
#
# Run: python examples/decision-request.py   (Python >= 3.11)
# Exits non-zero on any mismatch.
from __future__ import annotations

import json
import sys
from pathlib import Path

try:
    from interlock_protocol.canonical import canonical_bytes
    from interlock_protocol.protocol import EffectRequest, Policy, TargetResource
except ModuleNotFoundError:  # pragma: no cover - convenience for uninstalled runs
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))
    from interlock_protocol.canonical import canonical_bytes
    from interlock_protocol.protocol import EffectRequest, Policy, TargetResource

# The corpus lives three directories up from clients/python/examples.
CORPUS = Path(__file__).resolve().parent.parent.parent.parent / "conformance" / "compat" / "v0.1.0"


def resolve_resource(policy: Policy, resource_id: str) -> TargetResource:
    """Python mirror of Go's ir.Policy.ResolveResource.

    Name the resource once (in the policy) and look up its URI+kind here, rather
    than restating them at the call site. It fails closed exactly like Go: an
    unknown id raises (no default) and a duplicate id raises (no arbitrary pick),
    because a silently-wrong target is the one mistake a policy engine must never
    make.
    """
    matches = [r for r in policy["resources"] if r["id"] == resource_id]
    if not matches:
        raise ValueError(
            f'interlock: resource id "{resource_id}" not declared in policy "{policy["policy_id"]}"'
        )
    if len(matches) > 1:
        raise ValueError(
            f'interlock: resource id "{resource_id}" declared more than once in policy "{policy["policy_id"]}"'
        )
    return {"kind": matches[0]["kind"], "uri": matches[0]["uri"]}


def build_effect_request(
    policy: Policy,
    *,
    run_id: str,
    request_id: str,
    actor: str,
    operation: str,
    resource_id: str,
    claimed_policy_hash: str | None = None,
    evidence: list | None = None,
    source: str = "interlock-python-client",
    fidelity: str = "brokered",
) -> EffectRequest:
    """Assemble the typed EffectRequest, resolving the resource by id.

    Pure: no I/O, no decision. This is the exact value that would be canonically
    encoded and sent to the Go controller. Observation defaults to a brokered
    decision-transport reading, matching the Go client (client.EffectRequest).
    """
    req: EffectRequest = {
        "protocol": "interlock.effect.v1",
        "request_id": request_id,
        "run_id": run_id,
        "actor": actor,
        "operation": operation,
        "resource": resolve_resource(policy, resource_id),
        "observation": {"source": source, "fidelity": fidelity},
    }
    if claimed_policy_hash is not None:
        req["claimed_policy_hash"] = claimed_policy_hash
    if evidence is not None:
        req["evidence"] = evidence
    return req


def main() -> int:
    policy: Policy = json.loads(
        (CORPUS / "policies" / "exclusive-publish.json").read_text(encoding="utf-8")
    )

    failures = 0

    def check(ok: bool, msg: str) -> None:
        nonlocal failures
        print(f"{'PASS' if ok else 'FAIL'}  {msg}")
        if not ok:
            failures += 1

    # The win: the publisher publishes the "artifact" resource named by id. The URI
    # and kind are never typed by the caller — they come from the policy.
    req = build_effect_request(
        policy,
        run_id="run1",
        request_id="r1",
        actor="publisher",
        operation="artifact.publish",
        resource_id="artifact",
        claimed_policy_hash="sha256:edf4ed0d10b1aa9c0c2a0301688b3c97e34f6c0fc78502f4303466adb4ea82b3",
        evidence=[
            {"kind": "staged_hash_match"},
            {"kind": "receipt_status", "receipt": "deltawire.supervision.receipt.v1", "status": "released"},
        ],
    )

    check(
        req["resource"]["uri"] == "repo://out/result.json" and req["resource"]["kind"] == "file",
        "resource resolved from policy id (URI/kind never restated at call site)",
    )
    check(req["observation"]["fidelity"] == "brokered", "observation defaults to a brokered decision reading")
    check(req["protocol"] == "interlock.effect.v1", "protocol tag stamped")

    # Canonically encodable — the same deterministic bytes Go and TypeScript
    # produce, ready to transport. Byte-for-byte cross-language parity is proven by
    # parity.py against the frozen corpus.
    wire = canonical_bytes(req)
    check(len(wire) > 0, "typed request canonically encodes (no hand-written JSON)")

    # Fail-closed: an undeclared resource id is an error, never a silent empty target.
    threw = False
    try:
        build_effect_request(
            policy, run_id="r", request_id="r", actor="publisher",
            operation="artifact.publish", resource_id="nope",
        )
    except ValueError:
        threw = True
    check(threw, "unknown resource id fails closed (mirrors Go ir.ResolveResource)")

    print("\nRESULT: PASS" if failures == 0 else f"\nRESULT: FAIL ({failures})")
    return 0 if failures == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())

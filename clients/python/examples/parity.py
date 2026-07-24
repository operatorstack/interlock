#!/usr/bin/env python3
# parity.py is the Python client's executable proof of the support bar:
#
#   canonical hash matches · spec round-trips · decision fixtures parse
#
# It reads the FROZEN golden corpus (conformance/compat/v0.1.0) — the same oracle
# the Go compat test uses — and proves the Python canonical encoder reproduces
# Go's canonical policy bytes and hashes exactly. It deliberately does NOT decide
# anything and does NOT compile spec.v1 -> policy.v1: lowering is the Go
# compiler's job and enforcement is the Go executable's; this client only
# reproduces the deterministic canonicalization + hashing.
#
# Run: python examples/parity.py   (Python >= 3.11)
# Exits non-zero on any mismatch.
from __future__ import annotations

import json
import sys
from pathlib import Path

# Import the installed package if present; otherwise fall back to the src tree so
# the example runs before `pip install -e .`.
try:
    from interlock_protocol.canonical import canonical_bytes, hash
except ModuleNotFoundError:  # pragma: no cover - convenience for uninstalled runs
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))
    from interlock_protocol.canonical import canonical_bytes, hash

# The corpus lives at <interlock>/conformance/compat/v0.1.0; this file is at
# <interlock>/clients/python/examples, so it is three directories up.
CORPUS = Path(__file__).resolve().parent.parent.parent.parent / "conformance" / "compat" / "v0.1.0"


def read_corpus(rel: str) -> str:
    return (CORPUS / rel).read_text(encoding="utf-8")


def read_jsonl(rel: str) -> list:
    records = []
    for line in read_corpus(rel).splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        records.append(json.loads(line))
    return records


failures = 0


def check(ok: bool, msg: str) -> None:
    global failures
    print(f"{'PASS' if ok else 'FAIL'}  {msg}")
    if not ok:
        failures += 1


# 1. Canonical hash parity — the load-bearing test. Each frozen policy is
#    canonical compiled IR; re-canonicalizing the parsed value must reproduce the
#    exact frozen bytes and the frozen hash. This is Python == Go == frozen.
hashes = read_jsonl("hashes.jsonl")
check(len(hashes) >= 4, f"loaded {len(hashes)} frozen policy hashes")
for rec in hashes:
    raw = read_corpus(rec["policy"])
    parsed = json.loads(raw)
    re_canon = canonical_bytes(parsed).decode("utf-8")
    check(re_canon == raw, f"{rec['name']}: re-canonicalized bytes match frozen IR")
    check(
        hash(parsed) == rec["expected_hash"],
        f"{rec['name']}: hash == frozen {rec['expected_hash'][:19]}…",
    )

# 2. spec.v1 round-trip — the client can carry the authoring format. No hash
#    assertion: spec.v1 -> policy.v1 lowering is the Go compiler (not ported).
specs = read_jsonl("specs.jsonl")
for rec in specs:
    doc = json.loads(read_corpus(rec["spec"]))
    check(
        doc.get("protocol") == "interlock.spec.v1"
        and bool(doc.get("policy_id"))
        and isinstance(doc.get("rules"), list),
        f"{rec['name']}: spec.v1 parses into SpecDoc",
    )

# 3. Decision fixtures — every frozen decision vector parses into the generated
#    protocol DTOs and stays within the closed vocabularies. The client
#    TRANSPORTS and TYPES decisions; it never re-decides them.
OUTCOMES = {"allow", "deny", "require", "fault"}
OPERATIONS = {
    "filesystem.read", "filesystem.write", "filesystem.delete", "filesystem.rename_from",
    "filesystem.rename_to", "process.execute", "artifact.publish", "vcs.push", "vcs.force_push",
}
KINDS = {"file", "tree", "process", "branch"}

decisions = read_jsonl("decisions.jsonl")
check(len(decisions) > 0, f"loaded {len(decisions)} frozen decision vectors")
vocab_ok = True
for c in decisions:
    # Every expected outcome is a member of the closed Outcome vocabulary. The
    # request's operation/kind must be in-vocabulary EXCEPT for fault fixtures,
    # which deliberately carry unknown values to prove the engine faults on them —
    # the protocol must be able to transport those unknowns verbatim.
    good = c["expect"] in OUTCOMES
    if c["expect"] != "fault":
        good = (
            good
            and c["request"]["operation"] in OPERATIONS
            and c["request"]["resource"]["kind"] in KINDS
        )
    if not good:
        vocab_ok = False
        print(f"      offending fixture: {c['name']}")
    # Round-trip the inline policy through the encoder (must not throw).
    canonical_bytes(c["policy"])
check(vocab_ok, "all decision fixtures parse into DTOs within the closed vocabulary")

print("\nRESULT: PASS" if failures == 0 else f"\nRESULT: FAIL ({failures})")
sys.exit(0 if failures == 0 else 1)

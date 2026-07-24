// parity.ts is the TypeScript client's executable proof of the support bar:
//
//   canonical hash matches · spec round-trips · decision fixtures parse
//
// It reads the FROZEN golden corpus (conformance/compat/v0.1.0) — the same
// oracle the Go compat test uses — and proves the TypeScript canonical encoder
// reproduces Go's canonical policy bytes and hashes exactly. It deliberately
// does NOT decide anything and does NOT compile spec.v1 → policy.v1: lowering is
// the Go compiler's job and enforcement is the Go executable's; this client only
// reproduces the deterministic canonicalization + hashing.
//
// Run: node examples/parity.ts   (Node >= 22, native TypeScript type stripping)
// Exits non-zero on any mismatch.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { canonicalBytes, hash } from "../src/canonical.ts";
import type { Policy, SpecDoc } from "../src/protocol.ts";

// The corpus lives at <interlock>/conformance/compat/v0.1.0; this file is at
// <interlock>/clients/typescript/examples, so it is three directories up.
const CORPUS = join(import.meta.dirname, "..", "..", "..", "conformance", "compat", "v0.1.0");

function readCorpus(rel: string): string {
  return readFileSync(join(CORPUS, rel), "utf8");
}

function readJsonl(rel: string): unknown[] {
  return readCorpus(rel)
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.length > 0 && !l.startsWith("#"))
    .map((l) => JSON.parse(l));
}

let failures = 0;
function check(ok: boolean, msg: string): void {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}`);
  if (!ok) failures++;
}

// 1. Canonical hash parity — the load-bearing test. Each frozen policy is
//    canonical compiled IR; re-canonicalizing the parsed value must reproduce
//    the exact frozen bytes and the frozen hash. This is TS == Go == frozen.
interface HashRecord {
  name: string;
  policy: string;
  expected_hash: string;
}
const hashes = readJsonl("hashes.jsonl") as HashRecord[];
check(hashes.length >= 4, `loaded ${hashes.length} frozen policy hashes`);
for (const rec of hashes) {
  const raw = readCorpus(rec.policy);
  const parsed = JSON.parse(raw) as Policy;
  const reCanon = new TextDecoder().decode(canonicalBytes(parsed));
  check(reCanon === raw, `${rec.name}: re-canonicalized bytes match frozen IR`);
  check(
    hash(parsed) === rec.expected_hash,
    `${rec.name}: hash == frozen ${rec.expected_hash.slice(0, 19)}…`,
  );
}

// 2. spec.v1 round-trip — the client can carry the authoring format. No hash
//    assertion: spec.v1 → policy.v1 lowering is the Go compiler (not ported).
interface SpecRecord {
  name: string;
  spec: string;
  expected_hash: string;
}
const specs = readJsonl("specs.jsonl") as SpecRecord[];
for (const rec of specs) {
  const doc = JSON.parse(readCorpus(rec.spec)) as SpecDoc;
  check(
    doc.protocol === "interlock.spec.v1" && !!doc.policy_id && Array.isArray(doc.rules),
    `${rec.name}: spec.v1 parses into SpecDoc`,
  );
}

// 3. Decision fixtures — every frozen decision vector parses into the generated
//    protocol DTOs and stays within the closed vocabularies. The client
//    TRANSPORTS and TYPES decisions; it never re-decides them.
const OUTCOMES = new Set(["allow", "deny", "require", "fault"]);
const OPERATIONS = new Set([
  "filesystem.read", "filesystem.write", "filesystem.delete", "filesystem.rename_from",
  "filesystem.rename_to", "process.execute", "artifact.publish", "vcs.push", "vcs.force_push",
]);
const KINDS = new Set(["file", "tree", "process", "branch"]);
interface DecisionCase {
  name: string;
  policy: Policy;
  request: { operation: string; resource: { kind: string } };
  expect: string;
}
const decisions = readJsonl("decisions.jsonl") as DecisionCase[];
check(decisions.length > 0, `loaded ${decisions.length} frozen decision vectors`);
let vocabOK = true;
for (const c of decisions) {
  // Every expected outcome is a member of the closed Outcome vocabulary. The
  // request's operation/kind must be in-vocabulary EXCEPT for fault fixtures,
  // which deliberately carry unknown values to prove the engine faults on them —
  // the protocol must be able to transport those unknowns verbatim.
  let good = OUTCOMES.has(c.expect);
  if (c.expect !== "fault") {
    good = good && OPERATIONS.has(c.request.operation) && KINDS.has(c.request.resource.kind);
  }
  if (!good) {
    vocabOK = false;
    console.log(`      offending fixture: ${c.name}`);
  }
  // Round-trip the inline policy through the encoder (must not throw).
  canonicalBytes(c.policy);
}
check(vocabOK, "all decision fixtures parse into DTOs within the closed vocabulary");

console.log(failures === 0 ? "\nRESULT: PASS" : `\nRESULT: FAIL (${failures})`);
process.exit(failures === 0 ? 0 : 1);

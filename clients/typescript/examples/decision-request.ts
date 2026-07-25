// decision-request.ts shows the winning cross-language ergonomic in TypeScript:
// a caller names the resource by its POLICY ID and gets a typed EffectRequest with
// the URI+kind resolved from the policy — never restating a resource's URI/kind and
// never hand-writing the request JSON. The request is emitted with the same
// canonical encoder the hash is computed from.
//
// Authority boundary — read this before extending the example. This client does
// NOT decide and does NOT enforce. It builds a typed, canonically-encoded request
// DTO. The decision is made by the Go engine (behind the interlock-pitot
// controller), and the protected effect is performed by the Go broker. Porting the
// decision engine or the broker into TypeScript is an explicit non-goal: the
// canonical IR is the execution language, Go is the trusted executable. So this
// file resolves a resource and shapes a request; it makes no allow/deny claim.
//
// Run: node examples/decision-request.ts   (Node >= 22, native type stripping)
// Exits non-zero on any mismatch.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { canonicalBytes } from "../src/canonical.ts";
import type {
  EffectRequest,
  Fidelity,
  Operation,
  Policy,
  TargetResource,
} from "../src/protocol.ts";

// resolveResource is the TS mirror of Go's ir.Policy.ResolveResource: name the
// resource once (in the policy) and look up its URI+kind here, rather than
// restating them at the call site. It fails closed exactly like Go — an unknown id
// throws (no default) and a duplicate id throws (no arbitrary pick) — because a
// silently-wrong target is the one mistake a policy engine must never make.
function resolveResource(policy: Policy, id: string): TargetResource {
  const matches = policy.resources.filter((r) => r.id === id);
  if (matches.length === 0) {
    throw new Error(`interlock: resource id "${id}" not declared in policy "${policy.policy_id}"`);
  }
  if (matches.length > 1) {
    throw new Error(`interlock: resource id "${id}" declared more than once in policy "${policy.policy_id}"`);
  }
  return { kind: matches[0].kind, uri: matches[0].uri };
}

// DecisionInput is the small, honest set of facts a caller supplies. The resource
// is named by id; observation defaults to a brokered decision-transport reading,
// matching the Go client (client.EffectRequest).
interface DecisionInput {
  runId: string;
  requestId: string;
  actor: string;
  operation: Operation;
  resourceId: string;
  claimedPolicyHash?: string;
  evidence?: EffectRequest["evidence"];
  source?: string;
  fidelity?: Fidelity;
}

// buildEffectRequest assembles the typed EffectRequest, resolving the resource by
// id. It is pure: no I/O, no decision. This is the exact value that would be
// canonically encoded and sent to the Go controller.
function buildEffectRequest(policy: Policy, input: DecisionInput): EffectRequest {
  return {
    protocol: "interlock.effect.v1",
    request_id: input.requestId,
    run_id: input.runId,
    actor: input.actor,
    operation: input.operation,
    resource: resolveResource(policy, input.resourceId),
    observation: {
      source: input.source ?? "interlock-ts-client",
      fidelity: input.fidelity ?? "brokered",
    },
    ...(input.claimedPolicyHash ? { claimed_policy_hash: input.claimedPolicyHash } : {}),
    ...(input.evidence ? { evidence: input.evidence } : {}),
  };
}

// The corpus lives three directories up from clients/typescript/examples.
const CORPUS = join(import.meta.dirname, "..", "..", "..", "conformance", "compat", "v0.1.0");
const policy = JSON.parse(
  readFileSync(join(CORPUS, "policies", "exclusive-publish.json"), "utf8"),
) as Policy;

let failures = 0;
function check(ok: boolean, msg: string): void {
  console.log(`${ok ? "PASS" : "FAIL"}  ${msg}`);
  if (!ok) failures++;
}

// The win: the publisher publishes the "artifact" resource named by id. The URI
// and kind are never typed by the caller — they come from the policy.
const req = buildEffectRequest(policy, {
  runId: "run1",
  requestId: "r1",
  actor: "publisher",
  operation: "artifact.publish",
  resourceId: "artifact",
  claimedPolicyHash: "sha256:edf4ed0d10b1aa9c0c2a0301688b3c97e34f6c0fc78502f4303466adb4ea82b3",
  evidence: [
    { kind: "staged_hash_match" },
    { kind: "receipt_status", receipt: "deltawire.supervision.receipt.v1", status: "released" },
  ],
});

check(
  req.resource.uri === "repo://out/result.json" && req.resource.kind === "file",
  "resource resolved from policy id (URI/kind never restated at call site)",
);
check(req.observation.fidelity === "brokered", "observation defaults to a brokered decision reading");
check(req.protocol === "interlock.effect.v1", "protocol tag stamped");

// The request is canonically encodable — the same deterministic bytes Go and
// Python produce, ready to transport. We assert it encodes without throwing and is
// non-empty; byte-for-byte cross-language parity is proven by parity.ts against the
// frozen corpus.
const wire = canonicalBytes(req);
check(wire.length > 0, "typed request canonically encodes (no hand-written JSON)");

// Fail-closed: an undeclared resource id is an error, never a silent empty target.
let threw = false;
try {
  buildEffectRequest(policy, { runId: "r", requestId: "r", actor: "publisher", operation: "artifact.publish", resourceId: "nope" });
} catch {
  threw = true;
}
check(threw, "unknown resource id fails closed (mirrors Go ir.ResolveResource)");

console.log(failures === 0 ? "\nRESULT: PASS" : `\nRESULT: FAIL (${failures})`);
process.exit(failures === 0 ? 0 : 1);

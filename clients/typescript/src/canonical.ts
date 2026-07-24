// canonical.ts reproduces Interlock's ONE canonicalization authority
// (ir.Canonical / ir.writeCanonical in Go) byte-for-byte, so a policy authored
// or transported in TypeScript hashes identically to the Go implementation and
// to the frozen golden corpus. This is deliberately a re-implementation of a
// *deterministic* function — NOT of enforcement. There is no Decide and no
// broker here, and there must never be: enforcement stays the trusted Go
// executable (see the "no foreign enforcement" guardrail).
//
// The scheme (must match ir/ir.go exactly):
//   - object keys sorted by UTF-8 byte order (Go sort.Strings), recursively;
//   - no insignificant whitespace;
//   - each string/key escaped like Go's encoding/json default (HTML-escape ON):
//     " \\ \n \r \t as short escapes; < > & and other control chars < 0x20 and
//     U+2028 / U+2029 as \uXXXX (lowercase hex); everything else verbatim UTF-8;
//   - a trailing newline;
//   - hash = "sha256:" + hex(sha256(canonicalBytes)).

import { createHash } from "node:crypto";

const encoder = new TextEncoder();

// canonicalBytes renders a parsed JSON value (object / array / string / number /
// boolean / null) to Interlock canonical bytes. Input is plain JSON data, never
// a class instance — the generated DTOs are structural and parse straight to it.
export function canonicalBytes(value: unknown): Uint8Array {
  return encoder.encode(serialize(value) + "\n");
}

// hash returns the canonical identity of a value: "sha256:" + hex digest of its
// canonical bytes, matching ir.Policy.Hash / ir.HashBytes tagging.
export function hash(value: unknown): string {
  const digest = createHash("sha256").update(canonicalBytes(value)).digest("hex");
  return "sha256:" + digest;
}

function serialize(v: unknown): string {
  if (v === null || v === undefined) return "null";
  switch (typeof v) {
    case "boolean":
      return v ? "true" : "false";
    case "number":
      return encodeNumber(v);
    case "string":
      return encodeString(v);
    case "object":
      if (Array.isArray(v)) {
        return "[" + v.map(serialize).join(",") + "]";
      }
      return encodeObject(v as Record<string, unknown>);
    default:
      throw new Error(`interlock/canonical: unsupported value type ${typeof v}`);
  }
}

function encodeObject(obj: Record<string, unknown>): string {
  const keys = Object.keys(obj).sort(compareUtf8);
  let out = "{";
  for (let i = 0; i < keys.length; i++) {
    if (i > 0) out += ",";
    out += encodeString(keys[i]) + ":" + serialize(obj[keys[i]]);
  }
  return out + "}";
}

// compareUtf8 orders two strings by their UTF-8 byte sequences, matching Go's
// sort.Strings (which compares bytes). For ASCII keys — all the corpus uses —
// this is identical to a code-unit compare, but we do the real thing so keys
// with multibyte characters still sort exactly as Go would.
function compareUtf8(a: string, b: string): number {
  const ab = encoder.encode(a);
  const bb = encoder.encode(b);
  const n = Math.min(ab.length, bb.length);
  for (let i = 0; i < n; i++) {
    if (ab[i] !== bb[i]) return ab[i] - bb[i];
  }
  return ab.length - bb.length;
}

// encodeNumber preserves integer literals; Interlock's corpus contains no
// numbers, but a Receipt.sequence transported through these types stays exact.
// (Go preserves the JSON number literal via json.Number; JSON.parse has already
// collapsed it to a double by the time we see it, so integers round-trip and
// non-integers are emitted with JS's shortest round-trip form. This branch is
// not exercised by the parity corpus.)
function encodeNumber(n: number): string {
  if (!Number.isFinite(n)) {
    throw new Error("interlock/canonical: non-finite number is not valid JSON");
  }
  return String(n);
}

const HEX = "0123456789abcdef";

// encodeString matches Go encoding/json's default string encoder with HTML
// escaping enabled (the mode ir.go's json.Marshal uses).
function encodeString(s: string): string {
  let out = '"';
  for (const ch of s) {
    const code = ch.codePointAt(0)!;
    switch (ch) {
      case '"':
        out += '\\"';
        continue;
      case "\\":
        out += "\\\\";
        continue;
      case "\n":
        out += "\\n";
        continue;
      case "\r":
        out += "\\r";
        continue;
      case "\t":
        out += "\\t";
        continue;
      case "<":
        out += "\\u003c";
        continue;
      case ">":
        out += "\\u003e";
        continue;
      case "&":
        out += "\\u0026";
        continue;
    }
    if (code < 0x20) {
      out += "\\u00" + HEX[(code >> 4) & 0xf] + HEX[code & 0xf];
      continue;
    }
    // U+2028 LINE SEPARATOR / U+2029 PARAGRAPH SEPARATOR: Go escapes these so the
    // output is safe to embed in JavaScript. Matched by code point to avoid
    // embedding the raw characters in this source file.
    if (code === 0x2028 || code === 0x2029) {
      out += "\\u20" + HEX[(code >> 4) & 0xf] + HEX[code & 0xf];
      continue;
    }
    out += ch;
  }
  return out + '"';
}

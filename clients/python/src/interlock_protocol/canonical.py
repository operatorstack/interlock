# canonical.py reproduces Interlock's ONE canonicalization authority
# (ir.Canonical / ir.writeCanonical in Go) byte-for-byte, so a policy authored
# or transported in Python hashes identically to the Go implementation and to the
# frozen golden corpus. This is deliberately a re-implementation of a
# *deterministic* function — NOT of enforcement. There is no decide and no broker
# here, and there must never be: enforcement stays the trusted Go executable
# (see the "no foreign enforcement" guardrail).
#
# The scheme (must match ir/ir.go exactly):
#   - object keys sorted by UTF-8 byte order (Go sort.Strings), recursively;
#   - no insignificant whitespace;
#   - each string/key escaped like Go's encoding/json default (HTML-escape ON):
#     " \\ \n \r \t as short escapes; < > & and other control chars < 0x20 and
#     U+2028 / U+2029 as \uXXXX (lowercase hex); everything else verbatim UTF-8;
#   - a trailing newline;
#   - hash = "sha256:" + hex(sha256(canonical_bytes)).
#
# Stdlib only: hashlib for the digest, and the json module solely to PARSE inputs
# at call sites — the serializer below is hand-written because json.dumps does not
# reproduce Go's HTML-escaping or byte-order key sort.
from __future__ import annotations

import hashlib

__all__ = ["canonical_bytes", "hash"]


def canonical_bytes(value: object) -> bytes:
    """Render a parsed JSON value (dict / list / str / int / float / bool / None)
    to Interlock canonical bytes. Input is plain JSON data, never a class
    instance — the generated TypedDicts are structural and parse straight to it.
    """
    return (_serialize(value) + "\n").encode("utf-8")


def hash(value: object) -> str:
    """Return the canonical identity of a value: "sha256:" + hex digest of its
    canonical bytes, matching ir.Policy.Hash / ir.HashBytes tagging.
    """
    digest = hashlib.sha256(canonical_bytes(value)).hexdigest()
    return "sha256:" + digest


def _serialize(v: object) -> str:
    if v is None:
        return "null"
    # bool is a subclass of int in Python — must be checked BEFORE int.
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, str):
        return _encode_string(v)
    if isinstance(v, int):
        return str(v)
    if isinstance(v, float):
        return _encode_float(v)
    if isinstance(v, list):
        return "[" + ",".join(_serialize(x) for x in v) + "]"
    if isinstance(v, dict):
        return _encode_object(v)
    raise TypeError(f"interlock/canonical: unsupported value type {type(v).__name__}")


def _encode_object(obj: dict) -> str:
    # Sort keys by their UTF-8 byte sequences, matching Go's sort.Strings (which
    # compares bytes). Python compares bytes objects lexicographically by byte
    # value, so this reproduces Go exactly even for multibyte keys. For ASCII
    # keys — all the corpus uses — it coincides with an ordinary string sort.
    keys = sorted(obj.keys(), key=lambda k: k.encode("utf-8"))
    return "{" + ",".join(_encode_string(k) + ":" + _serialize(obj[k]) for k in keys) + "}"


def _encode_float(n: float) -> str:
    # Interlock's corpus contains no numbers; this branch is not exercised by the
    # parity corpus. Reject non-finite values (not valid JSON) and emit the
    # shortest round-trip form otherwise.
    if n != n or n in (float("inf"), float("-inf")):
        raise ValueError("interlock/canonical: non-finite number is not valid JSON")
    return repr(n)


_HEX = "0123456789abcdef"

# Short escapes matching Go encoding/json's default string encoder.
_SHORT = {
    '"': '\\"',
    "\\": "\\\\",
    "\n": "\\n",
    "\r": "\\r",
    "\t": "\\t",
    "<": "\\u003c",
    ">": "\\u003e",
    "&": "\\u0026",
}


def _encode_string(s: str) -> str:
    out = ['"']
    for ch in s:
        short = _SHORT.get(ch)
        if short is not None:
            out.append(short)
            continue
        code = ord(ch)
        if code < 0x20:
            out.append("\\u00" + _HEX[(code >> 4) & 0xF] + _HEX[code & 0xF])
            continue
        # U+2028 LINE SEPARATOR / U+2029 PARAGRAPH SEPARATOR: Go escapes these so
        # the output is safe to embed in JavaScript.
        if code == 0x2028 or code == 0x2029:
            out.append("\\u20" + _HEX[(code >> 4) & 0xF] + _HEX[code & 0xF])
            continue
        out.append(ch)
    out.append('"')
    return "".join(out)

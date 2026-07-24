//go:build ignore

// Command gen is the single source of truth for Interlock's cross-language
// protocol surface. It reflects over the Go wire structs (the authority for what
// the protocol IS) and emits, from those structs alone:
//
//   - clients/schema/interlock.schema.json   a JSON Schema (draft 2020-12) for
//     interlock.spec.v1 plus every protocol.* / ir.* / receipt / broker wire type;
//   - clients/typescript/src/protocol.ts      generated TypeScript DTOs;
//   - clients/python/src/interlock_protocol/protocol.py   generated Python DTOs.
//
// These are DATA TYPES ONLY — no decide, no publish, no broker. Enforcement stays
// the trusted Go executable; foreign languages get the shapes and (hand-written,
// parity-gated) canonical encoder, never the decision or broker logic.
//
// Regenerate:
//
//	go run ./clients/gen/main.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/operatorstack/interlock/broker"
	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/protocol"
	"github.com/operatorstack/interlock/receipt"
	"github.com/operatorstack/interlock/spec"
)

// enumValues maps each named string "enum" type to its closed vocabulary, in
// canonical order. These are the frozen V1 vocabularies; the generator refuses to
// emit a string field of an unregistered named type so a new enum can never slip
// through untyped.
var enumValues = map[reflect.Type][]string{
	reflect.TypeOf(ir.Operation("")):       toStrings(ir.Operations),
	reflect.TypeOf(ir.ResourceKind("")):    toStrings(ir.ResourceKinds),
	reflect.TypeOf(ir.Effect("")):          {string(ir.EffectAllow), string(ir.EffectDeny)},
	reflect.TypeOf(ir.RequirementKind("")): {string(ir.ReqReceiptStatus), string(ir.ReqStagedHashMatch), string(ir.ReqPolicyHashMatch), string(ir.ReqTargetHashMatch), string(ir.ReqHumanApproval)},
	reflect.TypeOf(protocol.Fidelity("")):  {string(protocol.FidelityObserved), string(protocol.FidelityOpaque), string(protocol.FidelityBrokered)},
	reflect.TypeOf(protocol.Outcome("")):   {string(protocol.OutcomeAllow), string(protocol.OutcomeDeny), string(protocol.OutcomeRequire), string(protocol.OutcomeFault)},
}

// rootStructs are the wire types emitted, in file order. Nested struct fields
// must resolve to a type in this list (checked at generation time).
var rootStructs = []reflect.Type{
	reflect.TypeOf(spec.SpecDoc{}),
	reflect.TypeOf(spec.ResourceDoc{}),
	reflect.TypeOf(spec.RuleDoc{}),
	reflect.TypeOf(ir.Policy{}),
	reflect.TypeOf(ir.Resource{}),
	reflect.TypeOf(ir.Rule{}),
	reflect.TypeOf(ir.Requirement{}),
	reflect.TypeOf(protocol.TargetResource{}),
	reflect.TypeOf(protocol.Observation{}),
	reflect.TypeOf(protocol.Evidence{}),
	reflect.TypeOf(protocol.EffectRequest{}),
	reflect.TypeOf(protocol.Decision{}),
	reflect.TypeOf(receipt.Receipt{}),
	reflect.TypeOf(broker.UpstreamReceipt{}),
	reflect.TypeOf(broker.PublishRequest{}),
}

func main() {
	root, err := findRoot()
	must(err)

	structNames := map[reflect.Type]bool{}
	for _, t := range rootStructs {
		structNames[t] = true
	}

	fields := map[reflect.Type][]fieldInfo{}
	for _, t := range rootStructs {
		fields[t] = analyze(t, structNames)
	}

	writeFile(filepath.Join(root, "clients/schema/interlock.schema.json"), renderSchema(fields))
	writeFile(filepath.Join(root, "clients/typescript/src/protocol.ts"), renderTS(fields))
	writeFile(filepath.Join(root, "clients/python/src/interlock_protocol/protocol.py"), renderPython(fields))
	fmt.Println("generated protocol types for schema / typescript / python")
}

// fieldInfo is one struct field's resolved shape.
type fieldInfo struct {
	json     string
	optional bool   // json:",omitempty"
	kind     string // "string" | "int" | "bool" | "enum" | "struct" | "array"
	ref      string // enum/struct type name, when kind is enum/struct
	elem     *fieldInfo
}

func analyze(t reflect.Type, structNames map[reflect.Type]bool) []fieldInfo {
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || name == "" {
			continue
		}
		fi := resolve(f.Type, structNames)
		fi.json = name
		fi.optional = strings.Contains(opts, "omitempty")
		out = append(out, fi)
	}
	return out
}

func resolve(t reflect.Type, structNames map[reflect.Type]bool) fieldInfo {
	switch t.Kind() {
	case reflect.String:
		if _, ok := enumValues[t]; ok {
			return fieldInfo{kind: "enum", ref: t.Name()}
		}
		if t.Name() != "" && t.Name() != "string" {
			fail("string type %q is not a registered enum; add it to enumValues", t.String())
		}
		return fieldInfo{kind: "string"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return fieldInfo{kind: "int"}
	case reflect.Bool:
		return fieldInfo{kind: "bool"}
	case reflect.Slice:
		elem := resolve(t.Elem(), structNames)
		return fieldInfo{kind: "array", elem: &elem}
	case reflect.Struct:
		if !structNames[t] {
			fail("struct type %q is referenced but not in rootStructs", t.String())
		}
		return fieldInfo{kind: "struct", ref: t.Name()}
	default:
		fail("unsupported field kind %s (%s)", t.Kind(), t.String())
		return fieldInfo{}
	}
}

// --- JSON Schema (draft 2020-12) ------------------------------------------

func renderSchema(fields map[reflect.Type][]fieldInfo) string {
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString(`  "$schema": "https://json-schema.org/draft/2020-12/schema",` + "\n")
	b.WriteString(`  "$id": "https://interlock.operatorstack.dev/schema/interlock.schema.json",` + "\n")
	b.WriteString(`  "title": "Interlock protocol types",` + "\n")
	b.WriteString(`  "$defs": {` + "\n")

	var defs []string
	// Enums first (sorted), then structs in declaration order.
	var enumNames []string
	enumByName := map[string][]string{}
	for t, vals := range enumValues {
		enumByName[t.Name()] = vals
		enumNames = append(enumNames, t.Name())
	}
	sort.Strings(enumNames)
	for _, name := range enumNames {
		var vals []string
		for _, v := range enumByName[name] {
			vals = append(vals, jsonString(v))
		}
		defs = append(defs, fmt.Sprintf("    %s: {\n      \"type\": \"string\",\n      \"enum\": [%s]\n    }", jsonString(name), strings.Join(vals, ", ")))
	}
	for _, t := range rootStructs {
		var props []string
		var required []string
		for _, f := range fields[t] {
			props = append(props, "        "+jsonString(f.json)+": "+schemaType(f))
			if !f.optional {
				required = append(required, jsonString(f.json))
			}
		}
		def := "    " + jsonString(t.Name()) + ": {\n" +
			"      \"type\": \"object\",\n" +
			"      \"additionalProperties\": false,\n" +
			"      \"properties\": {\n" + strings.Join(props, ",\n") + "\n      }"
		if len(required) > 0 {
			def += ",\n      \"required\": [" + strings.Join(required, ", ") + "]"
		}
		def += "\n    }"
		defs = append(defs, def)
	}
	b.WriteString(strings.Join(defs, ",\n"))
	b.WriteString("\n  }\n}\n")
	return b.String()
}

func schemaType(f fieldInfo) string {
	switch f.kind {
	case "string":
		return `{ "type": "string" }`
	case "int":
		return `{ "type": "integer" }`
	case "bool":
		return `{ "type": "boolean" }`
	case "enum", "struct":
		return `{ "$ref": "#/$defs/` + f.ref + `" }`
	case "array":
		return `{ "type": "array", "items": ` + schemaType(*f.elem) + ` }`
	}
	return `{}`
}

// --- TypeScript -----------------------------------------------------------

func renderTS(fields map[reflect.Type][]fieldInfo) string {
	var b strings.Builder
	b.WriteString(genHeaderTS)
	// Enums as string-literal unions, sorted for stability.
	var enumNames []string
	enumByName := map[string][]string{}
	for t, vals := range enumValues {
		enumByName[t.Name()] = vals
		enumNames = append(enumNames, t.Name())
	}
	sort.Strings(enumNames)
	for _, name := range enumNames {
		var parts []string
		for _, v := range enumByName[name] {
			parts = append(parts, jsonString(v))
		}
		b.WriteString(fmt.Sprintf("export type %s = %s;\n\n", name, strings.Join(parts, " | ")))
	}
	for _, t := range rootStructs {
		b.WriteString(fmt.Sprintf("export interface %s {\n", t.Name()))
		for _, f := range fields[t] {
			opt := ""
			if f.optional {
				opt = "?"
			}
			b.WriteString(fmt.Sprintf("  %s%s: %s;\n", f.json, opt, tsType(f)))
		}
		b.WriteString("}\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func tsType(f fieldInfo) string {
	switch f.kind {
	case "string":
		return "string"
	case "int":
		return "number"
	case "bool":
		return "boolean"
	case "enum", "struct":
		return f.ref
	case "array":
		return tsType(*f.elem) + "[]"
	}
	return "unknown"
}

// --- Python ---------------------------------------------------------------

func renderPython(fields map[reflect.Type][]fieldInfo) string {
	var b strings.Builder
	b.WriteString(genHeaderPy)
	var enumNames []string
	enumByName := map[string][]string{}
	for t, vals := range enumValues {
		enumByName[t.Name()] = vals
		enumNames = append(enumNames, t.Name())
	}
	sort.Strings(enumNames)
	for _, name := range enumNames {
		var parts []string
		for _, v := range enumByName[name] {
			parts = append(parts, pyString(v))
		}
		b.WriteString(fmt.Sprintf("%s = Literal[%s]\n", name, strings.Join(parts, ", ")))
	}
	b.WriteString("\n")
	for _, t := range rootStructs {
		// total=False when any field is optional, with Required[...] for the rest,
		// keeps required/optional exact under TypedDict.
		b.WriteString(fmt.Sprintf("class %s(TypedDict, total=False):\n", t.Name()))
		for _, f := range fields[t] {
			ann := pyType(f)
			if !f.optional {
				ann = "Required[" + ann + "]"
			}
			b.WriteString(fmt.Sprintf("    %s: %s\n", pyKey(f.json), ann))
		}
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func pyType(f fieldInfo) string {
	switch f.kind {
	case "string":
		return "str"
	case "int":
		return "int"
	case "bool":
		return "bool"
	case "enum", "struct":
		return `"` + f.ref + `"`
	case "array":
		return "list[" + strings.Trim(pyType(*f.elem), `"`) + "]"
	}
	return "object"
}

// pyKey keeps JSON keys as-is (they are all valid Python identifiers here); a
// TypedDict may use string keys, but these are plain snake_case already.
func pyKey(s string) string { return s }

// --- helpers --------------------------------------------------------------

const genHeaderTS = `// Code generated by clients/gen (go run ./clients/gen/main.go). DO NOT EDIT.
//
// interlock.spec.v1 + protocol DTOs. Data types only — no decide, no publish, no
// broker. Enforcement is the trusted Go executable; this package carries shapes
// and (see canonical.ts) the parity-gated canonical encoder, never decisions.

`

const genHeaderPy = `# Code generated by clients/gen (go run ./clients/gen/main.go). DO NOT EDIT.
#
# interlock.spec.v1 + protocol DTOs. Data types only — no decide, no publish, no
# broker. Enforcement is the trusted Go executable; this package carries shapes
# and (see canonical.py) the parity-gated canonical encoder, never decisions.
from __future__ import annotations

from typing import Literal, Required, TypedDict

`

func toStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

func jsonString(s string) string {
	// The strings here are identifiers/vocabulary — no special chars — but keep it
	// correct anyway.
	b, _ := marshalPlain(s)
	return b
}

func pyString(s string) string { return `"` + s + `"` }

// marshalPlain renders a Go string as a JSON string literal without importing a
// heavyweight path; the inputs are all simple ASCII vocabulary.
func marshalPlain(s string) (string, error) {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

func findRoot() (string, error) {
	// Run from the module root (where go.mod is).
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		dir = parent
	}
}

func writeFile(path, content string) {
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	must(os.WriteFile(path, []byte(content), 0o644))
	fmt.Println("wrote", path)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

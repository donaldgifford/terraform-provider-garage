// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Command spec-downgrade rewrites the Garage admin OpenAPI 3.1.0 spec
// into a 3.0.3-compatible form so `oapi-codegen` (which parses via
// `kin-openapi`, currently 3.0-only) can consume it.
//
// Transforms applied:
//
//   - `openapi: "3.1.0"` → `openapi: "3.0.3"`.
//   - JSON Schema type unions of the form `"type": ["X", "null"]` (3.1.0
//     nullable shorthand) → `"type": "X"` + `"nullable": true` (3.0 form).
//   - JSON Schema tuple validation (`prefixItems` + `items: false`) →
//     permissive `items` schema. Lossy — the generated Go client treats
//     tuples as homogeneous arrays, which is fine for client-side decode.
//   - `oneOf` schemas containing `{type: "null"}` (3.1.0 nullable-ref
//     idiom) → `nullable: true` on the parent, with the null branch
//     stripped from oneOf. A oneOf left with a single `$ref` element is
//     rewritten to `allOf: [{$ref: …}]` (3.0 sibling restriction). A
//     oneOf left with a single inline schema is merged into the parent.
//
// Run via `just generate`; not intended for manual invocation. Output is
// re-derived from the upstream raw spec every regeneration and committed
// alongside it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	in := flag.String("in", "", "input OpenAPI 3.1.0 spec (JSON)")
	out := flag.String("out", "", "output OpenAPI 3.0.3-compatible spec (JSON)")
	flag.Parse()

	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: spec-downgrade -in <path> -out <path>")
		os.Exit(2)
	}

	raw, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *in, err)
		os.Exit(1)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", *in, err)
		os.Exit(1)
	}

	doc["openapi"] = "3.0.3"
	walk(doc)

	formatted, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	formatted = append(formatted, '\n')

	if err := os.WriteFile(*out, formatted, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
		os.Exit(1)
	}
}

// walk applies the schema downgrades anywhere they appear in the doc tree.
// The transforms target leaf maps that look like JSON Schema objects;
// non-schema maps are unaffected because the keys we touch (type,
// prefixItems, items, oneOf) only carry the targeted semantics inside
// schemas.
func walk(node any) {
	switch v := node.(type) {
	case map[string]any:
		downgradeTypeUnion(v)
		downgradeTupleItems(v)
		downgradeNullableOneOf(v)
		for _, child := range v {
			walk(child)
		}
	case []any:
		for _, child := range v {
			walk(child)
		}
	}
}

// downgradeTypeUnion rewrites `"type": ["X", "null"]` into `"type": "X"` +
// `"nullable": true`. The two-element-with-null form is the only union
// shape the upstream Garage spec uses; other shapes are left untouched
// (and would fail parsing — surface that as a build error, not a silent
// rewrite).
func downgradeTypeUnion(obj map[string]any) {
	t, ok := obj["type"].([]any)
	if !ok {
		return
	}
	if len(t) != 2 {
		return
	}

	var primary string
	hasNull := false
	for _, entry := range t {
		s, ok := entry.(string)
		if !ok {
			return
		}
		if s == "null" {
			hasNull = true
			continue
		}
		primary = s
	}

	if !hasNull || primary == "" {
		return
	}

	obj["type"] = primary
	obj["nullable"] = true
}

// downgradeTupleItems collapses a tuple schema (`prefixItems` + items:false)
// to a permissive schema. The Garage spec uses this once, for HTTP-header
// `[name, value]` string pairs; treating it as `items: {type: string}` is
// lossy in the spec but exact at the wire level.
func downgradeTupleItems(obj map[string]any) {
	prefixRaw, hasPrefix := obj["prefixItems"]
	if !hasPrefix {
		return
	}
	prefix, ok := prefixRaw.([]any)
	if !ok || len(prefix) == 0 {
		return
	}

	itemsRaw, hasItems := obj["items"]
	if hasItems {
		if b, ok := itemsRaw.(bool); !ok || b {
			return
		}
	}

	first, ok := prefix[0].(map[string]any)
	if !ok {
		return
	}

	delete(obj, "prefixItems")
	obj["items"] = first
}

// downgradeNullableOneOf converts the 3.1.0 nullable-ref idiom
// `oneOf: [{type: "null"}, X]` to 3.0's `nullable: true` form.
//
// Outcomes:
//   - oneOf had no {type:null} branch: untouched.
//   - oneOf had a null branch and ≥2 non-null branches: drop null, set
//     nullable, keep oneOf with remaining branches.
//   - oneOf had a null branch and exactly one $ref branch: replace oneOf
//     with `allOf: [{$ref: …}]` (3.0 forbids $ref siblings; allOf wrap
//     is the canonical workaround) and set nullable: true.
//   - oneOf had a null branch and exactly one inline-schema branch:
//     merge that branch's keys into the parent (skipping conflicts) and
//     set nullable: true.
func downgradeNullableOneOf(obj map[string]any) {
	oneOfRaw, ok := obj["oneOf"].([]any)
	if !ok {
		return
	}

	kept := make([]any, 0, len(oneOfRaw))
	sawNull := false
	for _, branch := range oneOfRaw {
		m, isMap := branch.(map[string]any)
		if isMap && m["type"] == "null" && len(m) == 1 {
			sawNull = true
			continue
		}
		kept = append(kept, branch)
	}

	if !sawNull {
		return
	}

	obj["nullable"] = true

	if len(kept) > 1 {
		obj["oneOf"] = kept
		return
	}

	delete(obj, "oneOf")
	if len(kept) == 0 {
		return
	}

	only, isMap := kept[0].(map[string]any)
	if !isMap {
		obj["oneOf"] = kept
		return
	}

	if _, hasRef := only["$ref"]; hasRef {
		obj["allOf"] = []any{only}
		return
	}

	for k, v := range only {
		if _, exists := obj[k]; !exists {
			obj[k] = v
		}
	}
}

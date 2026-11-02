package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// surface is one schema dump, read for the comparison: every item of
// every list, keyed by itemKey, every other key but the build stamp, and
// each tool's fields at any depth. The fields are the contract (README,
// Versioning): tool names, their arguments and the shape of their output
// are stable within a major version. lists names every list the dump
// carries, empty ones included, since a dump older than a list cannot
// show it.
type surface struct {
	items  map[itemKey]map[string]any
	rest   map[string]any
	lists  map[string]bool
	fields map[string]toolFields
}

// stampKeys are the dump's build stamp: which binary and SDK wrote it.
// They change with the build and are not part of the surface; what an
// SDK bump changes on the wire shows up inside the tools.
var stampKeys = []string{"header"}

// itemKey names an item of a dumped list: the list, and the id a client
// asks for it by, its template or URI, else its name. The id is what a
// commit footer names the item by.
type itemKey struct{ list, id string }

// listWords is what the report calls an item of each list.
var listWords = map[string]string{"tools": "tool", "resource_templates": "resource template", "resources": "resource", "prompts": "prompt"}

func (k itemKey) String() string {
	w, ok := listWords[k.list]
	if !ok {
		w = k.list
	}
	return w + " " + k.id
}

func readSurface(data []byte) (surface, error) {
	var dump struct {
		Tools []struct {
			Name         string          `json:"name"`
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		return surface{}, err
	}
	var whole map[string]any
	if err := json.Unmarshal(data, &whole); err != nil {
		return surface{}, err
	}
	items, rest, err := dumpItems(whole)
	if err != nil {
		return surface{}, err
	}
	s := surface{items: items, rest: rest, lists: map[string]bool{}, fields: map[string]toolFields{}}
	for k, v := range whole {
		if _, isList := v.([]any); isList {
			s.lists[k] = true
		}
	}
	for _, t := range dump.Tools {
		f := toolFields{inputs: newFieldSet(), outputs: newFieldSet()}
		if err := walkSchema(t.InputSchema, f.inputs); err != nil {
			return surface{}, fmt.Errorf("%s input: %w", t.Name, err)
		}
		if err := walkSchema(t.OutputSchema, f.outputs); err != nil {
			return surface{}, fmt.Errorf("%s output: %w", t.Name, err)
		}
		s.fields[t.Name] = f
	}
	return s, nil
}

// dumpItems reads every item of every list in a dump, and every other
// key but the build stamp. A list with an item that has no id is
// compared as one value, since a position is no identity.
func dumpItems(whole map[string]any) (items map[itemKey]map[string]any, rest map[string]any, err error) {
	items = map[itemKey]map[string]any{}
	rest = map[string]any{}
	for list, v := range whole {
		if slices.Contains(stampKeys, list) {
			continue
		}
		entries, isList := v.([]any)
		if !isList {
			rest[list] = v
			continue
		}
		keyed := map[itemKey]map[string]any{}
		for _, e := range entries {
			obj, _ := e.(map[string]any)
			id := itemID(obj)
			if id == "" {
				keyed = nil
				break
			}
			k := itemKey{list, id}
			if _, twice := keyed[k]; twice {
				return nil, nil, fmt.Errorf("the %s list names %q twice", list, id)
			}
			keyed[k] = obj
		}
		if keyed == nil {
			rest[list] = v
			continue
		}
		maps.Copy(items, keyed)
	}
	return items, rest, nil
}

// itemID is the id of an item of a dumped list, or "" when it has none.
func itemID(item map[string]any) string {
	for _, k := range []string{"uriTemplate", "uri", "name"} {
		if v, ok := item[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// A change is one difference between two dumps: the item it is to, which
// is what a footer names, and how the report shows it. The item is a
// tool's name, a resource template's URI template, a list item's id, or
// a top-level key such as instructions. field is, on the break of a tool
// still there, the field it breaks, such as "input id", so a break is
// matched across two comparisons by item and field, never by its text.
type change struct {
	item, text, field string
}

// surfaceChanges is what differs between two surfaces. breaking is what
// breaks a caller; changed is every other difference, a description, an
// annotation or a _meta mark included, named by the part that moved.
// notes are values a kept field lists that a person should look at.
type surfaceChanges struct {
	added, changed, breaking []change
	notes                    []string
}

func (c surfaceChanges) empty() bool {
	return len(c.added) == 0 && len(c.changed) == 0 && len(c.breaking) == 0
}

// compareSurfaces is what changed from base to cur. An item gone breaks
// a caller, and so does a tool still there that broke at any depth
// (brokenFields). A tool that broke lists every other change to it too:
// the break names only the fields it broke, and anything else that moved
// there is a change a reviewer has to see.
func compareSurfaces(base, cur surface) surfaceChanges {
	var c surfaceChanges
	var removed []change
	byListThenID := func(a, b itemKey) int { return cmp.Or(strings.Compare(a.list, b.list), strings.Compare(a.id, b.id)) }
	for _, k := range sortedUnion(base.items, cur.items, byListThenID) {
		was, inBase := base.items[k]
		now, inCur := cur.items[k]
		switch {
		case !inCur:
			removed = append(removed, change{item: k.id, text: k.String() + " removed"})
		case !inBase:
			c.added = append(c.added, change{item: k.id, text: k.String()})
		default:
			if parts := changedParts(was, now); len(parts) > 0 {
				c.changed = append(c.changed, change{item: k.id, text: k.String() + " (" + strings.Join(parts, ", ") + ")"})
			}
		}
	}
	// A key other than a list is its own item, named first as a footer
	// names it: "instructions".
	for _, part := range changedParts(base.rest, cur.rest) {
		top, sub, _ := strings.Cut(part, ".")
		if n := len(c.changed); n > 0 && c.changed[n-1].item == top {
			c.changed[n-1].text += ", " + sub
			continue
		}
		text := top
		if sub != "" {
			text += ": " + sub
		}
		c.changed = append(c.changed, change{item: top, text: text})
	}
	c.breaking = slices.Concat(removed, brokenFields(base.fields, cur.fields))
	c.notes = valueNotes(base.fields, cur.fields)
	return c
}

// write prints the changes to w, under a header naming what they are
// against.
func (c surfaceChanges) write(w io.Writer, against string) {
	_, _ = fmt.Fprintf(w, "against %s: %d added, %d changed, %d breaking\n", against, len(c.added), len(c.changed), len(c.breaking))
	for _, n := range c.added {
		_, _ = fmt.Fprintf(w, "  + %s\n", n.text)
	}
	for _, n := range c.changed {
		_, _ = fmt.Fprintf(w, "  ~ %s\n", n.text)
	}
	for _, n := range c.notes {
		_, _ = fmt.Fprintf(w, "  ~ %s\n", n)
	}
	for _, b := range c.breaking {
		_, _ = fmt.Fprintf(w, "  ! %s  BREAKING\n", b.text)
	}
}

// changedParts names what differs between two dumped objects, one level
// deep, so a flipped hint reads as annotations.destructiveHint. Values
// are compared decoded, so a schema spelled another way, its keys in
// another order, is the same schema.
func changedParts(o, n map[string]any) []string {
	var parts []string
	for _, k := range sortedUnion(o, n, strings.Compare) {
		if reflect.DeepEqual(o[k], n[k]) {
			continue
		}
		om, oMap := o[k].(map[string]any)
		nm, nMap := n[k].(map[string]any)
		if !oMap || !nMap {
			parts = append(parts, k)
			continue
		}
		for _, sub := range sortedUnion(om, nm, strings.Compare) {
			if !reflect.DeepEqual(om[sub], nm[sub]) {
				parts = append(parts, k+"."+sub)
			}
		}
	}
	return parts
}

// sortedUnion is every key of a and b, sorted by compare.
func sortedUnion[K comparable, V any](a, b map[K]V, compare func(K, K) int) []K {
	keys := slices.AppendSeq(slices.Collect(maps.Keys(a)), maps.Keys(b))
	slices.SortFunc(keys, compare)
	return slices.Compact(keys)
}

// toolFields is what a caller relies on in one tool, at any depth: the
// fields it may send, and the ones it reads back.
//
// A path names a field the way a caller reaches it: `exposure.before`,
// `files[].id` for a field of each element of a list, and
// `restrictions{}` for the values of a map.
type toolFields struct {
	inputs, outputs fieldSet
}

// fieldSet is one side of a tool: every field by its path, and which of
// them are required.
type fieldSet struct {
	fields   map[string]field
	required map[string]bool
}

func newFieldSet() fieldSet {
	return fieldSet{fields: map[string]field{}, required: map[string]bool{}}
}

// field is one field's type as the schema spells it, "" for no type,
// and the values it is limited to, nil when it is not.
type field struct {
	typ  string
	enum []string
}

// schemaNode is the part of a JSON Schema the diff walks.
type schemaNode struct {
	Type                 json.RawMessage        `json:"type"`
	Enum                 []json.RawMessage      `json:"enum"`
	Properties           map[string]*schemaNode `json:"properties"`
	Items                *schemaNode            `json:"items"`
	AdditionalProperties *schemaNode            `json:"additionalProperties"`
	Required             []string               `json:"required"`
	// The walk cannot see through these, so a schema that uses one is
	// refused rather than compared as if it had no fields.
	Ref   string            `json:"$ref"`
	AnyOf []json.RawMessage `json:"anyOf"`
	OneOf []json.RawMessage `json:"oneOf"`
	AllOf []json.RawMessage `json:"allOf"`
}

// UnmarshalJSON takes a boolean schema too: `true`, which a list or a
// map of any values has for its items, is any value and has no fields to
// walk. Its type reads as the boolean, which typesCover reads as any
// type for `true` and as none for `false`.
func (n *schemaNode) UnmarshalJSON(data []byte) error {
	if b := bytes.TrimSpace(data); string(b) == "true" || string(b) == "false" {
		n.Type = json.RawMessage(b)
		return nil
	}
	type plain schemaNode
	return json.Unmarshal(data, (*plain)(n))
}

// walkSchema records every field in a raw schema, and which are
// required. A tool with no output schema has no output fields.
func walkSchema(raw json.RawMessage, into fieldSet) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var n schemaNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	return n.walk("", into)
}

func (n *schemaNode) walk(prefix string, into fieldSet) error {
	if n.Ref != "" || n.AnyOf != nil || n.OneOf != nil || n.AllOf != nil {
		where := prefix
		if where == "" {
			where = "the top level"
		}
		return fmt.Errorf("%s uses $ref, anyOf, oneOf or allOf, which this diff cannot compare", where)
	}
	for _, r := range n.Required {
		into.required[fieldPath(prefix, r)] = true
	}
	for name, child := range n.Properties {
		path := fieldPath(prefix, name)
		into.fields[path] = child.field()
		if err := child.walk(path, into); err != nil {
			return err
		}
	}
	if n.Items != nil {
		into.fields[prefix+"[]"] = n.Items.field()
		if err := n.Items.walk(prefix+"[]", into); err != nil {
			return err
		}
	}
	// additionalProperties is false on a closed object, and on a map it
	// types every value. A closed object has no values to name.
	if v := n.AdditionalProperties; v != nil && string(v.Type) != "false" {
		into.fields[prefix+"{}"] = v.field()
		if err := v.walk(prefix+"{}", into); err != nil {
			return err
		}
	}
	return nil
}

// field is what the schema says of the field n describes.
func (n *schemaNode) field() field {
	f := field{typ: compactJSON(n.Type)}
	for _, v := range n.Enum {
		f.enum = append(f.enum, compactJSON(v))
	}
	return f
}

// compactJSON is a schema value as the schema spells it, so `"string"`
// and `["null","string"]` differ.
func compactJSON(v json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, v) != nil {
		return string(v)
	}
	return buf.String()
}

func fieldPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// parentPath is the field a path sits in, or "" at the top.
func parentPath(path string) string {
	for _, suffix := range []string{"[]", "{}"} {
		if strings.HasSuffix(path, suffix) {
			return strings.TrimSuffix(path, suffix)
		}
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i]
	}
	return ""
}

// brokenFields lists, as changes to each tool, what a tool kept by name
// breaks: a field removed, an input that takes fewer types or values, an
// output that may return more types or may now be missing, or an input
// newly required. A removed tool is the caller's to report.
func brokenFields(prev, cur map[string]toolFields) []change {
	var out []change
	for _, name := range slices.Sorted(maps.Keys(prev)) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		// broke records that name's field on side broke, in the way the
		// format says, so the text and the field cannot drift apart.
		broke := func(side, field, format string, args ...any) {
			out = append(out, change{name, name + ": " + side + " field " + field + " " + fmt.Sprintf(format, args...), side + " " + field})
		}
		sideBreaks(broke, "input", was.inputs, now.inputs)
		sideBreaks(broke, "output", was.outputs, now.outputs)
		// A required field is new to a caller only where its parent was
		// already there: inside an object that is itself new and
		// optional, a caller who does not send the object is unaffected.
		for _, f := range slices.Sorted(maps.Keys(now.inputs.required)) {
			parent := parentPath(f)
			_, parentWas := was.inputs.fields[parent]
			if !was.inputs.required[f] && (parent == "" || parentWas) {
				broke("input", f, "newly required")
			}
		}
	}
	return out
}

// sideBreaks records what one side of a kept tool breaks: a field
// removed, a type a caller of it cannot rely on, an input value it
// refuses now, an output that may now be missing. A field inside one
// that was removed is not listed again.
func sideBreaks(broke func(side, field, format string, args ...any), side string, was, now fieldSet) {
	input := side == "input"
	for _, f := range slices.Sorted(maps.Keys(was.fields)) {
		w := was.fields[f]
		n, still := now.fields[f]
		if !still {
			if _, parentKept := now.fields[parentPath(f)]; parentPath(f) == "" || parentKept {
				broke(side, f, "removed")
			}
			continue
		}
		if typeBreaks(w.typ, n.typ, input) {
			broke(side, f, "changed type from %s to %s", typeWord(w.typ), typeWord(n.typ))
		}
		// A caller sent this value, and it is refused now.
		if input && n.enum != nil {
			for _, v := range w.enum {
				if !slices.Contains(n.enum, v) {
					broke(side, f, "no longer takes %s", v)
				}
			}
		}
		// A caller read this field as always there.
		if !input && was.required[f] && !now.required[f] {
			broke(side, f, "no longer required")
		}
	}
}

// valueNotes lists what a person should look at in the values a kept
// field lists, where a caller may or may not be broken: an output that
// may carry a value it did not, which a caller may not handle, and an
// input newly limited to a list, which breaks a caller only if the
// server took other values before.
func valueNotes(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(prev)) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, f := range slices.Sorted(maps.Keys(was.outputs.fields)) {
			w := was.outputs.fields[f]
			n, still := now.outputs.fields[f]
			switch {
			case !still || w.enum == nil:
			case n.enum == nil:
				out = append(out, fmt.Sprintf("%s: output field %s may now be any value", name, f))
			default:
				for _, v := range n.enum {
					if !slices.Contains(w.enum, v) {
						out = append(out, fmt.Sprintf("%s: output field %s may now be %s", name, f, v))
					}
				}
			}
		}
		for _, f := range slices.Sorted(maps.Keys(was.inputs.fields)) {
			if n, still := now.inputs.fields[f]; still && was.inputs.fields[f].enum == nil && n.enum != nil {
				out = append(out, fmt.Sprintf("%s: input field %s now takes only %s", name, f, strings.Join(n.enum, ", ")))
			}
		}
	}
	return out
}

// typeBreaks reports whether a field's type change breaks a caller. An
// input may take more types than it did, and an output may return fewer;
// the other way round, a caller that sent or read the old type is
// broken. So `"string"` to `["null","string"]` breaks an output, where a
// caller read the field as always there, and not an input, which is what
// a bool becoming a *bool produces.
func typeBreaks(was, now string, input bool) bool {
	if was == now {
		return false
	}
	wide, narrow := now, was
	if !input {
		wide, narrow = was, now
	}
	return !typesCover(wide, narrow)
}

// typesCover reports whether every value narrow allows, wide allows too,
// as JSON Schema 2020-12 reads a type: a list allows any type in it, and
// an integer is a number. No type, or the schema `true`, is any value;
// the schema `false` lists no type, so it allows none.
func typesCover(wide, narrow string) bool {
	if anyType(wide) {
		return true
	}
	if anyType(narrow) {
		return false
	}
	allowed := map[string]bool{}
	for _, t := range typeList(wide) {
		allowed[t] = true
	}
	if allowed["number"] {
		allowed["integer"] = true
	}
	for _, t := range typeList(narrow) {
		if !allowed[t] {
			return false
		}
	}
	return true
}

// anyType reports whether a field takes any value: it has no type, or it
// is the boolean schema `true`.
func anyType(typ string) bool {
	return typ == "" || typ == "true"
}

// typeList is a schema's type as a list, whether it was written as one
// name or several. The schema `false` reads as no type.
func typeList(typ string) []string {
	var one string
	if json.Unmarshal([]byte(typ), &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal([]byte(typ), &many)
	return many
}

// typeWord is a type for a message: as the schema spells it, or any.
func typeWord(typ string) string {
	if anyType(typ) {
		return "any"
	}
	return typ
}

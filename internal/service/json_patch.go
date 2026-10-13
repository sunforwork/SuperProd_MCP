package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
)

func (patch *Patch) UnmarshalJSON(b []byte) error {
	type wire Patch
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return e
	}
	if fields["op"] == nil || fields["path"] == nil {
		return errors.New("JSON Patch op and path required")
	}
	var v wire
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return e
	}
	if (v.Op == "add" || v.Op == "replace" || v.Op == "test") && fields["value"] == nil {
		return errors.New("JSON Patch value required (null is allowed)")
	}
	if (v.Op == "move" || v.Op == "copy") && fields["from"] == nil {
		return errors.New("JSON Patch from required")
	}
	*patch = Patch(v)
	return nil
}
func (patch Patch) MarshalJSON() ([]byte, error) {
	v := p.Object{"op": patch.Op, "path": patch.Path}
	if patch.Op == "add" || patch.Op == "replace" || patch.Op == "test" {
		v["value"] = patch.Value
	}
	if patch.Op == "move" || patch.Op == "copy" {
		v["from"] = patch.From
	}
	return json.Marshal(v)
}
func jsonEqual(a, b any) bool {
	isNumber := func(v any) bool {
		switch v.(type) {
		case json.Number, float64, int, int64:
			return true
		}
		return false
	}
	if isNumber(a) && isNumber(b) {
		an, aok := new(big.Rat).SetString(fmt.Sprint(a))
		bn, bok := new(big.Rat).SetString(fmt.Sprint(b))
		return aok && bok && an.Cmp(bn) == 0
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !jsonEqual(v, y[i]) {
				return false
			}
		}
		return true
	}
	return fingerprint(a) == fingerprint(b)
}

func jsonPointer(path string) ([]string, error) {
	if path == "" {
		return []string{}, nil
	}
	return pointer(path)
}
func escaped(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func arrayIndex(s string, size int, insert bool) (int, error) {
	if insert && s == "-" {
		return size, nil
	}
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, errors.New("invalid array index")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid array index")
		}
	}
	i, e := strconv.Atoi(s)
	if e != nil || i < 0 || i > size || (!insert && i == size) {
		return 0, errors.New("array index out of bounds")
	}
	return i, nil
}
func jsonAt(root any, parts []string) (any, error) {
	for _, part := range parts {
		switch v := root.(type) {
		case map[string]any:
			var ok bool
			root, ok = v[part]
			if !ok {
				return nil, errors.New("JSON Pointer target missing")
			}
		case []any:
			i, e := arrayIndex(part, len(v), false)
			if e != nil {
				return nil, e
			}
			root = v[i]
		default:
			return nil, errors.New("JSON Pointer traverses scalar")
		}
	}
	return root, nil
}
func jsonChange(root any, parts []string, op string, value any) (any, error) {
	if len(parts) == 0 {
		if op == "remove" {
			return nil, nil
		}
		return p.Clone(value), nil
	}
	part := parts[0]
	switch v := root.(type) {
	case map[string]any:
		old, exists := v[part]
		if len(parts) > 1 {
			if !exists {
				return nil, errors.New("parent missing")
			}
			next, e := jsonChange(old, parts[1:], op, value)
			if e != nil {
				return nil, e
			}
			v[part] = next
			return v, nil
		}
		if op != "add" && !exists {
			return nil, errors.New("target missing")
		}
		if op == "remove" {
			delete(v, part)
		} else {
			v[part] = p.Clone(value)
		}
		return v, nil
	case []any:
		i, e := arrayIndex(part, len(v), len(parts) == 1 && op == "add")
		if e != nil {
			return nil, e
		}
		if len(parts) > 1 {
			next, e := jsonChange(v[i], parts[1:], op, value)
			if e != nil {
				return nil, e
			}
			v[i] = next
			return v, nil
		}
		switch op {
		case "add":
			v = append(v, nil)
			copy(v[i+1:], v[i:])
			v[i] = p.Clone(value)
		case "remove":
			v = append(v[:i], v[i+1:]...)
		case "replace":
			v[i] = p.Clone(value)
		}
		return v, nil
	default:
		return nil, errors.New("parent is not a container")
	}
}
func hasRedaction(v any) bool {
	switch x := v.(type) {
	case string:
		return x == "[REDACTED]"
	case map[string]any:
		for _, c := range x {
			if hasRedaction(c) {
				return true
			}
		}
	case []any:
		for _, c := range x {
			if hasRedaction(c) {
				return true
			}
		}
	}
	return false
}
func applyJSONPatch(doc p.Object, patch []Patch) (p.Object, error) {
	return applyJSONPatchLimit(doc, patch, maxBusinessBytes)
}

func applyJSONPatchLimit(doc p.Object, patch []Patch, byteLimit int) (p.Object, error) {
	var root any = p.Clone(doc)
	for n, item := range patch {
		parts, e := jsonPointer(item.Path)
		if e != nil {
			return nil, e
		}
		if (item.Op == "add" || item.Op == "replace" || item.Op == "test") && hasRedaction(item.Value) {
			return nil, errors.New("redaction placeholders are not data; read and edit smaller paths")
		}
		switch item.Op {
		case "test":
			var old any
			old, e = jsonAt(root, parts)
			if e == nil && !jsonEqual(old, p.Clone(item.Value)) {
				e = errors.New("JSON Patch test failed")
			}
		case "add", "replace", "remove":
			root, e = jsonChange(root, parts, item.Op, item.Value)
		case "copy", "move":
			var from []string
			from, e = jsonPointer(item.From)
			if e != nil {
				break
			}
			var value any
			value, e = jsonAt(root, from)
			if e != nil {
				break
			}
			value = p.Clone(value)
			if item.Op == "move" {
				if item.Path == item.From {
					continue
				}
				if strings.HasPrefix(item.Path, item.From+"/") {
					e = errors.New("cannot move into descendant")
					break
				}
				root, e = jsonChange(root, from, "remove", nil)
				if e != nil {
					break
				}
			}
			root, e = jsonChange(root, parts, "add", value)
		default:
			e = errors.New("unsupported JSON Patch operation")
		}
		if e != nil {
			return nil, fmt.Errorf("patch[%d] %s: %w", n, item.Path, e)
		}

		body, e := json.Marshal(root)
		if e != nil {
			return nil, e
		}
		if len(body) > byteLimit {
			return nil, fmt.Errorf("patch[%d] exceeds business document byte limit (%d)", n, byteLimit)
		}
	}
	if p.Obj(root) == nil {
		return nil, errors.New("business document must be a JSON object")
	}
	return p.Obj(root), nil
}

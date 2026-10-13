package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

type Object = map[string]any

func Parse(data []byte) (Object, error) {
	var v Object
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, errors.New("invalid JSON object")
	}
	if v == nil {
		return nil, errors.New("expected JSON object")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing JSON content")
	}
	return v, nil
}
func Clone[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out T
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&out); err != nil {
		panic(err)
	}
	return out
}
func Obj(v any) Object {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
func Arr(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}
func Str(v any) string { s, _ := v.(string); return s }
func Num(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
func Int(v any) int64 { return int64(Num(v)) }
func Bool(v any) bool { b, _ := v.(bool); return b }
func Strings(v any) []string {
	a := Arr(v)
	out := make([]string, 0, len(a))
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func Values(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

func MergeClock(a, b Object) Object {
	out := Clone(a)
	if out == nil {
		out = Object{}
	}
	for k, v := range b {
		if Num(v) > Num(out[k]) {
			out[k] = v
		}
	}
	return out
}

func CompareClock(a, b Object) int {
	less, greater := false, false
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for k := range keys {
		less = less || Num(a[k]) < Num(b[k])
		greater = greater || Num(a[k]) > Num(b[k])
	}
	if less && greater {
		return 2
	}
	if less {
		return -1
	}
	if greater {
		return 1
	}
	return 0
}

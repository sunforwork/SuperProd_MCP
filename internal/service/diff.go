package service

import (
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"reflect"
	"sort"
	"strings"
)

func Diff(before, after p.Object) []any {
	out := []any{}
	var walk func(string, any, bool, any, bool)
	walk = func(path string, a any, hasA bool, b any, hasB bool) {
		if hasA == hasB && reflect.DeepEqual(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			ordered := make([]string, 0, len(keys))
			for k := range keys {
				ordered = append(ordered, k)
			}
			sort.Strings(ordered)
			for _, k := range ordered {
				av, ae := am[k]
				bv, be := bm[k]
				escaped := strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
				walk(path+"/"+escaped, av, ae, bv, be)
			}
			return
		}
		out = append(out, p.Object{"path": path, "beforeExists": hasA, "before": a, "afterExists": hasB, "after": b})
	}
	walk("", before, true, after, true)
	return out
}

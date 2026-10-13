package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"slices"
	"strings"
	"unicode/utf16"
)

func entityAdd(s p.Object, model string, value p.Object, upsert bool) error {
	m := p.Obj(s[model])
	if m == nil {
		return errors.New(model + " model missing")
	}
	es := p.Obj(m["entities"])
	if es == nil {
		return errors.New(model + " entities missing")
	}
	id := p.Str(value["id"])
	if id == "" {
		return errors.New("entity id missing")
	}
	if old := p.Obj(es[id]); old != nil {
		if upsert {
			update(old, value)
		}
		return nil
	}
	es[id] = p.Clone(value)
	m["ids"] = append(p.Arr(m["ids"]), id)
	return nil
}
func entityRemove(s p.Object, model, id string) {
	if m := p.Obj(s[model]); m != nil {
		delete(p.Obj(m["entities"]), id)
		m["ids"] = remove(p.Arr(m["ids"]), id)
	}
}
func entityUpdate(s p.Object, model string, u p.Object) error {
	id := p.Str(u["id"])
	old := p.Obj(entities(s, model)[id])
	if old == nil {
		return nil
	}
	ch := p.Obj(u["changes"])
	if renamed := p.Str(ch["id"]); renamed != "" && renamed != id {
		return ErrUnsupported
	}
	update(old, ch)
	return nil
}
func sanitizedSectionTitle(v any) string {
	units := utf16.Encode([]rune(strings.TrimSpace(p.Str(v))))
	if len(units) > 200 {
		units = units[:200]
	}
	return string(utf16.Decode(units))
}
func moveAfter(ids []any, id, anchor string) []any {
	out := remove(ids, id)
	index := 0
	if anchor != "" {
		index = -1
		for i, v := range out {
			if v == anchor {
				index = i + 1
				break
			}
		}
		if index < 0 {
			if has(ids, id) {
				return ids
			}
			return append(out, id)
		}
	}
	out = append(out, nil)
	copy(out[index+1:], out[index:])
	out[index] = id
	return out
}
func applySection(s p.Object, a Action) error {
	pl := a.Payload
	m := p.Obj(s["section"])
	if m == nil {
		return errors.New("section model missing")
	}
	switch a.Code {
	case "S1":
		value := p.Clone(p.Obj(pl["section"]))
		value["title"] = sanitizedSectionTitle(value["title"])
		if value["taskIds"] == nil {
			value["taskIds"] = []any{}
		}
		return entityAdd(s, "section", value, false)
	case "S2":
		entityRemove(s, "section", p.Str(pl["id"]))
	case "S3":
		u := p.Clone(p.Obj(pl["section"]))
		ch := p.Obj(u["changes"])
		if v, ok := ch["title"]; ok {
			ch["title"] = sanitizedSectionTitle(v)
		}
		return entityUpdate(s, "section", u)
	case "S4":
		context := p.Str(pl["contextId"])
		ordered := []any{}
		seen := map[string]bool{}
		for _, raw := range append(append([]any{}, p.Arr(pl["ids"])...), p.Arr(m["ids"])...) {
			id := p.Str(raw)
			if e := p.Obj(entities(s, "section")[id]); e != nil && e["contextId"] == context && !seen[id] {
				ordered = append(ordered, id)
				seen[id] = true
			}
		}
		next := append([]any{}, p.Arr(m["ids"])...)
		cursor := 0
		for i, id := range next {
			if e := p.Obj(entities(s, "section")[p.Str(id)]); e != nil && e["contextId"] == context {
				if cursor < len(ordered) {
					next[i] = ordered[cursor]
					cursor++
				}
			}
		}
		m["ids"] = next
	case "S5":
		id, task, source := p.Str(pl["sectionId"]), p.Str(pl["taskId"]), p.Str(pl["sourceSectionId"])
		if source != "" && source != id {
			if src := p.Obj(entities(s, "section")[source]); src != nil {
				src["taskIds"] = remove(p.Arr(src["taskIds"]), task)
			}
		}
		if target := p.Obj(entities(s, "section")[id]); target != nil {
			ids := p.Arr(target["taskIds"])
			if !has(ids, task) {
				ids = append(ids, task)
			}
			target["taskIds"] = moveAfter(ids, task, p.Str(pl["afterTaskId"]))
		}
	case "S6":
		id, task := p.Str(pl["sectionId"]), p.Str(pl["taskId"])
		if section := p.Obj(entities(s, "section")[id]); section != nil {
			section["taskIds"] = remove(p.Arr(section["taskIds"]), task)
		}
		model := "project"
		if pl["workContextType"] == "TAG" {
			model = "tag"
		}
		if ctx := p.Obj(entities(s, model)[p.Str(pl["workContextId"])]); ctx != nil {
			ctx["taskIds"] = moveAfter(p.Arr(ctx["taskIds"]), task, p.Str(pl["workContextAfterTaskId"]))
		}
	}
	return nil
}
func sanitizePanel(value p.Object) p.Object {
	out := p.Clone(value)
	if legacy, exists := out["projectId"]; exists {
		ids, arr := out["projectIds"].([]any)
		if _, ok := out["projectIds"]; !ok || (arr && len(ids) == 1 && ids[0] == "") {
			id := p.Str(legacy)
			out["projectIds"] = []any{id}
		}
		delete(out, "projectId")
	}
	ids, ok := out["projectIds"].([]any)
	if !ok || has(ids, "") {
		out["projectIds"] = []any{""}
	}
	if out["sortByDue"] == "asc" || out["sortByDue"] == "desc" {
		out["sortBy"] = "dueDate"
		out["sortDir"] = out["sortByDue"]
	}
	delete(out, "sortByDue")
	if !slices.Contains([]string{"dueDate", "created", "title", "timeEstimate"}, p.Str(out["sortBy"])) {
		delete(out, "sortBy")
	}
	for _, key := range []string{"sortDir", "includedTagsMatch", "excludedTagsMatch"} {
		if out[key] == nil {
			delete(out, key)
		}
	}
	return out
}
func sanitizeBoard(value p.Object) p.Object {
	out := p.Clone(value)
	panels := []any{}
	for _, panel := range p.Arr(out["panels"]) {
		panels = append(panels, sanitizePanel(p.Obj(panel)))
	}
	out["panels"] = panels
	return out
}
func applyBoard(s p.Object, a Action) error {
	m := p.Obj(s["boards"])
	if m == nil {
		return errors.New("boards model missing")
	}
	pl := a.Payload
	boards := p.Arr(m["boardCfgs"])
	switch a.Code {
	case "BA":
		m["boardCfgs"] = append(boards, sanitizeBoard(p.Obj(pl["board"])))
	case "BU":
		for _, raw := range boards {
			board := p.Obj(raw)
			if board["id"] == pl["id"] {
				ch := p.Clone(p.Obj(pl["updates"]))
				if panels := ch["panels"]; panels != nil {
					next := []any{}
					for _, v := range p.Arr(panels) {
						next = append(next, sanitizePanel(p.Obj(v)))
					}
					ch["panels"] = next
				}
				update(board, ch)
			}
		}
	case "BD":
		next := []any{}
		for _, raw := range boards {
			if p.Obj(raw)["id"] != pl["id"] {
				next = append(next, raw)
			}
		}
		m["boardCfgs"] = next
	case "BP":
	case "BT":
		found := false
		for _, raw := range boards {
			board := p.Obj(raw)
			for _, v := range p.Arr(board["panels"]) {
				if p.Obj(v)["id"] == pl["panelId"] {
					found = true
					break
				}
			}
			if found {
				for _, v := range p.Arr(board["panels"]) {
					if panel := p.Obj(v); panel["id"] == pl["panelId"] {
						panel["taskIds"] = p.Clone(pl["taskIds"])
					}
				}
				break
			}
		}
	case "BS":
		byID := map[string]any{}
		for _, raw := range boards {
			byID[p.Str(p.Obj(raw)["id"])] = raw
		}
		next := []any{}
		seen := map[string]bool{}
		for _, raw := range p.Arr(pl["ids"]) {
			id := p.Str(raw)
			seen[id] = true
			if b, ok := byID[id]; ok {
				next = append(next, b)
			}
		}
		for _, b := range boards {
			if !seen[p.Str(p.Obj(b)["id"])] {
				next = append(next, b)
			}
		}
		m["boardCfgs"] = next
	}
	return nil
}
func metricDefault(id string) p.Object {
	return p.Object{"id": id, "focusSessions": []any{}, "remindTomorrow": false, "reflections": []any{}}
}
func applyMetric(s p.Object, a Action) error {
	pl := a.Payload
	switch a.Code {
	case "EA":
		return entityAdd(s, "metric", p.Obj(pl["metric"]), false)
	case "EX":
		return entityAdd(s, "metric", p.Obj(pl["metric"]), true)
	case "ED":
		entityRemove(s, "metric", p.Str(pl["id"]))
	case "EU":
		u := p.Obj(pl["metric"])
		id := p.Str(u["id"])
		if entities(s, "metric")[id] == nil {
			obj := metricDefault(id)
			update(obj, p.Obj(u["changes"]))
			return entityAdd(s, "metric", obj, true)
		}
		return entityUpdate(s, "metric", u)
	case "EL":
		if p.Num(pl["duration"]) <= 0 {
			return nil
		}
		id := p.Str(pl["day"])
		obj := p.Obj(entities(s, "metric")[id])
		if obj == nil {
			obj = metricDefault(id)
			obj["focusSessions"] = []any{pl["duration"]}
			return entityAdd(s, "metric", obj, true)
		}
		obj["focusSessions"] = append(p.Arr(obj["focusSessions"]), pl["duration"])
	}
	return nil
}
func applyPlugin(s p.Object, a Action) error {
	model := "pluginUserData"
	deleteAction := "UD"
	if a.Code == "UM" || a.Code == "UDM" {
		model = "pluginMetadata"
		deleteAction = "UDM"
	}
	if _, ok := s[model].([]any); !ok {
		return errors.New(model + " model missing")
	}
	items := p.Arr(s[model])
	pl := a.Payload
	if a.Code == deleteAction {
		next := []any{}
		for _, v := range items {
			if p.Obj(v)["id"] != pl["pluginId"] {
				next = append(next, v)
			}
		}
		s[model] = next
		return nil
	}
	value := p.Obj(pl[model])
	exists := false
	next := []any{}
	for _, v := range items {
		if p.Obj(v)["id"] == value["id"] {
			next = append(next, p.Clone(value))
			exists = true
		} else {
			next = append(next, v)
		}
	}
	if !exists {
		next = append(next, p.Clone(value))
	}
	s[model] = next
	return nil
}
func applyMenu(s p.Object, a Action) error {
	m := p.Obj(s["menuTree"])
	if m == nil {
		return errors.New("menuTree missing")
	}
	pl := a.Payload
	if a.Code == "MP" {
		m["projectTree"] = p.Clone(pl["tree"])
		return nil
	}
	if a.Code == "MG" {
		m["tagTree"] = p.Clone(pl["tree"])
		return nil
	}
	key := "projectTree"
	if pl["treeType"] == "t" {
		key = "tagTree"
	} else if pl["treeType"] != "p" {
		return nil
	}
	var walk func([]any) []any
	walk = func(nodes []any) []any {
		out := []any{}
		for _, raw := range nodes {
			n := p.Clone(p.Obj(raw))
			if a.Code == "MD" && n["id"] == pl["folderId"] {
				continue
			}
			if n["k"] == "f" {
				if a.Code == "MU" && n["id"] == pl["folderId"] {
					n["name"] = pl["name"]
				} else {
					n["children"] = walk(p.Arr(n["children"]))
				}
			}
			out = append(out, n)
		}
		return out
	}
	m[key] = walk(p.Arr(m[key]))
	return nil
}

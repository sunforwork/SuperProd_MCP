package engine

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/sunforwork/SuperProd_MCP/internal/compat"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
)

//go:embed native-business-schema.generated.json
var businessSchemaJSON []byte
var businessSchema = func() p.Object {
	v, e := p.Parse(businessSchemaJSON)
	if e != nil || v["upstreamCommit"] != compat.Commit {
		panic("business schema baseline mismatch")
	}
	return v
}()

type ValidationIssue struct {
	Path string `json:"path"`
	Rule string `json:"rule"`
}
type BusinessValidationError struct {
	Issues []ValidationIssue `json:"issues"`
}

func (e *BusinessValidationError) Error() string {
	b, _ := json.Marshal(e)
	return "business validation failed: " + string(b)
}
func ptr(s string) string                       { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func issue(path, rule string) []ValidationIssue { return []ValidationIssue{{path, rule}} }

func businessSchemaIssues(rule p.Object, value any, path string, depth int) []ValidationIssue {
	if depth > 256 {
		return issue(path, "document nesting exceeds 256")
	}
	if ref := p.Str(rule["$ref"]); ref != "" {
		return businessSchemaIssues(p.Obj(p.Obj(p.Obj(businessSchema["components"])["schemas"])[strings.TrimPrefix(ref, "#/components/schemas/")]), value, path, depth+1)
	}
	if strings.HasSuffix(path, "/issueType") && len(p.Arr(rule["oneOf"])) > 0 {
		if v, ok := value.(string); ok && v != "" {
			return nil
		}
	}
	if options := p.Arr(rule["oneOf"]); len(options) > 0 {
		var best []ValidationIssue
		for _, option := range options {
			v := businessSchemaIssues(p.Obj(option), value, path, depth+1)
			if len(v) == 0 {
				return nil
			}
			if best == nil || len(v) < len(best) {
				best = v
			}
		}

		if strings.HasPrefix(path, "/issueProvider/entities/") && strings.Count(path, "/") == 3 {
			key := p.Str(p.Obj(value)["issueProviderKey"])
			known := false
			for _, option := range options {
				prop := p.Obj(p.Obj(p.Obj(option)["properties"])["issueProviderKey"])
				if len(businessSchemaIssues(prop, key, path+"/issueProviderKey", depth+1)) == 0 {
					known = true
				}
			}
			if key != "" && !known {
				return nil
			}
		}
		return best
	}
	if constant, ok := rule["const"]; ok {
		if isNumber(constant) && isNumber(value) && p.Num(constant) == p.Num(value) {
			return nil
		}
		a, _ := json.Marshal(constant)
		b, _ := json.Marshal(value)
		if string(a) != string(b) {
			return issue(path, "expected constant "+string(a))
		}
		return nil
	}
	switch p.Str(rule["type"]) {
	case "null":
		if value != nil {
			return issue(path, "expected null")
		}
	case "number":
		if !isNumber(value) {
			return issue(path, "expected number")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return issue(path, "expected boolean")
		}
	case "string":
		v, ok := value.(string)
		if !ok {
			return issue(path, "expected string")
		}
		if pattern := p.Str(rule["pattern"]); pattern != "" {
			rx, e := regexp.Compile(pattern)
			if e != nil {
				return issue(path, "unsupported pinned pattern")
			}
			if !rx.MatchString(v) {
				return issue(path, "string pattern mismatch")
			}
		}
	case "array":
		v, ok := value.([]any)
		if !ok {
			return issue(path, "expected array")
		}
		var out []ValidationIssue
		for i, item := range v {
			out = append(out, businessSchemaIssues(p.Obj(rule["items"]), item, fmt.Sprintf("%s/%d", path, i), depth+1)...)
			if len(out) >= 64 {
				break
			}
		}
		return out
	case "object":
		v := p.Obj(value)
		if v == nil {
			return issue(path, "expected object")
		}
		var out []ValidationIssue
		props := p.Obj(rule["properties"])
		for _, key := range p.Strings(rule["required"]) {
			if _, ok := v[key]; !ok {
				out = append(out, ValidationIssue{path + "/" + ptr(key), "required field missing"})
			}
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			r, exists := props[key]
			if !exists {
				r = rule["additionalProperties"]
			}
			if obj := p.Obj(r); obj != nil {
				out = append(out, businessSchemaIssues(obj, v[key], path+"/"+ptr(key), depth+1)...)
			}
			if len(out) >= 64 {
				break
			}
		}
		return out
	}
	return nil
}

func ValidateBusiness(d p.Object) error {

	validationView := d
	if syncCfg := p.Obj(p.Obj(d["globalConfig"])["sync"]); syncCfg != nil {
		if _, exists := syncCfg["syncInterval"]; !exists {
			validationView = p.Clone(d)
			p.Obj(p.Obj(validationView["globalConfig"])["sync"])["syncInterval"] = 0
		}
	}
	issues := businessSchemaIssues(p.Obj(businessSchema["schema"]), validationView, "", 0)
	if len(issues) > 0 {
		return &BusinessValidationError{issues}
	}
	add := func(path, rule string) {
		if len(issues) < 64 {
			issues = append(issues, ValidationIssue{path, rule})
		}
	}
	entityStates := []string{"task", "project", "tag", "note", "issueProvider", "metric", "simpleCounter", "section", "taskRepeatCfg", "archiveYoung/task", "archiveOld/task"}
	for _, name := range entityStates {
		var state p.Object
		if parts := strings.Split(name, "/"); len(parts) == 2 {
			state = p.Obj(p.Obj(d[parts[0]])[parts[1]])
		} else {
			state = p.Obj(d[name])
		}
		entities := p.Obj(state["entities"])
		ids := p.Arr(state["ids"])
		seen := map[string]bool{}
		for _, raw := range ids {
			id, ok := raw.(string)
			_, exists := entities[id]
			if !ok || !exists || seen[id] {
				add("/"+name+"/ids", "ids and entity keys must match exactly")
			}
			seen[id] = true
		}
		if len(ids) != len(entities) {
			add("/"+name, "ids and entities have different sizes")
		}
	}
	if len(issues) > 0 {
		return &BusinessValidationError{issues}
	}
	entities := func(name string) p.Object { return p.Obj(p.Obj(d[name])["entities"]) }
	tasks, projects, tags, notes, providers, repeats := entities("task"), entities("project"), entities("tag"), entities("note"), entities("issueProvider"), entities("taskRepeatCfg")
	has := func(es p.Object, id string) bool { return p.Obj(es[id]) != nil }
	for id, raw := range projects {
		pr := p.Obj(raw)
		base := "/project/entities/" + ptr(id)
		for _, key := range []string{"taskIds", "backlogTaskIds"} {
			for _, tid := range p.Strings(pr[key]) {
				if !has(tasks, tid) || p.Obj(tasks[tid])["projectId"] != pr["id"] {
					add(base+"/"+key, "missing task or inconsistent task projectId")
				}
			}
		}
		for _, nid := range p.Strings(pr["noteIds"]) {
			if !has(notes, nid) || p.Obj(notes[nid])["projectId"] != pr["id"] {
				add(base+"/noteIds", "missing note or inconsistent note projectId")
			}
		}
	}
	for id, raw := range tasks {
		task := p.Obj(raw)
		base := "/task/entities/" + ptr(id)
		if pid := p.Str(task["projectId"]); pid != "" && !has(projects, pid) {
			add(base+"/projectId", "project does not exist")
		}
		for _, tid := range p.Strings(task["tagIds"]) {
			if !has(tags, tid) {
				add(base+"/tagIds", "tag does not exist")
			}
		}
		if rid := p.Str(task["repeatCfgId"]); rid != "" && !has(repeats, rid) {
			add(base+"/repeatCfgId", "repeat configuration does not exist")
		}
		if p.Str(task["parentId"]) == "" && p.Str(task["projectId"]) == "" && len(p.Arr(task["tagIds"])) == 0 {
			add(base, "task without project or tag")
		}
	}
	for id, raw := range tags {
		if id == "TODAY" {
			continue
		}
		for _, tid := range p.Strings(p.Obj(raw)["taskIds"]) {
			if !has(tasks, tid) {
				add("/tag/entities/"+ptr(id)+"/taskIds", "task does not exist")
			}
		}
	}
	for _, nid := range p.Strings(p.Obj(d["note"])["todayOrder"]) {
		if !has(notes, nid) {
			add("/note/todayOrder", "note does not exist")
		}
	}
	young := p.Obj(p.Obj(p.Obj(d["archiveYoung"])["task"])["entities"])
	old := p.Obj(p.Obj(p.Obj(d["archiveOld"])["task"])["entities"])
	for _, group := range []struct {
		name      string
		es, other p.Object
	}{{"task", tasks, nil}, {"archiveYoung/task", young, old}, {"archiveOld/task", old, young}} {
		for id, raw := range group.es {
			task := p.Obj(raw)
			base := "/" + group.name + "/entities/" + ptr(id)
			if pid := p.Str(task["parentId"]); pid != "" && !has(group.es, pid) && !has(group.other, pid) {
				add(base+"/parentId", "parent does not exist in this task partition")
			}
			for _, sid := range p.Strings(task["subTaskIds"]) {
				if !has(group.es, sid) && !has(group.other, sid) {
					add(base+"/subTaskIds", "subtask does not exist in this task partition")
				}
			}
		}
	}
	for id, raw := range providers {
		ip := p.Obj(raw)
		if pid := p.Str(ip["defaultProjectId"]); pid != "" && !has(projects, pid) {
			add("/issueProvider/entities/"+ptr(id)+"/defaultProjectId", "project does not exist")
		}
	}
	var menu func([]any, string, string)
	menu = func(nodes []any, kind, path string) {
		for i, raw := range nodes {
			node := p.Obj(raw)
			at := fmt.Sprintf("%s/%d", path, i)
			id := p.Str(node["id"])
			switch p.Str(node["k"]) {
			case "f":
				if id == "" || p.Str(node["name"]) == "" {
					add(at, "folder requires id and name")
				}
				menu(p.Arr(node["children"]), kind, at+"/children")
			case "p":
				if kind != "projectTree" || !has(projects, id) {
					add(at, "invalid project menu reference")
				}
			case "t":
				if kind != "tagTree" || !has(tags, id) {
					add(at, "invalid tag menu reference")
				}
			default:
				add(at, "invalid menu node kind")
			}
		}
	}
	for _, kind := range []string{"projectTree", "tagTree"} {
		menu(p.Arr(p.Obj(d["menuTree"])[kind]), kind, "/menuTree/"+kind)
	}
	if len(issues) > 0 {
		return &BusinessValidationError{issues}
	}
	return nil
}

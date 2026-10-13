package engine

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sunforwork/SuperProd_MCP/internal/compat"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"regexp"
	"strings"
)

//go:embed native-task-schema.generated.json
var taskSchemaJSON []byte
var taskSchema = func() p.Object {
	schema, err := p.Parse(taskSchemaJSON)
	if err != nil || schema["upstreamCommit"] != compat.Commit {
		panic("invalid native Task contract")
	}
	return schema
}()

func matchesTaskSchema(schema p.Object, value any) bool {
	if schema == nil {
		return false
	}
	if ref := p.Str(schema["$ref"]); ref != "" {
		return matchesTaskSchema(p.Obj(p.Obj(p.Obj(taskSchema["components"])["schemas"])[strings.TrimPrefix(ref, "#/components/schemas/")]), value)
	}
	if options := p.Arr(schema["oneOf"]); len(options) > 0 {
		for _, option := range options {
			if matchesTaskSchema(p.Obj(option), value) {
				return true
			}
		}
		return false
	}
	if constant, ok := schema["const"]; ok {
		if isNumber(constant) && isNumber(value) {
			return p.Num(constant) == p.Num(value)
		}
		a, _ := json.Marshal(constant)
		b, _ := json.Marshal(value)
		return string(a) == string(b)
	}
	switch p.Str(schema["type"]) {
	case "null":
		return value == nil
	case "string":
		s, ok := value.(string)
		if !ok {
			return false
		}
		if pattern := p.Str(schema["pattern"]); pattern != "" {
			return regexp.MustCompile(pattern).MatchString(s)
		}
	case "number":
		return isNumber(value)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		array, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range array {
			if !matchesTaskSchema(p.Obj(schema["items"]), item) {
				return false
			}
		}
	case "object":
		object := p.Obj(value)
		if object == nil {
			return false
		}
		properties := p.Obj(schema["properties"])
		for _, key := range p.Strings(schema["required"]) {
			if _, ok := object[key]; !ok {
				return false
			}
		}
		for key, item := range object {
			if rule, ok := properties[key]; ok {
				if !matchesTaskSchema(p.Obj(rule), item) {
					return false
				}
			} else if rule := p.Obj(schema["additionalProperties"]); rule != nil {
				if !matchesTaskSchema(rule, item) {
					return false
				}
			}
		}
	}
	return true
}

func nativeTaskFieldRule(field string) p.Object {
	rule := p.Obj(taskSchema["schema"])
	if ref := p.Str(rule["$ref"]); ref != "" {
		rule = p.Obj(p.Obj(p.Obj(taskSchema["components"])["schemas"])[strings.TrimPrefix(ref, "#/components/schemas/")])
	}
	return p.Obj(p.Obj(rule["properties"])[field])
}

func jsFalsy(value any) bool {
	if value == nil {
		return true
	}
	if s, ok := value.(string); ok {
		return s == ""
	}
	if b, ok := value.(bool); ok {
		return !b
	}
	if isNumber(value) {
		return p.Num(value) == 0
	}
	return false
}

func newTaskWithDefaults(s p.Object, options p.Object, ct, ci string, c Context) (p.Object, error) {
	additional := p.Obj(options["additional"])
	task := defaultModel("task")
	task["created"], task["title"] = c.Now, options["title"]
	id := p.Str(options["id"])
	if id == "" {
		id = c.ID()
	}
	task["id"] = id
	project := func() string {
		if ct == "PROJECT" {
			return ci
		}
		if id := p.Str(p.Obj(p.Obj(s["globalConfig"])["tasks"])["defaultProjectId"]); id != "" {
			return id
		}
		return inboxID()
	}
	task["projectId"], task["tagIds"] = project(), []any{}
	if ct == "TAG" && jsFalsy(additional["parentId"]) && ci != "TODAY" {
		task["tagIds"] = []any{ci}
	}
	_, hasDueDay := additional["dueDay"]
	if ci == "TODAY" && jsFalsy(additional["parentId"]) && jsFalsy(additional["dueWithTime"]) && !hasDueDay {
		task["dueDay"] = c.CalendarToday
	}
	update(task, additional)
	for _, key := range []string{"dueDay", "deadlineDay"} {
		if date, ok := task[key].(string); ok && date != "" && !datePattern.MatchString(date) {
			delete(task, key)
		}
	}
	if jsFalsy(task["projectId"]) {
		task["projectId"] = project()
	}
	if !matchesTaskSchema(p.Obj(taskSchema["schema"]), task) {
		return nil, errors.New("task fields do not match the pinned client's Task type")
	}
	return task, nil
}

func compileAddChild(s p.Object, arg p.Object, c Context) ([]Action, error) {
	if p.Str(arg["titleMode"]) != "literal" {
		return nil, fmt.Errorf("%w: child short syntax; titleMode literal required", ErrUnsupported)
	}
	parentID := p.Str(arg["parentId"])
	if _, err := Entity(s, "task", parentID); err != nil {
		return nil, err
	}
	ct, ci := p.Str(arg["contextType"]), p.Str(arg["contextId"])
	model := "project"
	if ct == "TAG" {
		model = "tag"
	} else if ct != "PROJECT" {
		return nil, errors.New("explicit PROJECT or TAG context required")
	}
	if _, err := Entity(s, model, ci); err != nil {
		return nil, err
	}
	additional := p.Clone(p.Obj(arg["additional"]))
	if additional == nil {
		additional = p.Object{}
	}
	title := additional["title"]
	if jsFalsy(title) {
		title = ""
	}

	hasDay := false
	if _, hasDay = additional["dueDay"]; !hasDay {
		additional["dueDay"] = nil
	}
	task, err := newTaskWithDefaults(s, p.Object{"title": title, "additional": additional}, ct, ci, c)
	if err != nil {
		return nil, err
	}
	if !hasDay {
		delete(task, "dueDay")
	}
	return []Action{{Code: "TA", Kind: "CRT", Entity: "TASK", ID: p.Str(task["id"]), Payload: p.Object{"task": task, "parentId": parentID, "isIgnoreShortSyntax": true}}}, nil
}

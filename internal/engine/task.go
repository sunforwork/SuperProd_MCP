package engine

import (
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
)

func addUnique(ids []any, id string, bottom bool) []any {
	if has(ids, id) {
		return ids
	}
	if bottom {
		return append(ids, id)
	}
	return prepend(ids, id)
}
func applyAddTask(s p.Object, a Action, c Context) error {
	pl := a.Payload
	task := p.Clone(p.Obj(pl["task"]))
	if task == nil {
		return errors.New("task missing")
	}
	if _, ok := task["created"]; !ok {
		return fmt.Errorf("%w: task without creation timestamp", ErrUnsupported)
	}
	defaults := defaultModel("task")
	update(defaults, task)
	task = defaults
	id := p.Str(task["id"])
	task["tagIds"] = remove(p.Arr(task["tagIds"]), "TODAY")
	task["timeSpent"] = total(p.Obj(task["timeSpentOnDay"]))
	if p.Str(task["projectId"]) == "" {
		task["projectId"] = ""
	}
	model := p.Obj(s["task"])
	ents := p.Obj(model["entities"])
	if ents[id] == nil {
		ents[id] = task
		model["ids"] = append(p.Arr(model["ids"]), id)
	}
	bottom := p.Bool(pl["isAddToBottom"])
	if proj := p.Obj(entities(s, "project")[p.Str(task["projectId"])]); proj != nil && p.Str(task["parentId"]) == "" {
		field := "taskIds"
		if p.Bool(pl["isAddToBacklog"]) && p.Bool(proj["isEnableBacklog"]) {
			field = "backlogTaskIds"
		}
		proj[field] = addUnique(p.Arr(proj[field]), id, bottom)
	}
	tags := p.Strings(task["tagIds"])
	if p.Str(task["dueDay"]) == c.Today {
		tags = append(tags, "TODAY")
	}
	for _, tagID := range tags {
		if tag := p.Obj(entities(s, "tag")[tagID]); tag != nil {
			tag["taskIds"] = addUnique(p.Arr(tag["taskIds"]), id, bottom)
		}
	}
	if day := p.Str(task["dueDay"]); day != "" && day != c.Today {
		days := p.Obj(p.Obj(s["planner"])["days"])
		if days == nil {
			return errors.New("planner model missing")
		}
		days[day] = addUnique(p.Arr(days[day]), id, bottom)
	}
	if today := p.Str(pl["autoPlanToday"]); today != "" {
		autoPlanDeadline(s, id, today, p.Int(pl["autoPlanStartOfNextDayDiffMs"]), c)
	}
	return nil
}
func compileCreate(s p.Object, args p.Object, c Context) ([]Action, error) {
	if p.Str(args["titleMode"]) != "literal" {
		return nil, fmt.Errorf("%w: short syntax; titleMode must explicitly be literal", ErrUnsupported)
	}
	title, ok := args["title"].(string)
	if !ok {
		return nil, errors.New("title required")
	}
	ct, ci := p.Str(args["contextType"]), p.Str(args["contextId"])
	model := "project"
	if ct == "TAG" {
		model = "tag"
	} else if ct != "PROJECT" {
		return nil, errors.New("PROJECT or TAG context required")
	}
	if _, e := Entity(s, model, ci); e != nil {
		return nil, e
	}
	task, err := newTaskWithDefaults(s, p.Object{"title": title, "additional": args["additional"]}, ct, ci, c)
	if err != nil {
		return nil, err
	}
	project := p.Str(task["projectId"])
	if _, e := Entity(s, "project", project); e != nil {
		return nil, e
	}
	id := p.Str(task["id"])
	action := Action{Code: "HA", Kind: "CRT", Entity: "TASK", ID: id, Payload: p.Object{"task": task, "workContextId": ci, "workContextType": ct, "isAddToBacklog": p.Bool(args["isAddToBacklog"]), "isAddToBottom": p.Bool(args["isAddToBottom"]), "isIgnoreShortSyntax": true}}
	if deadlineToday(task, c.Today, DayOffset(s).Milliseconds(), c) {
		action.Payload["autoPlanToday"], action.Payload["autoPlanStartOfNextDayDiffMs"] = c.Today, DayOffset(s).Milliseconds()
	}
	return []Action{action}, nil
}
func compileDone(s p.Object, args p.Object, c Context, done bool) ([]Action, error) {
	id := p.Str(args["id"])
	task, e := Entity(s, "task", id)
	if e != nil {
		return nil, e
	}
	if p.Str(task["repeatCfgId"]) != "" {
		return nil, fmt.Errorf("%w: recurring completion", ErrUnsupported)
	}
	return []Action{taskFieldAction(id, p.Object{"isDone": done})}, nil
}

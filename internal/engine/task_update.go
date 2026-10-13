package engine

import (
	"encoding/json"
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"regexp"
	"time"
)

var clearedFieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func applyTaskUI(s p.Object, a Action) error {
	u := p.Obj(a.Payload["task"])
	task := p.Obj(entities(s, "task")[p.Str(u["id"])])
	if task == nil {
		return nil
	}
	update(task, p.Obj(u["changes"]))
	for i, raw := range p.Arr(a.Payload["clearedFields"]) {
		if i >= 32 {
			break
		}
		if key, ok := raw.(string); ok && key != "id" && clearedFieldName.MatchString(key) {
			delete(task, key)
		}
	}
	return nil
}

func validTaskProject(s p.Object, task p.Object, id string) bool {
	if parentID := p.Str(task["parentId"]); parentID != "" {
		return p.Obj(entities(s, "task")[parentID])["projectId"] == id
	}
	return id == "" || p.Obj(entities(s, "project")[id]) != nil
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int64, float64, json.Number:
		return true
	}
	return false
}

func applyTaskUpdate(s p.Object, a Action, c Context) error {
	u := p.Obj(a.Payload["task"])
	id := p.Str(u["id"])
	task := p.Obj(entities(s, "task")[id])
	if task == nil {
		return nil
	}
	before := p.Clone(task)
	ch := p.Clone(p.Obj(u["changes"]))
	if ch == nil {
		return errors.New("task changes missing")
	}
	if newID, exists := ch["id"]; exists && newID != id {
		return ErrUnsupported
	}
	if p.Bool(ch["isDone"]) {
		if day, ok := ch["dueDay"].(string); ok {
			_, oldDayString := before["dueDay"].(string)
			if oldDayString || isNumber(before["dueWithTime"]) || p.Str(before["parentId"]) != "" {
				completionDay := c.Today
				if isNumber(ch["doneOn"]) {
					completionDay = time.UnixMilli(p.Int(ch["doneOn"])).In(c.Location).Format("2006-01-02")
				}
				if day == completionDay && (day != before["dueDay"] || p.Str(before["parentId"]) != "") {
					delete(ch, "dueDay")
				}
			}
		}
		tags := p.Arr(ch["tagIds"])
		if tags == nil {
			tags = p.Arr(before["tagIds"])
		}
		if has(tags, "KANBAN_IN_PROGRESS") {
			ch["tagIds"] = remove(tags, "KANBAN_IN_PROGRESS")
		}
	}
	if project, exists := ch["projectId"]; exists {
		if target, ok := project.(string); !ok || !validTaskProject(s, task, target) {
			delete(ch, "projectId")
		}
	}
	if tags := p.Arr(ch["tagIds"]); tags != nil {
		applyTaskTags(s, id, p.Arr(before["tagIds"]), tags)
	}
	if target, exists := ch["projectId"].(string); exists {
		all := collectTaskIDs(s, []any{id})
		if footprint, ok := a.Payload["projectMoveSubTaskIds"]; ok {
			all = uniqueIDs(append([]any{id}, p.Arr(footprint)...))
		}
		cleanupSections(s, all, func(section p.Object) bool {
			return section["contextType"] == "PROJECT" && section["contextId"] != target
		})
		if p.Str(before["parentId"]) == "" {
			project := p.Obj(entities(s, "project")[target])
			original := p.Clone(project)
			cleanupLists(s, "project", all, "taskIds", "backlogTaskIds")
			if project != nil {
				if before["projectId"] == target {
					children := remove(all, id)
					active, backlog := p.Arr(original["taskIds"]), p.Arr(original["backlogTaskIds"])
					for _, child := range children {
						active = remove(active, p.Str(child))
						backlog = remove(backlog, p.Str(child))
					}
					active, backlog = uniqueIDs(active), uniqueIDs(backlog)
					if has(active, id) {
						backlog = remove(backlog, id)
					} else if !has(backlog, id) {
						active = append(active, id)
					}
					project["taskIds"], project["backlogTaskIds"] = active, backlog
				} else {
					project["taskIds"] = uniqueIDs(append(p.Arr(project["taskIds"]), id))
				}
			}
			for _, child := range remove(all, id) {
				if sub := p.Obj(entities(s, "task")[p.Str(child)]); sub != nil {
					sub["projectId"] = target
				}
			}
		}
	}
	if days := p.Obj(ch["timeSpentOnDay"]); days != nil {
		setTaskDays(s, task, days)
	}
	_, doneChanged := ch["isDone"]
	if isNumber(ch["timeEstimate"]) || doneChanged {
		if isNumber(ch["timeEstimate"]) {
			task["timeEstimate"] = ch["timeEstimate"]
		}
		if parent := p.Obj(entities(s, "task")[p.Str(task["parentId"])]); parent != nil {
			var left float64
			for _, sid := range p.Strings(parent["subTaskIds"]) {
				sub := p.Obj(entities(s, "task")[sid])
				if sub == nil {
					continue
				}
				sub = p.Clone(sub)
				if sid == id {
					update(sub, ch)
				}
				if !p.Bool(sub["isDone"]) {
					left += max(float64(0), p.Num(sub["timeEstimate"])-p.Num(sub["timeSpent"]))
				}
			}
			parent["timeEstimate"] = left
		}
	}
	if done, ok := ch["isDone"].(bool); ok {
		if done {
			task["doneOn"] = c.Now
			if isNumber(ch["doneOn"]) {
				task["doneOn"] = ch["doneOn"]
			}
		} else {
			delete(task, "doneOn")
		}
	}
	update(task, ch)
	task["modified"] = c.Now
	todayTag := p.Obj(entities(s, "tag")["TODAY"])
	if p.Bool(ch["isDone"]) && p.Str(before["parentId"]) == "" && before["dueDay"] == c.Today && todayTag != nil && !has(p.Arr(todayTag["taskIds"]), id) {
		todayTag["taskIds"] = append(p.Arr(todayTag["taskIds"]), id)
	}
	if day, changed := ch["dueDay"]; changed && day != before["dueDay"] {
		days := p.Obj(p.Obj(s["planner"])["days"])
		for key, ids := range days {
			days[key] = remove(p.Arr(ids), id)
		}
		if date := p.Str(day); date != "" && date != c.Today && !p.Bool(ch["isDone"]) {
			days[date] = addUnique(p.Arr(days[date]), id, true)
		}
		if todayTag != nil {
			if before["dueDay"] == c.Today && day != c.Today {
				todayTag["taskIds"] = remove(p.Arr(todayTag["taskIds"]), id)
				if has(p.Arr(task["tagIds"]), "TODAY") {
					task["tagIds"] = remove(p.Arr(task["tagIds"]), "TODAY")
				}
			} else if before["dueDay"] != c.Today && day == c.Today {
				todayTag["taskIds"] = addUnique(p.Arr(todayTag["taskIds"]), id, true)
			}
		}
	}
	return nil
}

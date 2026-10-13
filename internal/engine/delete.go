package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"sort"
)

func collectTaskIDs(s p.Object, primary []any) []any {
	ids := append([]any{}, primary...)
	for _, id := range primary {
		for _, child := range p.Strings(p.Obj(entities(s, "task")[p.Str(id)])["subTaskIds"]) {
			ids = addUnique(ids, child, true)
		}
	}
	for _, id := range p.Strings(p.Obj(s["task"])["ids"]) {
		t := p.Obj(entities(s, "task")[id])
		if parent := p.Str(t["parentId"]); parent != "" && has(primary, parent) {
			ids = addUnique(ids, id, true)
		}
	}
	return ids
}
func cleanupSections(s p.Object, ids []any, match func(p.Object) bool) {
	if len(ids) == 0 {
		return
	}
	for _, sid := range p.Strings(p.Obj(s["section"])["ids"]) {
		section := p.Obj(entities(s, "section")[sid])
		if section == nil || match != nil && !match(section) {
			continue
		}
		before := p.Arr(section["taskIds"])
		next := before
		for _, id := range ids {
			next = remove(next, p.Str(id))
		}
		if len(next) != len(before) {
			section["taskIds"] = next
		}
	}
}
func removeEntity(s p.Object, id string) {
	delete(entities(s, "task"), id)
	p.Obj(s["task"])["ids"] = remove(p.Arr(p.Obj(s["task"])["ids"]), id)
}
func removeFromParent(s p.Object, task p.Object) {
	parent := p.Obj(entities(s, "task")[p.Str(task["parentId"])])
	if parent == nil {
		return
	}
	wasLast := len(p.Arr(parent["subTaskIds"])) == 1
	parent["subTaskIds"] = remove(p.Arr(parent["subTaskIds"]), p.Str(task["id"]))
	if wasLast {
		parent["timeSpentOnDay"] = p.Clone(p.Obj(task["timeSpentOnDay"]))
		parent["timeEstimate"] = task["timeEstimate"]

		return
	}
	days := p.Object{}
	var estimate float64
	for _, id := range p.Strings(parent["subTaskIds"]) {
		child := p.Obj(entities(s, "task")[id])
		if child == nil {
			continue
		}
		for day, value := range p.Obj(child["timeSpentOnDay"]) {
			if p.Num(value) != 0 {
				days[day] = p.Num(days[day]) + p.Num(value)
			}
		}
		if !p.Bool(child["isDone"]) {
			estimate += max(float64(0), p.Num(child["timeEstimate"])-p.Num(child["timeSpent"]))
		}
	}
	parent["timeSpentOnDay"] = days
	parent["timeSpent"] = total(days)
	parent["timeEstimate"] = estimate
}
func calendarDismissals(tasks []any) []any {
	r := []any{}
	for _, raw := range tasks {
		t := p.Obj(raw)
		if t["issueType"] == "ICAL" && p.Str(t["issueProviderId"]) != "" && p.Str(t["issueId"]) != "" {
			r = append(r, p.Object{"issueProviderId": t["issueProviderId"], "issueId": t["issueId"]})
		}
	}
	return r
}
func applyDismissals(s p.Object, dismissals []any) {
	model := p.Obj(s["task"])
	m := p.Obj(model["dismissedCalendarAutoImportEventIdsByProvider"])
	for _, raw := range dismissals {
		d := p.Obj(raw)
		provider, id := p.Str(d["issueProviderId"]), p.Str(d["issueId"])
		if provider == "" || id == "" {
			continue
		}
		if m == nil {
			m = p.Object{}
		}
		if has(p.Arr(m[provider]), id) {
			continue
		}
		ids := append(p.Strings(m[provider]), id)
		sort.Strings(ids)
		m[provider] = p.Values(ids)
		model["dismissedCalendarAutoImportEventIdsByProvider"] = m
	}
}
func cleanupLists(s p.Object, model string, ids []any, fields ...string) {
	for _, id := range p.Strings(p.Obj(s[model])["ids"]) {
		entity := p.Obj(entities(s, model)[id])
		if entity == nil {
			continue
		}
		for _, field := range fields {
			before := p.Arr(entity[field])
			next := before
			for _, tid := range ids {
				next = remove(next, p.Str(tid))
			}
			if len(before) != len(next) {
				entity[field] = next
			}
		}
	}
}
func applyDeleteTasks(s p.Object, a Action) error {
	pl := a.Payload
	model := p.Obj(s["task"])
	if model == nil {
		return errors.New("task model missing")
	}
	if a.Code == "HD" {
		task := p.Obj(pl["task"])
		id := p.Str(task["id"])
		if id == "" {
			return nil
		}
		cleanupSections(s, collectTaskIDs(s, []any{id}), nil)
		all := append([]any{id}, p.Arr(task["subTaskIds"])...)
		for _, tid := range p.Strings(model["ids"]) {
			if p.Str(p.Obj(entities(s, "task")[tid])["parentId"]) == id {
				all = addUnique(all, tid, true)
			}
		}
		removeEntity(s, id)
		removeFromParent(s, task)
		for _, sub := range p.Strings(task["subTaskIds"]) {
			all = addUnique(all, sub, true)
		}
		for _, tid := range all {
			removeEntity(s, p.Str(tid))
		}
		if has(all, p.Str(model["currentTaskId"])) {
			model["currentTaskId"] = nil
		}
		applyDismissals(s, calendarDismissals(append([]any{task}, p.Arr(task["subTasks"])...)))
		if proj := p.Obj(entities(s, "project")[p.Str(task["projectId"])]); proj != nil {
			proj["taskIds"] = remove(p.Arr(proj["taskIds"]), id)
			proj["backlogTaskIds"] = remove(p.Arr(proj["backlogTaskIds"]), id)
		}

		cleanupLists(s, "tag", append([]any{id}, p.Arr(task["subTaskIds"])...), "taskIds")
		return nil
	}
	primary := p.Arr(pl["taskIds"])
	cleanupSections(s, collectTaskIDs(s, primary), nil)
	all := []any{}
	projects := []any{}
	parents := []any{}
	snapshots := []any{}
	for _, raw := range primary {
		id := p.Str(raw)
		task := p.Obj(entities(s, "task")[id])
		all = append(all, id)
		all = append(all, p.Arr(task["subTaskIds"])...)
		if pid := p.Str(task["projectId"]); pid != "" {
			projects = addUnique(projects, pid, true)
		}
	}
	for _, id := range all {
		if t := p.Obj(entities(s, "task")[p.Str(id)]); t != nil {
			snapshots = append(snapshots, p.Clone(t))
		}
	}
	for _, raw := range primary {
		parentID := p.Str(p.Obj(entities(s, "task")[p.Str(raw)])["parentId"])
		if parentID == "" || has(all, parentID) || has(parents, parentID) {
			continue
		}
		parent := p.Obj(entities(s, "task")[parentID])
		if parent == nil {
			continue
		}
		parents = append(parents, parentID)
		for _, subID := range p.Strings(parent["subTaskIds"]) {
			if !has(all, subID) {
				continue
			}
			sub := p.Obj(entities(s, "task")[subID])
			if sub != nil {
				removeEntity(s, subID)
				removeFromParent(s, sub)
			}
		}
	}
	for _, id := range all {
		removeEntity(s, p.Str(id))
	}
	if has(all, p.Str(model["currentTaskId"])) {
		model["currentTaskId"] = nil
	}
	applyDismissals(s, p.Arr(pl["calendarAutoImportDismissals"]))
	applyDismissals(s, calendarDismissals(snapshots))
	for _, raw := range projects {
		proj := p.Obj(entities(s, "project")[p.Str(raw)])
		if proj == nil {
			continue
		}
		for _, field := range []string{"taskIds", "backlogTaskIds"} {
			ids := p.Arr(proj[field])
			for _, tid := range all {
				ids = remove(ids, p.Str(tid))
			}
			proj[field] = ids
		}
	}
	cleanupLists(s, "tag", all, "taskIds")
	return nil
}
func compileDelete(s p.Object, cmd Command) ([]Action, error) {
	if cmd.Name == "task_delete" {
		id := p.Str(cmd.Args["id"])
		task, e := Entity(s, "task", id)
		if e != nil {
			return nil, e
		}
		full := p.Clone(task)
		subs := []any{}
		for _, sid := range p.Strings(task["subTaskIds"]) {
			if sub := p.Obj(entities(s, "task")[sid]); sub != nil {
				subs = append(subs, p.Clone(sub))
			}
		}
		full["subTasks"] = subs
		return []Action{{Code: "HD", ID: id, Entity: "TASK", Kind: "DEL", Payload: p.Object{"task": full}}}, nil
	}
	ids := p.Strings(cmd.Args["ids"])
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, errors.New("between 1 and 1000 task IDs required")
	}
	for _, id := range ids {
		if _, e := Entity(s, "task", id); e != nil {
			return nil, e
		}
	}
	all := []any{}
	for _, id := range ids {
		all = addUnique(all, id, true)
		for _, sub := range p.Strings(p.Obj(entities(s, "task")[id])["subTaskIds"]) {
			all = addUnique(all, sub, true)
		}
	}
	tasks := []any{}
	for _, id := range all {
		if task := p.Obj(entities(s, "task")[p.Str(id)]); task != nil {
			tasks = append(tasks, p.Clone(task))
		}
	}
	pl := p.Object{"taskIds": p.Values(ids), "tasks": tasks}
	if dismissals := calendarDismissals(tasks); len(dismissals) > 0 {
		pl["calendarAutoImportDismissals"] = dismissals
	}
	return []Action{{Code: "HDM", ID: ids[0], IDs: ids, Entity: "TASK", Kind: "DEL", Payload: pl}}, nil
}

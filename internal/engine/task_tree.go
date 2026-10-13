package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
)

func recalcParent(s p.Object, id string, estimate bool) {
	parent := p.Obj(entities(s, "task")[id])
	if parent == nil {
		return
	}
	days := p.Object{}
	var left float64
	for _, childID := range p.Strings(parent["subTaskIds"]) {
		child := p.Obj(entities(s, "task")[childID])
		if child == nil {
			continue
		}
		for day, value := range p.Obj(child["timeSpentOnDay"]) {
			if n := p.Num(value); n != 0 {
				days[day] = p.Num(days[day]) + n
			}
		}
		if !p.Bool(child["isDone"]) {
			left += max(float64(0), p.Num(child["timeEstimate"])-p.Num(child["timeSpent"]))
		}
	}
	parent["timeSpentOnDay"], parent["timeSpent"] = days, total(days)
	if estimate {
		parent["timeEstimate"] = left
	}
}

func setTaskDays(s p.Object, task p.Object, days p.Object) {
	old := p.Obj(task["timeSpentOnDay"])
	if parent := p.Obj(entities(s, "task")[p.Str(task["parentId"])]); parent != nil {
		pd := p.Clone(p.Obj(parent["timeSpentOnDay"]))
		if pd == nil {
			pd = p.Object{}
		}
		keys := map[string]bool{}
		for k := range old {
			keys[k] = true
		}
		for k := range days {
			keys[k] = true
		}
		var delta float64
		for day := range keys {
			d := p.Num(days[day]) - p.Num(old[day])
			if d == 0 {
				continue
			}
			delta += d
			if n := p.Num(pd[day]) + d; n > 0 {
				pd[day] = n
			} else {
				delete(pd, day)
			}
		}
		parent["timeSpentOnDay"], parent["timeSpent"] = pd, p.Num(parent["timeSpent"])+delta
	}
	task["timeSpentOnDay"], task["timeSpent"] = p.Clone(days), total(days)
}

func reorderSingle(ids []any, id, code string) []any {
	idx := -1
	for i, v := range ids {
		if v == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ids
	}
	target := idx
	switch code {
	case "TMU":
		target--
	case "TMD":
		target++
	case "TMT":
		target = 0
	case "TMB":
		target = len(ids) - 1
	}
	if target < 0 || target >= len(ids) || target == idx {
		return ids
	}

	next := append([]any{}, ids[:idx]...)
	next = append(next, ids[idx+1:]...)
	next = append(next, nil)
	copy(next[target+1:], next[target:])
	next[target] = id
	return next
}

func canNest(task, parent p.Object) bool {
	if task == nil || parent == nil || task["id"] == parent["id"] || p.Str(parent["parentId"]) != "" || len(p.Arr(task["subTaskIds"])) > 0 {
		return false
	}
	for _, key := range []string{"parentId", "repeatCfgId", "issueId", "issueProviderId", "issueType", "dueWithTime", "reminderId", "remindAt"} {
		v := task[key]
		if p.Str(v) != "" || p.Num(v) != 0 || p.Bool(v) {
			return false
		}
	}
	return true
}

func applyTaskTree(s p.Object, a Action, c Context) error {
	pl := a.Payload
	model, ents := p.Obj(s["task"]), entities(s, "task")
	switch a.Code {
	case "TA":
		parent := p.Obj(ents[p.Str(pl["parentId"])])
		if parent == nil {
			return nil
		}
		task := p.Clone(p.Obj(pl["task"]))
		if task == nil {
			return errors.New("task missing")
		}
		id := p.Str(task["id"])
		if len(p.Arr(parent["subTaskIds"])) == 0 {
			if len(p.Obj(task["timeSpentOnDay"])) == 0 {
				task["timeSpentOnDay"] = p.Clone(p.Obj(parent["timeSpentOnDay"]))
				task["timeSpent"] = total(p.Obj(parent["timeSpentOnDay"]))
			}
			if p.Num(task["timeEstimate"]) == 0 {
				task["timeEstimate"] = parent["timeEstimate"]
			}
		}
		task["parentId"], task["projectId"], task["tagIds"] = parent["id"], parent["projectId"], []any{}
		if ents[id] == nil {
			ents[id] = task
			model["ids"] = append(p.Arr(model["ids"]), id)
		}
		parent["subTaskIds"] = addUnique(p.Arr(parent["subTaskIds"]), id, true)
		if model["currentTaskId"] == parent["id"] {
			model["currentTaskId"] = id
		}
		recalcParent(s, p.Str(parent["id"]), true)
	case "TMU", "TMD", "TMT", "TMB":
		if parent := p.Obj(ents[p.Str(pl["parentId"])]); parent != nil {
			parent["subTaskIds"] = reorderSingle(p.Arr(parent["subTaskIds"]), p.Str(pl["id"]), a.Code)
		}
	case "TMS":
		id, source, target := p.Str(pl["taskId"]), p.Str(pl["srcTaskId"]), p.Str(pl["targetTaskId"])
		old, next := p.Obj(ents[source]), p.Obj(ents[target])
		if old == nil || next == nil || id == source || id == target {
			return nil
		}
		visited := map[string]bool{}
		for current := target; current != "" && !visited[current]; current = p.Str(p.Obj(ents[current])["parentId"]) {
			if current == id {
				return nil
			}
			visited[current] = true
		}
		old["subTaskIds"] = remove(p.Arr(old["subTaskIds"]), id)
		recalcParent(s, source, true)
		next["subTaskIds"] = moveAfter(p.Arr(next["subTaskIds"]), id, p.Str(pl["afterTaskId"]))
		recalcParent(s, target, true)
		if task := p.Obj(ents[id]); task != nil {
			task["parentId"], task["projectId"] = next["id"], next["projectId"]
		}
	case "HCS":
		id := p.Str(pl["taskId"])
		task, parent := p.Obj(ents[id]), p.Obj(ents[p.Str(pl["targetParentId"])])
		if !canNest(task, parent) {
			return nil
		}
		cleanupSections(s, []any{id}, nil)
		if project := p.Obj(entities(s, "project")[p.Str(task["projectId"])]); project != nil {
			project["taskIds"] = remove(p.Arr(project["taskIds"]), id)
			project["backlogTaskIds"] = remove(p.Arr(project["backlogTaskIds"]), id)
		}
		if today := p.Obj(entities(s, "tag")["TODAY"]); today != nil {
			today["taskIds"] = remove(p.Arr(today["taskIds"]), id)
		}
		for day, ids := range p.Obj(p.Obj(s["planner"])["days"]) {
			p.Obj(p.Obj(s["planner"])["days"])[day] = remove(p.Arr(ids), id)
		}
		parent["subTaskIds"] = moveAfter(p.Arr(parent["subTaskIds"]), id, p.Str(pl["afterTaskId"]))
		task["parentId"], task["projectId"], task["modified"] = parent["id"], parent["projectId"], c.Now
		delete(task, "dueDay")
		recalcParent(s, p.Str(parent["id"]), true)
	case "HC":
		snapshot := p.Obj(pl["task"])
		id := p.Str(snapshot["id"])
		parent := p.Obj(ents[p.Str(snapshot["parentId"])])
		if parent == nil {
			return errors.New("No parent for sub task")
		}
		task := p.Obj(ents[id])
		tags := p.Arr(task["tagIds"])
		if tags == nil {
			tags = p.Arr(snapshot["tagIds"])
		}
		tags = remove(tags, "TODAY")
		if len(tags) == 0 {
			tags = p.Arr(pl["parentTagIds"])
			if tags == nil {
				tags = p.Arr(parent["tagIds"])
			}
			tags = remove(tags, "TODAY")
		}
		parent["subTaskIds"] = remove(p.Arr(parent["subTaskIds"]), id)
		recalcParent(s, p.Str(parent["id"]), true)
		day := p.Str(pl["today"])
		if day == "" {
			day = c.Today
		}
		if task != nil {
			delete(task, "parentId")
			task["tagIds"] = tags
			task["modified"] = c.Now
			if v, ok := pl["modified"]; ok && v != nil {
				task["modified"] = v
			}
			if p.Bool(pl["isPlanForToday"]) && p.Num(snapshot["dueWithTime"]) == 0 {
				task["dueDay"] = day
			}
			if done, ok := pl["isDone"]; ok {
				task["isDone"] = done
				if p.Bool(done) {
					task["doneOn"] = c.Now
					if v := pl["doneOn"]; v != nil {
						task["doneOn"] = v
					}
					task["dueDay"] = day
					delete(task, "dueWithTime")
				} else {
					delete(task, "doneOn")
				}
			}
		}
		position := func(ids []any) []any {
			if pl["afterTaskId"] == nil && p.Bool(pl["isDone"]) {
				return append(remove(ids, id), id)
			}
			return moveAfter(ids, id, p.Str(pl["afterTaskId"]))
		}
		if project := p.Obj(entities(s, "project")[p.Str(snapshot["projectId"])]); project != nil {
			project["taskIds"] = position(p.Arr(project["taskIds"]))
		}
		if p.Bool(pl["isPlanForToday"]) {
			tags = append(append([]any{}, tags...), "TODAY")
		}
		for _, tagID := range tags {
			if tag := p.Obj(entities(s, "tag")[p.Str(tagID)]); tag != nil {
				tag["taskIds"] = position(p.Arr(tag["taskIds"]))
			}
		}
	case "TR":
		task := p.Obj(ents[p.Str(pl["id"])])
		if task == nil {
			return nil
		}
		days := p.Clone(p.Obj(task["timeSpentOnDay"]))
		if days == nil {
			days = p.Object{}
		}
		day := p.Str(pl["date"])
		days[day] = max(float64(0), p.Num(days[day])-p.Num(pl["duration"]))
		setTaskDays(s, task, days)
	case "TRD":
		factor := map[string]float64{"5M": 5, "QUARTER": 15, "HALF": 30, "HOUR": 60}[p.Str(pl["roundTo"])]
		parents := []any{}
		project, hasProject := pl["projectId"]
		limit := hasProject && (project == nil || p.Str(project) != "")
		for _, id := range p.Strings(pl["taskIds"]) {
			task := p.Obj(ents[id])
			if task == nil || len(p.Arr(task["subTaskIds"])) > 0 && p.Str(task["parentId"]) == "" || limit && task["projectId"] != project {
				continue
			}
			days := p.Clone(p.Obj(task["timeSpentOnDay"]))
			if days == nil {
				days = p.Object{}
			}
			day := p.Str(pl["day"])
			if factor > 0 {
				minutes := math.Floor(p.Num(days[day])/60000 + 0.5)
				rounded := math.Floor(minutes/factor + 0.5)
				if p.Bool(pl["isRoundUp"]) {
					rounded = math.Ceil(minutes / factor)
				}
				days[day] = int64(rounded * factor * 60000)
			}
			task["timeSpentOnDay"], task["timeSpent"] = days, total(days)
			if parent := p.Str(task["parentId"]); parent != "" {
				parents = addUnique(parents, parent, true)
			}
		}
		for _, id := range parents {
			recalcParent(s, p.Str(id), false)
		}
	}
	return nil
}

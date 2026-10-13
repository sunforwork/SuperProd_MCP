package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"sort"
	"strconv"
	"time"
)

func jsArrayIndex(id string) (uint64, bool) {
	n, e := strconv.ParseUint(id, 10, 32)
	return n, e == nil && n < 4294967295 && strconv.FormatUint(n, 10) == id
}

func activeTaskIDs(s p.Object) []string {
	ids := []string{}
	for _, id := range p.Strings(p.Obj(s["task"])["ids"]) {
		task := p.Obj(entities(s, "task")[id])
		if task != nil && !p.Bool(p.Obj(entities(s, "project")[p.Str(task["projectId"])])["isArchived"]) {
			ids = append(ids, id)
		}
	}

	sort.SliceStable(ids, func(i, j int) bool {
		a, ai := jsArrayIndex(ids[i])
		b, bi := jsArrayIndex(ids[j])
		if ai != bi {
			return ai
		}
		return ai && a < b
	})
	return ids
}

func orderContextMembers(s p.Object, members map[string]bool, stored []string) []string {
	positions := map[string]int{}
	for i, id := range stored {
		positions[id] = i
	}
	ordered, unordered := []string{}, []string{}
	for _, id := range activeTaskIDs(s) {
		if !members[id] || members[p.Str(p.Obj(entities(s, "task")[id])["parentId"])] {
			continue
		}
		if _, exists := positions[id]; exists {
			ordered = append(ordered, id)
		} else {
			unordered = append(unordered, id)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return positions[ordered[i]] < positions[ordered[j]] })
	return append(ordered, unordered...)
}

func WorkContextView(s p.Object, ct, ci string, now time.Time, location *time.Location) (p.Object, error) {
	ctx := NewContext(s, now, location)
	var view p.Object
	switch ct {
	case "PROJECT":
		project := p.Obj(entities(s, "project")[ci])
		if project == nil {
			return WorkContextView(s, "TAG", "TODAY", now, location)
		}
		view = p.Clone(project)
		if view["icon"] == nil {
			view["icon"] = nil
		}
		view["taskIds"], view["backlogTaskIds"] = p.Values(p.Strings(project["taskIds"])), p.Values(p.Strings(project["backlogTaskIds"]))
		view["type"], view["routerLink"] = "PROJECT", "project/"+p.Str(project["id"])
	case "TAG":
		tag, err := Entity(s, "tag", ci)
		if err != nil {
			return nil, err
		}
		view = p.Clone(tag)
		var ordered []string
		if ci == "TODAY" {
			today, err := TodaySchedule(s, now, location)
			if err != nil {
				return nil, err
			}
			ordered = p.Strings(today["taskIds"])
		} else {
			members := map[string]bool{}
			for _, id := range activeTaskIDs(s) {
				if has(p.Arr(p.Obj(entities(s, "task")[id])["tagIds"]), ci) {
					members[id] = true
				}
			}
			ordered = orderContextMembers(s, members, p.Strings(tag["taskIds"]))
		}
		view["taskIds"], view["type"], view["routerLink"] = p.Values(ordered), "TAG", "tag/"+p.Str(tag["id"])
		delete(view, "noteIds")
		if order, ok := p.Obj(s["note"])["todayOrder"]; ok {
			view["noteIds"] = p.Clone(order)
		}
	default:
		return nil, errors.New("explicit PROJECT or TAG context required")
	}
	undone := func(ids []any) []any {
		out := []any{}
		for _, id := range ids {
			if !p.Bool(p.Obj(entities(s, "task")[p.Str(id)])["isDone"]) {
				out = append(out, id)
			}
		}
		return out
	}
	trackable := []any{}
	for _, id := range p.Strings(view["taskIds"]) {
		task := p.Obj(entities(s, "task")[id])
		if task == nil {
			continue
		}
		children := p.Strings(task["subTaskIds"])
		if len(children) == 0 {
			trackable = append(trackable, p.Clone(task))
		} else {
			for _, childID := range children {
				if child := p.Obj(entities(s, "task")[childID]); child != nil {
					trackable = append(trackable, p.Clone(child))
				}
			}
		}
	}
	sort.SliceStable(trackable, func(i, j int) bool {
		return !p.Bool(p.Obj(trackable[i])["isDone"]) && p.Bool(p.Obj(trackable[j])["isDone"])
	})
	startable := []any{}
	for _, task := range trackable {
		if !p.Bool(p.Obj(task)["isDone"]) {
			startable = append(startable, task)
		}
	}
	out := p.Object{"today": ctx.Today, "context": view, "undoneTaskIds": undone(p.Arr(view["taskIds"])), "trackableTasks": trackable, "startableTasks": startable}
	if _, ok := view["backlogTaskIds"]; ok {
		out["undoneBacklogTaskIds"] = undone(p.Arr(view["backlogTaskIds"]))
	}
	return out, nil
}

func moveSkippingDone(ids []any, id, direction string, undone []any) []any {
	old := -1
	for i, value := range ids {
		if value == id {
			old = i
			break
		}
	}
	if old < 0 {
		return ids
	}
	next := old
	switch direction {
	case "up":
		next--
		for next > 0 && !has(undone, p.Str(ids[next])) {
			next--
		}
	case "down":
		next++
		for next < len(ids) && !has(undone, p.Str(ids[next])) {
			next++
		}
	case "top":
		next = 0
	case "bottom":
		next = len(ids) - 1
	}
	if next < 0 || next >= len(ids) || next == old {
		return ids
	}
	out := append([]any{}, ids...)
	value := out[old]
	out = append(out[:old], out[old+1:]...)
	out = append(out, nil)
	copy(out[next+1:], out[next:])
	out[next] = value
	return out
}

func applyContextMove(s p.Object, a Action) error {
	pl := a.Payload
	ct, ci, id := p.Str(pl["workContextType"]), p.Str(pl["workContextId"]), p.Str(pl["taskId"])
	backlog := a.Code == "PMU" || a.Code == "PMD" || a.Code == "PMT" || a.Code == "PMB" || a.Code == "PM"
	if backlog {
		ct = "PROJECT"
	}
	model := "project"
	if ct == "TAG" {
		model = "tag"
	} else if ct != "PROJECT" {
		return nil
	}
	context, err := Entity(s, model, ci)
	if err != nil {
		return err
	}
	field, undone := "taskIds", p.Arr(pl["doneTaskIds"])
	if backlog {
		field, undone = "backlogTaskIds", p.Arr(pl["doneBacklogTaskIds"])
	}
	direction := map[string]string{"WMU": "up", "WMD": "down", "WMT": "top", "WMB": "bottom", "PMU": "up", "PMD": "down", "PMT": "top", "PMB": "bottom"}[a.Code]
	ids := p.Arr(context[field])
	if direction != "" {

		if !backlog {
			for _, sectionID := range p.Strings(p.Obj(s["section"])["ids"]) {
				section := p.Obj(entities(s, "section")[sectionID])
				if section != nil && section["contextType"] == ct && section["contextId"] == ci && has(p.Arr(section["taskIds"]), id) {
					section["taskIds"] = moveSkippingDone(p.Arr(section["taskIds"]), id, direction, undone)
				}
			}
		}
		context[field] = moveSkippingDone(ids, id, direction, undone)
	} else if pl["afterTaskId"] == nil && pl["target"] == "DONE" && !backlog {
		context[field] = append(remove(ids, id), id)
	} else {
		context[field] = moveAfter(ids, id, p.Str(pl["afterTaskId"]))
	}
	return nil
}

func compileContextMove(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id, ct, ci := p.Str(arg["id"]), p.Str(arg["contextType"]), p.Str(arg["contextId"])
	task, err := Entity(s, "task", id)
	if err != nil {
		return nil, err
	}
	model := "project"
	if ct == "TAG" {
		model = "tag"
	} else if ct != "PROJECT" {
		return nil, errors.New("explicit PROJECT or TAG context required")
	}
	if _, err := Entity(s, model, ci); err != nil {
		return nil, err
	}
	view, err := WorkContextView(s, ct, ci, time.UnixMilli(c.Now), c.Location)
	if err != nil {
		return nil, err
	}
	direction := map[string]string{"task_move_up": "up", "task_move_down": "down", "task_move_to_top": "top", "task_move_to_bottom": "bottom"}[cmd.Name]
	parentID := p.Str(task["parentId"])
	if parentID != "" {
		context := p.Obj(view["context"])
		asMain := has(p.Arr(context["taskIds"]), id) || has(p.Arr(context["backlogTaskIds"]), id)
		if direction == "top" || direction == "bottom" || !asMain {
			parent, err := Entity(s, "task", parentID)
			if err != nil {
				return nil, err
			}
			children := p.Arr(parent["subTaskIds"])
			boundary := len(children) > 0 && ((direction == "up" && children[0] == id) || (direction == "down" && children[len(children)-1] == id))
			if boundary {
				args := p.Clone(arg)
				args["id"], args["isBacklog"] = parentID, false
				return compileContextMove(s, Command{Name: cmd.Name, Args: args}, c)
			}
			code := map[string]string{"up": "TMU", "down": "TMD", "top": "TMT", "bottom": "TMB"}[direction]
			return []Action{{Code: code, Entity: "TASK", ID: id, Kind: "MOV", Payload: p.Object{"id": id, "parentId": parentID}}}, nil
		}
	}
	pl := p.Object{"taskId": id, "workContextId": ci, "workContextType": ct, "doneTaskIds": view["undoneTaskIds"]}
	code := map[string]string{"up": "WMU", "down": "WMD", "top": "WMT", "bottom": "WMB"}[direction]
	entity, entityID := ct, ci
	if p.Bool(arg["isBacklog"]) {
		if ct != "PROJECT" {
			return nil, errors.New("backlog requires PROJECT context")
		}
		code = map[string]string{"up": "PMU", "down": "PMD", "top": "PMT", "bottom": "PMB"}[direction]
		pl = p.Object{"taskId": id, "workContextId": ci, "doneBacklogTaskIds": view["undoneBacklogTaskIds"]}
		entity, entityID = "TASK", id
	}
	return []Action{{Code: code, Entity: entity, ID: entityID, Kind: "MOV", Payload: pl}}, nil
}

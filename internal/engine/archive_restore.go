package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
	"slices"
	"time"
)

type BusinessDocument struct {
	Envelope p.Object
	State    p.Object
}

func NewBusinessDocument(envelope p.Object) BusinessDocument {
	return BusinessDocument{Envelope: envelope, State: p.Obj(envelope["state"])}
}

func shouldClearDueTime(value any, today string, offset int64, c Context) bool {
	if value == nil {
		return false
	}
	n := p.Num(value)
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > 8640000000000000 {
		return true
	}
	loc := c.Location
	if loc == nil {
		loc = time.UTC
	}
	return time.UnixMilli(int64(n)-offset).In(loc).Format("2006-01-02") != today
}

func (d BusinessDocument) Compile(cmd Command, c Context) ([]Action, error) {
	if cmd.Name == "time_set_day" && p.Obj(entities(d.State, "task")[p.Str(cmd.Args["id"])]) == nil {
		for _, key := range []string{"archiveYoung", "archiveOld"} {
			archive := p.Obj(d.Envelope[key])
			if p.Obj(entities(archive, "task")[p.Str(cmd.Args["id"])]) != nil {
				actions, err := compileHistoryTimeEdit(p.Object{"task": archive["task"]}, cmd.Args)
				for i := range actions {
					actions[i].archiveTarget = key
				}
				return actions, err
			}
		}
		return nil, errors.New("Archive task to update not found")
	}
	if cmd.Name != "task_restore" {
		return Compile(d.State, cmd, c)
	}
	placement := p.Str(cmd.Args["placement"])
	if placement != "original" && placement != "today" {
		return nil, errors.New("placement required: original or today")
	}
	archive := mergedArchiveTasks(d.Envelope)
	task := p.Clone(p.Obj(p.Obj(archive["entities"])[p.Str(cmd.Args["id"])]))
	if task == nil {
		return nil, errors.New("archived task not found")
	}
	children := []any{}
	for _, id := range p.Strings(task["subTaskIds"]) {
		if child := p.Obj(p.Obj(archive["entities"])[id]); child != nil {
			children = append(children, p.Clone(child))
		}
	}
	pl := p.Object{"task": task, "subTasks": children}
	if placement == "today" {
		offset := DayOffset(d.State).Milliseconds()
		pl["restoreToToday"] = p.Object{"today": c.Today, "startOfNextDayDiffMs": offset}
		task["dueDay"] = c.Today
		delete(task, "remindAt")
		if shouldClearDueTime(task["dueWithTime"], c.Today, offset, c) {
			delete(task, "dueWithTime")
		}
		for _, raw := range children {
			child := p.Obj(raw)
			delete(child, "dueDay")
			delete(child, "dueWithTime")
			delete(child, "remindAt")
		}
	}
	a := Action{Code: "HR", Kind: "UPD", Entity: "TASK", ID: p.Str(task["id"]), Payload: pl}

	preview := NewBusinessDocument(p.Clone(d.Envelope))
	if err := preview.ApplyLocal(a, c); err != nil {
		return nil, err
	}
	return []Action{a}, nil
}

func (d BusinessDocument) Apply(a Action, c Context) error {
	if err := Apply(d.State, a, c); err != nil {
		return err
	}
	if a.Code == "HR" {
		task := p.Obj(a.Payload["task"])
		ids := append([]any{task["id"]}, p.Arr(task["subTaskIds"])...)
		return deleteRestoredArchiveTasks(d.Envelope, ids)
	}
	if a.Code == "HU" {
		id := p.Str(p.Obj(a.Payload["task"])["id"])
		for _, key := range []string{"archiveYoung", "archiveOld"} {
			if p.Obj(entities(p.Obj(d.Envelope[key]), "task")[id]) != nil {
				return d.applyArchiveUpdate(key, a, c)
			}
		}
	}
	return nil
}

func (d BusinessDocument) ApplyLocal(a Action, c Context) error {
	if a.archiveTarget != "" {
		if err := d.applyArchiveUpdate(a.archiveTarget, a, c); err != nil {
			return err
		}
	}
	if err := Apply(d.State, a, c); err != nil {
		return err
	}
	if a.Code == "HR" {
		task := p.Obj(a.Payload["task"])
		return deleteRestoredArchiveTasks(d.Envelope, append([]any{task["id"]}, p.Arr(task["subTaskIds"])...))
	}
	return nil
}

func (d BusinessDocument) applyArchiveUpdate(key string, a Action, c Context) error {
	archive := p.Obj(d.Envelope[key])
	state := p.Object{"task": p.Obj(archive["task"]), "project": p.Object{"ids": []any{}, "entities": p.Object{}}, "tag": p.Object{"ids": []any{}, "entities": p.Object{}}, "planner": p.Object{"days": p.Object{}}}
	return applyTaskUpdate(state, a, c)
}

func mergedArchiveTasks(envelope p.Object) p.Object {
	ents := p.Object{}
	for _, key := range []string{"archiveOld", "archiveYoung"} {
		for id, value := range entities(p.Obj(envelope[key]), "task") {
			ents[id] = p.Clone(value)
		}
	}
	ids := []any{}
	for _, key := range []string{"archiveYoung", "archiveOld"} {
		for _, id := range p.Strings(p.Obj(p.Obj(envelope[key])["task"])["ids"]) {
			if p.Obj(ents[id]) != nil {
				ids = addUnique(ids, id, true)
			}
		}
	}

	normalize := false
	for _, id := range p.Strings(ids) {
		if jsFalsy(p.Obj(ents[id])["timeSpentOnDay"]) {
			normalize = true
		}
	}
	if normalize {
		next := p.Object{}
		for _, id := range p.Strings(ids) {
			task := p.Obj(ents[id])
			if jsFalsy(task["timeSpentOnDay"]) {
				task["timeSpentOnDay"] = p.Object{}
			}
			next[id] = task
		}
		ents = next
	}
	return p.Object{"ids": ids, "entities": ents}
}

func normalizeRestoredTask(s, task p.Object) p.Object {
	task = p.Clone(task)
	if project := p.Str(task["projectId"]); project != "" && p.Obj(entities(s, "project")[project]) == nil {
		delete(task, "projectId")
		if p.Obj(entities(s, "project")["INBOX_PROJECT"]) != nil {
			task["projectId"] = "INBOX_PROJECT"
		}
	}
	tags := []any{}
	for _, id := range p.Strings(task["tagIds"]) {
		if id != "TODAY" && p.Obj(entities(s, "tag")[id]) != nil {
			tags = append(tags, id)
		}
	}
	task["tagIds"] = tags
	if id := p.Str(task["repeatCfgId"]); id == "" || p.Obj(entities(s, "taskRepeatCfg")[id]) == nil {
		delete(task, "repeatCfgId")
	}
	return task
}

func prototypeProperty(id string) bool {
	return slices.Contains([]string{"constructor", "__defineGetter__", "__defineSetter__", "hasOwnProperty", "__lookupGetter__", "__lookupSetter__", "isPrototypeOf", "propertyIsEnumerable", "toString", "valueOf", "__proto__", "toLocaleString"}, id)
}

func applyRestoreTask(s p.Object, a Action, c Context) error {
	pl, ents := a.Payload, entities(s, "task")
	snapshot := p.Obj(pl["task"])
	if snapshot == nil {
		return errors.New("restore task snapshot missing")
	}
	id := p.Str(snapshot["id"])
	if prototypeProperty(id) || p.Obj(ents[id])["id"] == id {
		return planRestoredTaskToday(s, a, c)
	}
	sectionIDs := collectTaskIDs(s, []any{id})
	for _, childID := range p.Strings(snapshot["subTaskIds"]) {
		child := p.Obj(ents[childID])
		if child == nil || child["parentId"] == id {
			sectionIDs = addUnique(sectionIDs, childID, true)
		}
	}
	for _, raw := range p.Arr(pl["subTasks"]) {
		child := p.Obj(raw)
		existing := p.Obj(ents[p.Str(child["id"])])
		if existing["parentId"] == id || existing == nil && child["parentId"] == id {
			sectionIDs = addUnique(sectionIDs, p.Str(child["id"]), true)
		}
	}
	cleanupSections(s, sectionIDs, func(section p.Object) bool { return section["contextType"] == "PROJECT" })
	root := normalizeRestoredTask(s, snapshot)
	root["isDone"] = false
	delete(root, "doneOn")
	if root["projectId"] == nil {
		root["projectId"] = ""
	}
	declared := p.Arr(root["subTaskIds"])
	restoredChildren := []any{}
	for _, raw := range p.Arr(pl["subTasks"]) {
		child := p.Obj(raw)
		childID := p.Str(child["id"])
		existing := p.Obj(ents[childID])
		belongs := child["parentId"] == id || jsFalsy(child["parentId"]) && has(declared, childID)
		if belongs && (existing == nil || existing["parentId"] == id) {
			child = normalizeRestoredTask(s, child)
			child["parentId"], child["projectId"] = id, root["projectId"]
			restoredChildren = append(restoredChildren, child)
		}
	}
	for _, raw := range append([]any{root}, restoredChildren...) {
		task := p.Obj(raw)
		taskID := p.Str(task["id"])
		if ents[taskID] == nil {
			ents[taskID] = task
			p.Obj(s["task"])["ids"] = append(p.Arr(p.Obj(s["task"])["ids"]), taskID)
		}
	}
	candidates := append([]any{id}, declared...)
	for _, raw := range restoredChildren {
		candidates = addUnique(candidates, p.Str(p.Obj(raw)["id"]), true)
	}
	candidates = uniqueIDs(append(candidates, collectTaskIDs(s, []any{id})...))
	children := []any{}
	for _, childID := range p.Strings(candidates) {
		if childID != id && p.Obj(ents[childID])["parentId"] == id {
			children = addUnique(children, childID, true)
			p.Obj(ents[childID])["projectId"] = root["projectId"]
		}
	}
	root["subTaskIds"] = children
	all := append([]any{id}, children...)
	for _, project := range entities(s, "project") {
		pobj := p.Obj(project)
		for _, field := range []string{"taskIds", "backlogTaskIds"} {
			before := p.Arr(pobj[field])
			next := before
			for _, taskID := range p.Strings(all) {
				next = remove(next, taskID)
			}
			if len(next) != len(before) {
				pobj[field] = next
			}
		}
	}
	if project := p.Obj(entities(s, "project")[p.Str(root["projectId"])]); project != nil {
		project["taskIds"] = uniqueIDs(append(p.Arr(project["taskIds"]), id))
	}
	for _, taskID := range p.Strings(all) {
		for _, tagID := range p.Strings(p.Obj(ents[taskID])["tagIds"]) {
			if tag := p.Obj(entities(s, "tag")[tagID]); tag != nil {
				tag["taskIds"] = uniqueIDs(append(p.Arr(tag["taskIds"]), taskID))
			}
		}
	}
	return planRestoredTaskToday(s, a, c)
}

func planRestoredTaskToday(s p.Object, a Action, c Context) error {
	if today := p.Obj(a.Payload["restoreToToday"]); today != nil {
		id := p.Str(p.Obj(a.Payload["task"])["id"])
		return applyPlanToday(s, Action{Code: "HPT", Payload: p.Object{"taskIds": []any{id}, "today": today["today"], "startOfNextDayDiffMs": today["startOfNextDayDiffMs"], "parentTaskMap": p.Object{}}}, c)
	}
	return nil
}

func emptyArchive() p.Object {
	return p.Object{"task": p.Object{"ids": []any{}, "entities": p.Object{}}, "timeTracking": p.Object{"tag": p.Object{}, "project": p.Object{}}, "lastTimeTrackingFlush": 0}
}

func deleteRestoredArchiveTasks(envelope p.Object, ids []any) error {
	deleteFrom := func(key string) (int, error) {
		archive := p.Obj(envelope[key])
		if archive == nil {
			archive = emptyArchive()
		}
		found := []any{}
		for _, id := range p.Strings(ids) {
			if p.Obj(entities(archive, "task")[id]) != nil {
				found = append(found, id)
			}
		}
		if key == "archiveYoung" && len(found) == 0 {
			return 0, nil
		}
		state := p.Object{"task": p.Obj(archive["task"]), "project": p.Object{"ids": []any{}, "entities": p.Object{}}, "tag": p.Object{"ids": []any{}, "entities": p.Object{}}, "planner": p.Object{"days": p.Object{}}}
		if err := applyDeleteTasks(state, Action{Code: "HDM", Payload: p.Object{"taskIds": found}}); err != nil {
			return 0, err
		}
		envelope[key] = archive
		return len(found), nil
	}
	found, err := deleteFrom("archiveYoung")
	if err != nil {
		return err
	}

	if found < len(ids) {
		_, err = deleteFrom("archiveOld")
	}
	return err
}

package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"slices"
)

func uniqueIDs(ids []any) []any {
	r := []any{}
	for _, id := range ids {
		r = addUnique(r, p.Str(id), true)
	}
	return r
}
func taskWithChildren(s p.Object, task p.Object) p.Object {
	full := p.Clone(task)
	subs := []any{}
	for _, id := range p.Strings(task["subTaskIds"]) {
		if sub := p.Obj(entities(s, "task")[id]); sub != nil {
			subs = append(subs, p.Clone(sub))
		}
	}
	full["subTasks"] = subs
	return full
}
func applyTaskTags(s p.Object, id string, old, next []any) {
	for _, raw := range old {
		tagID := p.Str(raw)
		if tagID == "TODAY" || has(next, tagID) {
			continue
		}
		if tag := p.Obj(entities(s, "tag")[tagID]); tag != nil {
			tag["taskIds"] = remove(p.Arr(tag["taskIds"]), id)
		}
	}
	for _, raw := range next {
		tagID := p.Str(raw)
		if tagID == "TODAY" || has(old, tagID) {
			continue
		}
		if tag := p.Obj(entities(s, "tag")[tagID]); tag != nil {
			tag["taskIds"] = uniqueIDs(prepend(p.Arr(tag["taskIds"]), id))
		}
	}
}
func applyTaskContent(s p.Object, a Action) error {
	pl := a.Payload
	if a.Code == "HMP" {
		snapshot := p.Obj(pl["task"])
		id := p.Str(snapshot["id"])
		task := p.Obj(entities(s, "task")[id])
		target := p.Str(pl["targetProjectId"])
		old := p.Str(task["projectId"])
		if old != "" && old != target {
			cleanupSections(s, collectTaskIDs(s, []any{id}), func(section p.Object) bool { return section["contextType"] == "PROJECT" && section["contextId"] == old })
		}
		if task["projectId"] == nil {
			old = p.Str(snapshot["projectId"])
		}
		all := uniqueIDs(append(append([]any{id}, p.Arr(task["subTaskIds"])...), p.Arr(snapshot["subTaskIds"])...))
		if project := p.Obj(entities(s, "project")[old]); project != nil {
			for _, field := range []string{"taskIds", "backlogTaskIds"} {
				ids := p.Arr(project[field])
				for _, raw := range all {
					ids = remove(ids, p.Str(raw))
				}
				project[field] = ids
			}
		}
		if project := p.Obj(entities(s, "project")[target]); project != nil {
			project["taskIds"] = uniqueIDs(append(p.Arr(project["taskIds"]), id))
		}
		for _, raw := range all {
			if t := p.Obj(entities(s, "task")[p.Str(raw)]); t != nil {
				t["projectId"] = target
			}
		}
		return nil
	}
	id := p.Str(pl["taskId"])
	task, e := Entity(s, "task", id)
	if e != nil {
		return e
	}
	attachments := p.Arr(task["attachments"])
	if _, ok := task["attachments"].([]any); !ok {
		return errors.New("task attachments must be an array")
	}
	switch a.Code {
	case "XA":
		task["attachments"] = append(attachments, p.Clone(p.Obj(pl["taskAttachment"])))
	case "XU":
		patch := p.Obj(pl["taskAttachment"])
		for _, raw := range attachments {
			item := p.Obj(raw)
			if item["id"] == patch["id"] {
				update(item, p.Obj(patch["changes"]))
			}
		}
		task["attachments"] = attachments
	case "XD":
		next := []any{}
		for _, raw := range attachments {
			if p.Obj(raw)["id"] != pl["id"] {
				next = append(next, raw)
			}
		}
		task["attachments"] = next
	}
	return nil
}
func compileTaskContent(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id := p.Str(arg["id"])
	task, e := Entity(s, "task", id)
	if e != nil {
		return nil, e
	}
	a := Action{Entity: "TASK", Kind: "UPD", ID: id, Payload: p.Object{}}
	switch cmd.Name {
	case "task_move_project":
		if p.Str(task["parentId"]) != "" {
			return nil, errors.New("move the parent task; subtasks inherit its project")
		}
		target := p.Str(arg["projectId"])
		if _, e := Entity(s, "project", target); e != nil {
			return nil, e
		}
		a.Code = "HMP"
		a.Payload = p.Object{"task": taskWithChildren(s, task), "targetProjectId": target}
	case "task_set_tags":
		raw, ok := arg["tagIds"]
		if !ok {
			return nil, errors.New("tagIds array required; use an empty array to clear")
		}
		if _, ok = raw.([]any); !ok {
			return nil, errors.New("tagIds must be an array")
		}
		tags := []any{}
		for _, value := range p.Arr(raw) {
			tagID, ok := value.(string)
			if !ok || tagID == "TODAY" {
				return nil, errors.New("TODAY is a virtual schedule; use task_plan_today")
			}
			if _, e := Entity(s, "tag", tagID); e != nil {
				return nil, e
			}
			tags = addUnique(tags, tagID, true)
		}
		a.Code = "HU"
		a.Payload = p.Object{"task": p.Object{"id": id, "changes": p.Object{"tagIds": tags}}}
	case "attachment_add", "attachment_update":
		fields := p.Obj(arg["attachment"])
		if cmd.Name == "attachment_update" {
			fields = p.Obj(arg["changes"])
		}
		if len(fields) == 0 {
			return nil, errors.New("attachment fields required")
		}
		for k, v := range fields {
			if !slices.Contains([]string{"type", "title", "path", "icon", "originalImgPath"}, k) {
				return nil, errors.New("unsupported attachment field")
			}
			if _, ok := v.(string); !ok {
				return nil, errors.New("attachment fields must be strings")
			}
		}
		if v, ok := fields["type"]; ok && !slices.Contains([]string{"FILE", "LINK", "IMG", "COMMAND", "NOTE"}, p.Str(v)) {
			return nil, errors.New("invalid attachment type")
		}
		a.Payload["taskId"] = id
		if cmd.Name == "attachment_add" {
			if p.Str(fields["type"]) == "" || p.Str(fields["path"]) == "" {
				return nil, errors.New("attachment type and path required")
			}
			metadata := p.Clone(fields)
			metadata["id"] = c.ID()
			a.Code = "XA"
			a.Payload["taskAttachment"] = metadata
		} else {
			a.Code = "XU"
			a.Payload["taskAttachment"] = p.Object{"id": arg["attachmentId"], "changes": p.Clone(fields)}
		}
	case "attachment_delete":
		a.Code = "XD"
		a.Payload = p.Object{"taskId": id, "id": arg["attachmentId"]}
	}
	if a.Code == "XU" || a.Code == "XD" {
		found := false
		for _, raw := range p.Arr(task["attachments"]) {
			if p.Obj(raw)["id"] == arg["attachmentId"] {
				found = true
			}
		}
		if !found {
			return nil, errors.New("attachment not found")
		}
	}
	return []Action{a}, nil
}

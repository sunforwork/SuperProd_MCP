package engine

import (
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
)

func compileTaskCommands(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id := p.Str(arg["id"])
	task, err := Entity(s, "task", id)
	if err != nil {
		return nil, err
	}
	a := Action{Entity: "TASK", ID: id, Kind: "UPD", Payload: p.Object{}}
	switch cmd.Name {
	case "task_nest":
		parentID := p.Str(arg["targetParentId"])
		if _, err := Entity(s, "task", parentID); err != nil {
			return nil, err
		}
		anchor, ok := arg["afterTaskId"]
		if !ok {
			return nil, errors.New("afterTaskId required; null means beginning")
		}
		if anchor != nil {
			if _, ok := anchor.(string); !ok {
				return nil, errors.New("afterTaskId must be string or null")
			}
		}
		a.Code = "HCS"
		a.Payload = p.Object{"taskId": id, "targetParentId": parentID, "afterTaskId": anchor}
	case "task_promote":
		parent, err := Entity(s, "task", p.Str(task["parentId"]))
		if err != nil {
			return nil, errors.New("No parent for sub task")
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
		a.Code = "HC"
		a.Payload = p.Object{"task": p.Clone(task), "parentTagIds": p.Clone(parent["tagIds"]), "isPlanForToday": ci == "TODAY", "today": c.Today, "modified": c.Now}
	case "task_move_child":
		parentID := p.Str(arg["targetParentId"])
		if _, err := Entity(s, "task", parentID); err != nil {
			return nil, err
		}
		if p.Str(task["parentId"]) == "" {
			return nil, errors.New("task must be a child; use task_nest for a main task")
		}
		anchor, ok := arg["afterTaskId"]
		if !ok {
			return nil, errors.New("afterTaskId required; null means beginning")
		}
		if anchor != nil {
			if _, ok := anchor.(string); !ok {
				return nil, errors.New("afterTaskId must be string or null")
			}
		}
		a.Code = "TMS"
		a.Kind = "MOV"
		a.Payload = p.Object{"taskId": id, "srcTaskId": task["parentId"], "targetTaskId": parentID, "afterTaskId": anchor}
	case "task_child_to_top", "task_child_to_bottom":
		if p.Str(task["parentId"]) == "" {
			return nil, errors.New("child task required")
		}
		a.Code = "TMT"
		if cmd.Name == "task_child_to_bottom" {
			a.Code = "TMB"
		}
		a.Kind = "MOV"
		a.Payload = p.Object{"id": id, "parentId": task["parentId"]}
	case "task_set_subtasks_display":
		changes := p.Object{}
		a.Code = "TUU"
		a.Payload = p.Object{"task": p.Object{"id": id, "changes": changes}}
		switch p.Str(arg["mode"]) {
		case "show":
			a.Payload["clearedFields"] = []any{"_hideSubTasksMode"}
		case "hideDone":
			changes["_hideSubTasksMode"] = 1
		case "hideAll":
			changes["_hideSubTasksMode"] = 2
		default:
			return nil, errors.New("mode must be show, hideDone, or hideAll")
		}
	case "time_remove":
		day := p.Str(arg["date"])
		if day == "" {
			day = c.Today
		}
		if !validDate(day) || !nonnegativeNumber(arg["durationMs"]) {
			return nil, errors.New("valid date and nonnegative milliseconds required")
		}
		a.Code = "TR"
		a.Payload = p.Object{"id": id, "date": day, "duration": arg["durationMs"]}
	default:
		return nil, fmt.Errorf("%w: command %s", ErrUnsupported, cmd.Name)
	}
	return []Action{a}, nil
}

func validateTaskEdits(ch p.Object) error {
	for key, value := range ch {
		switch key {
		case "title", "notes", "issueId", "issueProviderId", "issueType", "repeatCfgId", "reminderId":
			if value == nil && key != "title" {
				continue
			}
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s must be a string", key)
			}
		case "timeEstimate", "timeSpent", "doneOn", "dueWithTime", "deadlineWithTime", "deadlineRemindAt", "remindAt", "issueLastUpdated", "issueAttachmentNr", "issuePoints":
			if value == nil {
				continue
			}
			if !nonnegativeNumber(value) {
				return fmt.Errorf("%s must be nonnegative milliseconds/number", key)
			}
		case "priority":
			if value == nil {
				continue
			}
			if !isNumber(value) || p.Int(value) < 1 || p.Int(value) > 3 || p.Num(value) != float64(p.Int(value)) {
				return errors.New("priority must be 1, 2, 3 or null")
			}
		case "dueDay", "deadlineDay":
			if value == nil {
				continue
			}
			if !validDate(p.Str(value)) {
				return fmt.Errorf("%s must be YYYY-MM-DD or null", key)
			}
		case "isDone", "issueWasUpdated", "hasPlannedTime":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s must be boolean", key)
			}
		case "issueTimeTracked", "issueLastSyncedValues":
			if value != nil && p.Obj(value) == nil {
				return fmt.Errorf("%s must be object or null", key)
			}
		case "projectId":
			if _, ok := value.(string); !ok {
				return errors.New("projectId must be a string")
			}
		case "tagIds":
			if _, ok := value.([]any); !ok {
				return errors.New("tagIds must be array")
			}
			for _, v := range p.Arr(value) {
				if _, ok := v.(string); !ok {
					return errors.New("tagIds must contain strings")
				}
			}
		case "timeSpentOnDay":
			if p.Obj(value) == nil {
				return errors.New("timeSpentOnDay must be object")
			}
			for date, v := range p.Obj(value) {
				if !validDate(date) || !nonnegativeNumber(v) {
					return errors.New("invalid daily time")
				}
			}
		default:
			return fmt.Errorf("%w: task field %s; use the dedicated relationship/content tool where available", ErrUnsupported, key)
		}
	}
	return nil
}

package engine

import (
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"slices"
)

func applyBacklogTransfer(s p.Object, a Action) error {
	pl := a.Payload
	ci := p.Str(pl["workContextId"])
	if ci == "" {
		ci = p.Str(pl["projectId"])
	}
	project, err := Entity(s, "project", ci)
	if err != nil {
		return err
	}
	id := p.Str(pl["taskId"])
	regular, backlog := p.Arr(project["taskIds"]), p.Arr(project["backlogTaskIds"])
	switch a.Code {
	case "PRB":
		if !p.Bool(project["isEnableBacklog"]) {
			return nil
		}
		project["taskIds"], project["backlogTaskIds"] = remove(regular, id), moveAfter(backlog, id, p.Str(pl["afterTaskId"]))
	case "PBR":
		project["backlogTaskIds"] = remove(backlog, id)
		if pl["afterTaskId"] == nil && pl["target"] == "DONE" {
			project["taskIds"] = append(remove(regular, id), id)
		} else {
			project["taskIds"] = moveAfter(regular, id, p.Str(pl["afterTaskId"]))
		}
	case "PAB":
		if !p.Bool(project["isEnableBacklog"]) || has(backlog, id) {
			return nil
		}
		project["taskIds"], project["backlogTaskIds"] = remove(regular, id), prepend(backlog, id)
	case "PAR":
		if has(regular, id) || !has(backlog, id) {
			return nil
		}
		project["backlogTaskIds"] = remove(backlog, id)
		if p.Bool(pl["isMoveToTop"]) {
			project["taskIds"] = prepend(regular, id)
		} else {
			project["taskIds"] = append(regular, id)
		}
	}
	return nil
}

func compileBacklog(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id := p.Str(arg["id"])
	task, err := Entity(s, "task", id)
	if err != nil {
		return nil, err
	}
	a := Action{Kind: "MOV", Entity: "TASK", ID: id, Payload: p.Object{}}
	if cmd.Name == "task_send_to_backlog" || cmd.Name == "task_send_to_regular" {
		projectID := p.Str(arg["projectId"])
		if _, err := Entity(s, "project", projectID); err != nil {
			return nil, err
		}
		a.Code = "PAB"
		a.Payload = p.Object{"taskId": id, "projectId": projectID}
		if cmd.Name == "task_send_to_regular" {
			a.Code = "PAR"
			a.Payload["isMoveToTop"] = p.Bool(arg["isMoveToTop"])
		}
		return []Action{a}, nil
	}
	ct, ci, src, target := p.Str(arg["contextType"]), p.Str(arg["contextId"]), p.Str(arg["src"]), p.Str(arg["target"])
	if !slices.Contains([]string{"UNDONE", "DONE", "BACKLOG"}, src) || !slices.Contains([]string{"UNDONE", "DONE", "BACKLOG"}, target) {
		return nil, errors.New("src and target must be UNDONE, DONE or BACKLOG; use child tools for parent changes")
	}
	if target == "DONE" && src != "DONE" && p.Str(task["repeatCfgId"]) != "" {
		return nil, fmt.Errorf("%w: recurring completion workflow", ErrUnsupported)
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
	if (src == "BACKLOG" || target == "BACKLOG") && ct != "PROJECT" {
		return nil, errors.New("backlog requires PROJECT context")
	}
	ordered, ok := arg["newOrderedIds"].([]any)
	if !ok {
		return nil, errors.New("newOrderedIds array required")
	}
	for _, v := range ordered {
		if _, ok := v.(string); !ok {
			return nil, errors.New("newOrderedIds must contain strings")
		}
	}
	var anchor any
	for i, value := range ordered {
		if value == id {
			if i > 0 {
				anchor = ordered[i-1]
			}
			break
		}
	}
	a.Payload = p.Object{"taskId": id, "workContextId": ci, "afterTaskId": anchor}
	switch {
	case src != "BACKLOG" && target != "BACKLOG":
		a.Code = "WM"
		a.Entity, a.ID = ct, ci
		a.Payload["workContextType"], a.Payload["src"], a.Payload["target"] = ct, src, target
	case src == "BACKLOG" && target == "BACKLOG":
		a.Code = "PM"
	case src == "BACKLOG":
		a.Code = "PBR"
		a.Payload["src"], a.Payload["target"] = src, target
	default:
		a.Code = "PRB"
	}
	return []Action{a}, nil
}

package engine

import (
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"slices"
)

func taskFieldAction(id string, changes p.Object) Action {
	return Action{Code: "HU", Kind: "UPD", Entity: "TASK", ID: id, Payload: p.Object{"task": p.Object{"id": id, "changes": changes}}}
}

func localTaskFollowups(initial p.Object, actions []Action, c Context) ([]Action, error) {
	state := p.Clone(initial)
	out := []Action{}
	var dispatch func(Action) error
	dispatch = func(a Action) error {
		if err := Apply(state, a, c); err != nil {
			return err
		}
		out = append(out, a)
		switch a.Code {
		case "WM", "PBR":
			src, target := p.Str(a.Payload["src"]), p.Str(a.Payload["target"])
			if (src == "DONE" || src == "BACKLOG") && target == "UNDONE" {
				return dispatch(taskFieldAction(p.Str(a.Payload["taskId"]), p.Object{"isDone": false}))
			}
			if (src == "UNDONE" || src == "BACKLOG") && target == "DONE" {
				return dispatch(taskFieldAction(p.Str(a.Payload["taskId"]), p.Object{"isDone": true}))
			}
		case "HA", "TA":
			task := p.Obj(a.Payload["task"])
			if p.Num(task["timeEstimate"]) == 0 {
				key := "defaultEstimate"
				if p.Str(task["parentId"]) != "" || a.Code == "TA" && p.Str(a.Payload["parentId"]) != "" {
					key = "defaultEstimateSubTasks"
				}
				estimate := p.Obj(p.Obj(state["globalConfig"])["timeTracking"])[key]
				if p.Num(estimate) > 0 {
					return dispatch(taskFieldAction(p.Str(task["id"]), p.Object{"timeEstimate": estimate}))
				}
			}
		case "HCS", "HC":
			id := p.Str(a.Payload["taskId"])
			if a.Code == "HC" {
				id = p.Str(p.Obj(a.Payload["task"])["id"])
			}
			task := p.Obj(entities(state, "task")[id])
			if task == nil {
				return nil
			}
			own := remove(p.Arr(task["tagIds"]), "TODAY")
			if len(own) == 0 {
				return nil
			}
			if a.Code == "HCS" {
				if task["parentId"] != a.Payload["targetParentId"] {
					return nil
				}
			} else {
				if p.Str(task["parentId"]) != "" {
					return nil
				}
				parent := p.Obj(entities(state, "task")[p.Str(p.Obj(a.Payload["task"])["parentId"])])
				oldTags := p.Arr(parent["tagIds"])
				if oldTags == nil {
					oldTags = p.Arr(a.Payload["parentTagIds"])
				}
				if slices.Equal(p.Strings(own), p.Strings(remove(oldTags, "TODAY"))) {
					return nil
				}
			}
			return dispatch(taskFieldAction(id, p.Object{"tagIds": own}))
		case "HU":
			payload := p.Obj(a.Payload["task"])
			changes := p.Obj(payload["changes"])
			if !p.Bool(changes["isDone"]) || !p.Bool(p.Obj(p.Obj(state["globalConfig"])["tasks"])["isAutoMarkParentAsDone"]) {
				return nil
			}
			task := p.Obj(entities(state, "task")[p.Str(payload["id"])])
			parentID := p.Str(task["parentId"])
			if parentID == "" {
				return nil
			}
			parent := p.Obj(entities(state, "task")[parentID])
			if parent == nil {
				return errors.New("completion parent missing")
			}
			for _, id := range p.Strings(parent["subTaskIds"]) {
				child := p.Obj(entities(state, "task")[id])
				if child == nil {
					return errors.New("completion child missing")
				}
				if !p.Bool(child["isDone"]) {
					return nil
				}
			}
			if p.Str(parent["repeatCfgId"]) != "" {
				return fmt.Errorf("%w: recurring parent completion", ErrUnsupported)
			}
			return dispatch(taskFieldAction(parentID, p.Object{"isDone": true}))
		}
		return nil
	}
	for _, a := range actions {
		if err := dispatch(a); err != nil {
			return nil, err
		}
	}
	return out, nil
}

package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
)

func compileTimeEdit(s p.Object, cmd Command, c Context) ([]Action, error) {
	id := p.Str(cmd.Args["id"])
	task, err := Entity(s, "task", id)
	if err != nil {
		return nil, err
	}
	days, estimate := p.Clone(p.Obj(task["timeSpentOnDay"])), task["timeEstimate"]
	if days == nil {
		days = p.Object{}
	}
	if cmd.Name == "time_delete_day" {
		day, ok := cmd.Args["date"].(string)
		if !ok {
			return nil, errors.New("date row required")
		}
		delete(days, day)
	} else {
		days, estimate = p.Clone(p.Obj(cmd.Args["days"])), cmd.Args["timeEstimate"]
	}
	if !matchesTaskSchema(nativeTaskFieldRule("timeEstimate"), estimate) || !matchesTaskSchema(nativeTaskFieldRule("timeSpentOnDay"), days) {
		return nil, errors.New("timeEstimate and days must match the pinned native Task fields")
	}
	return []Action{taskFieldAction(id, p.Object{"timeEstimate": estimate, "timeSpentOnDay": days})}, nil
}

func compileHistoryTimeEdit(s p.Object, args p.Object) ([]Action, error) {
	id := p.Str(args["id"])
	task, err := Entity(s, "task", id)
	if err != nil {
		return nil, err
	}
	day, ok := args["date"].(string)
	if !ok || !matchesTaskSchema(nativeTaskFieldRule("timeEstimate"), args["durationMs"]) {
		return nil, errors.New("explicit date key and native numeric milliseconds required")
	}
	days := p.Clone(p.Obj(task["timeSpentOnDay"]))
	if days == nil {
		days = p.Object{}
	}
	days[day] = args["durationMs"]
	return []Action{taskFieldAction(id, p.Object{"timeSpentOnDay": days})}, nil
}

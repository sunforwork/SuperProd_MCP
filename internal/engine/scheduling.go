package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
	"time"
)

func applyPlanToday(s p.Object, a Action, c Context) error {
	pl := a.Payload
	ids := p.Arr(pl["taskIds"])
	tasks := entities(s, "task")
	tag := p.Obj(entities(s, "tag")["TODAY"])
	if tag == nil {
		return errors.New("TODAY tag missing")
	}
	today := c.Today
	if value, ok := pl["today"].(string); ok {
		today = value
	}
	offset := DayOffset(s).Milliseconds()
	if v, exists := pl["startOfNextDayDiffMs"]; exists && v != nil {
		offset = p.Int(v)
	}
	parents := p.Obj(pl["parentTaskMap"])
	stored := p.Arr(tag["taskIds"])
	added := []any{}
	for _, raw := range ids {
		id := p.Str(raw)
		if tasks[id] != nil && !has(stored, id) && (p.Str(parents[id]) == "" || !has(stored, p.Str(parents[id]))) {
			added = append(added, id)
		}
	}
	updates := []string{}
	clear := p.Bool(pl["isClearScheduledTime"])
	for _, raw := range ids {
		id := p.Str(raw)
		task := p.Obj(tasks[id])
		if task == nil {
			continue
		}
		if p.Str(task["dueDay"]) != today {
			if has(added, id) || has(stored, id) {
				updates = append(updates, id)
			}
		} else if clear {
			_, reminder := task["remindAt"]
			_, timed := task["dueWithTime"]
			if reminder || timed {
				updates = append(updates, id)
			}
		}
	}
	if len(added) == 0 && len(updates) == 0 {
		return nil
	}
	loc := c.Location
	if loc == nil {
		loc = time.UTC
	}
	for _, id := range updates {
		task := p.Obj(tasks[id])
		due, present := task["dueWithTime"]
		clearTime := false
		if clear {
			clearTime = p.Num(due) != 0
		} else if present && due != nil {
			timestamp := p.Num(due)
			clearTime = math.IsNaN(timestamp) || math.IsInf(timestamp, 0) || timestamp <= 0 || timestamp > 8640000000000000
			if !clearTime {
				clearTime = time.UnixMilli(int64(timestamp)-offset).In(loc).Format("2006-01-02") != today
			}
		}
		task["dueDay"] = today
		delete(task, "remindAt")
		if clearTime {
			delete(task, "dueWithTime")
		}
	}
	next := []any{}
	for _, raw := range append(added, stored...) {
		if !has(next, p.Str(raw)) {
			next = append(next, raw)
		}
	}
	tag["taskIds"] = next
	days := p.Obj(p.Obj(s["planner"])["days"])
	for day, raw := range days {
		old := p.Arr(raw)
		next := []any{}
		for _, id := range old {
			if !has(ids, p.Str(id)) {
				next = append(next, id)
			}
		}
		if len(next) != len(old) {
			days[day] = next
		}
	}
	return nil
}

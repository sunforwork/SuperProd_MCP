package engine

import (
	"encoding/json"
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
	"sort"
	"time"
)

func samePrimitive(a, b any) bool {
	switch v := a.(type) {
	case nil:
		return b == nil
	case string:
		w, ok := b.(string)
		return ok && v == w
	case bool:
		w, ok := b.(bool)
		return ok && v == w
	case json.Number, float64, int, int64:
		switch b.(type) {
		case json.Number, float64, int, int64:
			return p.Num(a) == p.Num(b)
		}
	}
	return false
}
func timestampDay(v any, offset int64, c Context) string {
	n := p.Num(v)
	if n <= 0 || n > 8640000000000000 || math.IsNaN(n) || math.IsInf(n, 0) {
		return ""
	}
	loc := c.Location
	if loc == nil {
		loc = time.UTC
	}
	return time.UnixMilli(int64(n) - offset).In(loc).Format("2006-01-02")
}
func setOptional(dst, src p.Object, key string) {
	if v, ok := src[key]; ok {
		dst[key] = v
	} else {
		delete(dst, key)
	}
}
func removePlannerIDs(s p.Object, ids []any) {
	for day, raw := range p.Obj(p.Obj(s["planner"])["days"]) {
		next := p.Arr(raw)
		for _, id := range ids {
			next = remove(next, p.Str(id))
		}
		p.Obj(p.Obj(s["planner"])["days"])[day] = next
	}
}
func deadlineToday(task p.Object, today string, offset int64, c Context) bool {
	if day := timestampDay(task["deadlineWithTime"], offset, c); day != "" {
		return day == today
	}
	return p.Str(task["deadlineDay"]) == today
}
func dueScheduleDay(task p.Object, offset int64, c Context) string {
	if day := timestampDay(task["dueWithTime"], offset, c); day != "" {
		return day
	}
	return p.Str(task["dueDay"])
}

func autoPlanDeadline(s p.Object, id, today string, offset int64, c Context) {
	task := p.Obj(entities(s, "task")[id])
	tag := p.Obj(entities(s, "tag")["TODAY"])
	if task == nil || tag == nil || p.Bool(task["isDone"]) || !deadlineToday(task, today, offset, c) {
		return
	}
	stored := p.Arr(tag["taskIds"])
	if parent := p.Str(task["parentId"]); parent != "" {
		if has(stored, parent) || dueScheduleDay(p.Obj(entities(s, "task")[parent]), offset, c) == today {
			return
		}
	}
	day := dueScheduleDay(task, offset, c)
	if day > today {
		return
	}
	if day != today {
		task["dueDay"] = today
		if day != "" && timestampDay(task["dueWithTime"], offset, c) != "" {
			delete(task, "dueWithTime")
			delete(task, "remindAt")
		}
		removePlannerIDs(s, []any{id})
	}
	tag["taskIds"] = addUnique(stored, id, false)
}
func applySchedule(s p.Object, a Action, c Context) error {
	pl := a.Payload

	defer func() {
		if a.Code == "HS" {
			removePlannerIDs(s, []any{p.Obj(pl["task"])["id"]})
		}
		if a.Code == "HSX" {
			removePlannerIDs(s, []any{pl["id"]})
		}
	}()
	id := p.Str(pl["taskId"])
	if id == "" {
		id = p.Str(pl["id"])
	}
	if id == "" {
		id = p.Str(p.Obj(pl["task"])["id"])
	}
	task := p.Obj(entities(s, "task")[id])
	tag := p.Obj(entities(s, "tag")["TODAY"])
	if a.Code != "HDT" && task == nil {

		if a.Code == "HSX" && !p.Bool(pl["isLeaveInToday"]) && tag != nil {
			tag["taskIds"] = remove(p.Arr(tag["taskIds"]), id)
		}
		return nil
	}
	switch a.Code {
	case "HS", "HSR":
		day := timestampDay(pl["dueWithTime"], DayOffset(s).Milliseconds(), c)
		if day == "" {
			return errors.New("invalid scheduled timestamp")
		}
		if tag == nil {
			return errors.New("TODAY tag missing")
		}
		stored := p.Arr(tag["taskIds"])
		isToday := day == c.Today
		oldReminder, oldHas := task["remindAt"]
		reminder, newHas := pl["remindAt"]
		if samePrimitive(task["dueWithTime"], pl["dueWithTime"]) && oldHas == newHas && samePrimitive(oldReminder, reminder) && isToday == has(stored, id) && !p.Bool(pl["isMoveToBacklog"]) {
			return nil
		}
		task["dueWithTime"] = pl["dueWithTime"]
		delete(task, "dueDay")
		setOptional(task, pl, "remindAt")
		if p.Bool(pl["isMoveToBacklog"]) {
			proj := p.Obj(entities(s, "project")[p.Str(task["projectId"])])
			if proj != nil && p.Bool(proj["isEnableBacklog"]) && !has(p.Arr(proj["backlogTaskIds"]), id) {
				proj["taskIds"] = remove(p.Arr(proj["taskIds"]), id)
				proj["backlogTaskIds"] = prepend(p.Arr(proj["backlogTaskIds"]), id)
			}
		}
		if isToday {
			tag["taskIds"] = addUnique(stored, id, false)
		} else {
			tag["taskIds"] = remove(stored, id)
		}
	case "HSX":
		delete(task, "dueWithTime")
		delete(task, "remindAt")
		delete(task, "dueDay")
		if p.Bool(pl["isLeaveInToday"]) {
			today := c.Today
			if v, ok := pl["today"].(string); ok {
				today = v
			}
			task["dueDay"] = today
		} else if tag != nil {
			tag["taskIds"] = remove(p.Arr(tag["taskIds"]), id)
		}
	case "HRX":
		delete(task, "remindAt")
	case "HXD":
		delete(task, "deadlineDay")
		delete(task, "deadlineWithTime")
		delete(task, "deadlineRemindAt")
	case "HCR":
		delete(task, "deadlineRemindAt")
	case "HDL":
		if day := p.Str(pl["deadlineDay"]); day != "" && !datePattern.MatchString(day) {
			return nil
		}
		setOptional(task, pl, "deadlineDay")
		setOptional(task, pl, "deadlineWithTime")
		setOptional(task, pl, "deadlineRemindAt")
		if p.Num(pl["deadlineWithTime"]) != 0 {
			delete(task, "deadlineDay")
		}
		if p.Str(pl["deadlineDay"]) != "" {
			delete(task, "deadlineWithTime")
		}
		if today := p.Str(pl["autoPlanToday"]); datePattern.MatchString(today) && pl["autoPlanStartOfNextDayDiffMs"] != nil {
			autoPlanDeadline(s, id, today, p.Int(pl["autoPlanStartOfNextDayDiffMs"]), c)
		}
	case "HDT":
		today := p.Str(pl["today"])
		if !datePattern.MatchString(today) || pl["startOfNextDayDiffMs"] == nil {
			return nil
		}
		ids := p.Strings(pl["taskIds"])
		sort.SliceStable(ids, func(i, j int) bool {
			return p.Str(p.Obj(entities(s, "task")[ids[i]])["parentId"]) == "" && p.Str(p.Obj(entities(s, "task")[ids[j]])["parentId"]) != ""
		})
		for _, id := range ids {
			autoPlanDeadline(s, id, today, p.Int(pl["startOfNextDayDiffMs"]), c)
		}
	}
	return nil
}

func reminderAt(due int64, option string) (int64, bool, error) {
	mins := map[string]int64{"AtStart": 0, "m5": 5, "m10": 10, "m15": 15, "m30": 30, "h1": 60}
	if option == "DoNotRemind" {
		return 0, false, nil
	}
	m, ok := mins[option]
	if !ok {
		return 0, false, errors.New("reminder must be DoNotRemind, AtStart, m5, m10, m15, m30 or h1")
	}
	return due - m*60000, true, nil
}
func compileSchedule(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id := p.Str(arg["id"])
	task, e := Entity(s, "task", id)
	if e != nil {
		return nil, e
	}
	a := Action{ID: id, Entity: "TASK", Kind: "UPD", Payload: p.Object{}}
	switch cmd.Name {
	case "task_schedule", "task_reschedule":
		due := arg["dueWithTime"]
		if !nonnegativeNumber(due) || timestampDay(due, 0, c) == "" {
			return nil, errors.New("positive dueWithTime milliseconds required")
		}
		a.Code = "HS"
		if cmd.Name == "task_reschedule" {
			a.Code = "HSR"
		}
		a.Payload = p.Object{"task": p.Clone(task), "dueWithTime": due, "isMoveToBacklog": p.Bool(arg["isMoveToBacklog"])}
		remind, yes, e := reminderAt(p.Int(due), p.Str(arg["reminder"]))
		if e != nil {
			return nil, e
		}
		if yes {
			a.Payload["remindAt"] = remind
		}
	case "task_unschedule":
		a.Code = "HSX"
		a.Payload = p.Object{"id": id, "isLeaveInToday": p.Bool(arg["isLeaveInToday"]), "today": c.Today}
	case "task_dismiss_reminder":
		a.Code = "HRX"
		a.Payload["id"] = id
	case "task_remove_deadline":
		a.Code = "HXD"
		a.Payload["taskId"] = id
	case "task_dismiss_deadline_reminder":
		a.Code = "HCR"
		a.Payload["taskId"] = id
	case "task_set_deadline":
		a.Code = "HDL"
		a.Payload["taskId"] = id
		day := p.Str(arg["date"])
		due, present := arg["deadlineWithTime"]
		if (day != "") == present {
			return nil, errors.New("provide exactly one of date or deadlineWithTime")
		}
		if day != "" {
			if !validDate(day) {
				return nil, errors.New("invalid deadline date")
			}
			a.Payload["deadlineDay"] = day
		} else {
			if !nonnegativeNumber(due) || timestampDay(due, 0, c) == "" {
				return nil, errors.New("positive deadline timestamp required")
			}
			a.Payload["deadlineWithTime"] = due
			remind, yes, e := reminderAt(p.Int(due), p.Str(arg["reminder"]))
			if e != nil {
				return nil, e
			}
			if yes {
				a.Payload["deadlineRemindAt"] = remind
			}
		}
		if deadlineToday(a.Payload, c.Today, DayOffset(s).Milliseconds(), c) {
			a.Payload["autoPlanToday"] = c.Today
			a.Payload["autoPlanStartOfNextDayDiffMs"] = DayOffset(s).Milliseconds()
		}
	}
	return []Action{a}, nil
}
